// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// forceDestroyScript is a scripted S3 server for the force_destroy delete
// path. It answers the empty-bucket calls with the smallest payloads the AWS
// SDK accepts and records the order of the calls, which is what the test
// asserts: an emptied bucket is a sequence, not a single API call.
type forceDestroyScript struct {
	t *testing.T

	mu    sync.Mutex
	calls []string

	// versioned selects whether GetBucketVersioning reports a versioning
	// configuration, which gates the version/delete-marker pass.
	versioned bool
	// listObjectsStatus and listObjectsBody override the ListObjectsV2 answer;
	// listObjectsStatus 0 means 200 with one listed object.
	listObjectsStatus int
	listObjectsBody   string
	// listVersionsStatus and listVersionsBody override ListObjectVersions.
	listVersionsStatus int
	listVersionsBody   string
	// versioningStatus and versioningBody override GetBucketVersioning.
	versioningStatus int
	versioningBody   string
	// abortStatus and abortBody override AbortMultipartUpload (default 204).
	abortStatus int
	abortBody   string
	// deleteObjectsStatus overrides the DeleteObjects status; deleteObjectsBody
	// is returned verbatim (for a per-object error).
	deleteObjectsStatus int
	deleteObjectsBody   string
	// deleteBucketStatus and deleteBucketBody override DeleteBucket (default
	// 200). A zero status with no body means 200.
	deleteBucketStatus int
	deleteBucketBody   string
}

func (s *forceDestroyScript) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *forceDestroyScript) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *forceDestroyScript) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	q := r.URL.Query()

	switch {
	case r.Method == http.MethodGet && q.Has("uploads"):
		s.record("ListMultipartUploads")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Bucket>tf-test</Bucket><IsTruncated>false</IsTruncated>
<Upload><Key>big.bin</Key><UploadId>upload-1</UploadId><Initiated>2026-01-01T00:00:00Z</Initiated></Upload>
</ListMultipartUploadsResult>`)

	case r.Method == http.MethodDelete && q.Has("uploadId"):
		s.record("AbortMultipartUpload")
		if s.abortStatus != 0 {
			w.WriteHeader(s.abortStatus)
			fmt.Fprint(w, s.abortBody)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && q.Has("list-type"):
		s.record("ListObjectsV2")
		if s.listObjectsStatus != 0 {
			w.WriteHeader(s.listObjectsStatus)
			fmt.Fprint(w, s.listObjectsBody)
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>tf-test</Name><KeyCount>1</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>
<Contents><Key>one.txt</Key><Size>1</Size></Contents>
</ListBucketResult>`)

	case r.Method == http.MethodPost && q.Has("delete"):
		s.record("DeleteObjects")
		if s.deleteObjectsStatus != 0 {
			w.WriteHeader(s.deleteObjectsStatus)
			fmt.Fprint(w, s.deleteObjectsBody)
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+s.deleteObjectsBody+`</DeleteResult>`)

	case r.Method == http.MethodGet && q.Has("versioning"):
		s.record("GetBucketVersioning")
		if s.versioningStatus != 0 {
			w.WriteHeader(s.versioningStatus)
			fmt.Fprint(w, s.versioningBody)
			return
		}
		status := ""
		if s.versioned {
			status = "<Status>Enabled</Status>"
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+status+`</VersioningConfiguration>`)

	case r.Method == http.MethodGet && q.Has("versions"):
		s.record("ListObjectVersions")
		if s.listVersionsStatus != 0 {
			w.WriteHeader(s.listVersionsStatus)
			fmt.Fprint(w, s.listVersionsBody)
			return
		}
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>tf-test</Name><IsTruncated>false</IsTruncated>
<Version><Key>one.txt</Key><VersionId>v2</VersionId><IsLatest>true</IsLatest><Size>1</Size></Version>
<DeleteMarker><Key>gone.txt</Key><VersionId>v3</VersionId><IsLatest>true</IsLatest></DeleteMarker>
</ListVersionsResult>`)

	case r.Method == http.MethodDelete:
		s.record("DeleteBucket")
		if s.deleteBucketStatus != 0 {
			w.WriteHeader(s.deleteBucketStatus)
			fmt.Fprint(w, s.deleteBucketBody)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		s.t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, s3ErrorXML("UnroutedRequest", "no route for "+r.Method+" "+r.URL.Path))
	}
}

func TestBucketResourceForceDestroy(t *testing.T) {
	t.Parallel()

	t.Run("default refuses a non-empty bucket", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodDelete,
			status: http.StatusConflict,
			body:   s3ErrorXML("BucketNotEmpty", "The bucket you tried to delete is not empty"),
		})
		h := newCRUDHarness(t, NewBucketResource(), client)

		resp := h.delete(&bucketResourceModel{Name: types.StringValue("tf-test")})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error deleting bucket",
			"Could not delete bucket, unexpected error:")
		assertDiagnosticDetail(t, resp.Diagnostics, "BucketNotEmpty")
	})

	t.Run("empties a versioned bucket before deleting it", func(t *testing.T) {
		script := &forceDestroyScript{t: t, versioned: true}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		want := []string{
			"ListMultipartUploads",
			"AbortMultipartUpload",
			"ListObjectsV2",
			"DeleteObjects",
			"GetBucketVersioning",
			"ListObjectVersions",
			"DeleteObjects",
			"DeleteBucket",
		}
		if got := script.recorded(); !equalStrings(got, want) {
			t.Errorf("call sequence = %v, want %v", got, want)
		}
	})

	t.Run("skips the version pass on an unversioned bucket", func(t *testing.T) {
		script := &forceDestroyScript{t: t}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		want := []string{
			"ListMultipartUploads",
			"AbortMultipartUpload",
			"ListObjectsV2",
			"DeleteObjects",
			"GetBucketVersioning",
			"DeleteBucket",
		}
		if got := script.recorded(); !equalStrings(got, want) {
			t.Errorf("call sequence = %v, want %v", got, want)
		}
	})

	t.Run("a bucket that vanished during the emptying is done", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                 t,
			listObjectsStatus: http.StatusNotFound,
			listObjectsBody:   s3ErrorXML("NoSuchBucket", "gone"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		script.assertNoCall(t, "DeleteObjects")
	})

	t.Run("an aborted upload that is already gone does not fail the delete", func(t *testing.T) {
		script := &forceDestroyScript{
			t:           t,
			abortStatus: http.StatusNotFound,
			abortBody:   s3ErrorXML("NoSuchUpload", "upload already aborted"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		script.assertCall(t, "DeleteBucket")
	})

	t.Run("a per-object deletion error names the object", func(t *testing.T) {
		script := &forceDestroyScript{
			t: t,
			deleteObjectsBody: `<Error><Key>one.txt</Key><Code>AccessDenied</Code>` +
				`<Message>denied by policy</Message></Error>`,
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error emptying bucket before deletion",
			"one.txt: denied by policy")
		script.assertNoCall(t, "DeleteBucket")
	})

	t.Run("a listing failure is reported and the bucket is kept", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                 t,
			listObjectsStatus: http.StatusForbidden,
			listObjectsBody:   s3ErrorXML("AccessDenied", "no listing for you"),
			versioned:         true,
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error emptying bucket before deletion",
			"no listing for you")
		script.assertNoCall(t, "DeleteBucket")
	})

	t.Run("a failed upload abort keeps the bucket", func(t *testing.T) {
		script := &forceDestroyScript{
			t:           t,
			abortStatus: http.StatusInternalServerError,
			abortBody:   s3ErrorXML("InternalError", "abort exploded"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error emptying bucket before deletion", "abort exploded")
		script.assertNoCall(t, "DeleteBucket")
	})

	t.Run("a version-listing failure keeps the bucket", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                  t,
			versioned:          true,
			listVersionsStatus: http.StatusForbidden,
			listVersionsBody:   s3ErrorXML("AccessDenied", "no versions for you"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error emptying bucket before deletion", "no versions for you")
		script.assertNoCall(t, "DeleteBucket")
	})

	t.Run("a failed versioning read keeps the bucket", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                t,
			versioningStatus: http.StatusForbidden,
			versioningBody:   s3ErrorXML("AccessDenied", "no versioning for you"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error emptying bucket before deletion", "no versioning for you")
		script.assertNoCall(t, "DeleteBucket")
	})

	t.Run("a failed batch delete keeps the bucket", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                   t,
			deleteObjectsStatus: http.StatusForbidden,
			deleteObjectsBody:   s3ErrorXML("AccessDenied", "no delete for you"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error emptying bucket before deletion", "no delete for you")
		script.assertNoCall(t, "DeleteBucket")
	})

	t.Run("a versioning lookup that reports a vanished bucket is treated as done", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                t,
			versioningStatus: http.StatusNotFound,
			versioningBody:   s3ErrorXML("NoSuchBucket", "gone"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		script.assertCall(t, "DeleteBucket")
	})

	t.Run("an abort that reports a vanished bucket is treated as done", func(t *testing.T) {
		script := &forceDestroyScript{
			t:           t,
			abortStatus: http.StatusNotFound,
			abortBody:   s3ErrorXML("NoSuchBucket", "gone"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		script.assertCall(t, "DeleteBucket")
	})

	t.Run("a batch delete that reports a vanished bucket is treated as done", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                   t,
			deleteObjectsStatus: http.StatusNotFound,
			deleteObjectsBody:   s3ErrorXML("NoSuchBucket", "gone"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		script.assertCall(t, "DeleteBucket")
	})

	t.Run("a failed DeleteBucket is still reported after emptying", func(t *testing.T) {
		script := &forceDestroyScript{
			t:                  t,
			deleteBucketStatus: http.StatusForbidden,
			deleteBucketBody:   s3ErrorXML("AccessDenied", "cannot delete"),
		}
		h := newCRUDHarness(t, NewBucketResource(), httptestClient(t, script))

		resp := h.delete(&bucketResourceModel{
			Name:         types.StringValue("tf-test"),
			ForceDestroy: types.BoolValue(true),
		})

		assertErrorDiagnostic(t, resp.Diagnostics, "Error deleting bucket", "cannot delete")
	})
}

func (s *forceDestroyScript) assertCall(t *testing.T, want string) {
	t.Helper()
	for _, call := range s.recorded() {
		if call == want {
			return
		}
	}
	t.Errorf("expected call %q, recorded %v", want, s.recorded())
}

func (s *forceDestroyScript) assertNoCall(t *testing.T, unwanted string) {
	t.Helper()
	for _, call := range s.recorded() {
		if call == unwanted {
			t.Errorf("unexpected call %q, recorded %v", unwanted, s.recorded())
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestIsNoSuchUpload(t *testing.T) {
	if !isNoSuchUpload(fmt.Errorf("wrapped: %w", &rustfs.APIError{Code: "NoSuchUpload"})) {
		t.Error("a structured NoSuchUpload code must be recognised")
	}
	if !isNoSuchUpload(fmt.Errorf("server said NoSuchUpload here")) {
		t.Error("the payload wording must be recognised for endpoints without a code")
	}
	if isNoSuchUpload(errors.New("AccessDenied")) {
		t.Error("an unrelated error must not be treated as a vanished upload")
	}
}
