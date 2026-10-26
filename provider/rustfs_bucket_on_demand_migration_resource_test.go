// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketOnDemandMigrationResourceSchema(t *testing.T) {
	r := NewBucketOnDemandMigrationResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema is not a valid implementation: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"id", "bucket", "enabled", "version", "source", "filter", "policy", "updated_at"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	bucket, ok := attrs["bucket"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("bucket attribute is %T", attrs["bucket"])
	}
	if !bucket.Required || !hasRequiresReplace(bucket.PlanModifiers) {
		t.Error("bucket must be required and carry RequiresReplace")
	}

	enabled, ok := attrs["enabled"].(schema.BoolAttribute)
	if !ok || !enabled.Optional || !enabled.Computed || enabled.Default == nil {
		t.Error("enabled must be Optional+Computed with a default")
	}

	source, ok := attrs["source"].(schema.SingleNestedAttribute)
	if !ok || !source.Required {
		t.Fatalf("source must be a required SingleNestedAttribute, got %T", attrs["source"])
	}
	for _, name := range []string{"provider", "endpoint", "region", "bucket", "path_style", "credentials", "tls", "azure", "gcs"} {
		if _, ok := source.Attributes[name]; !ok {
			t.Errorf("source is missing %q", name)
		}
	}

	credentials, ok := source.Attributes["credentials"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("credentials is %T", source.Attributes["credentials"])
	}
	accessKey, ok := credentials.Attributes["access_key"].(schema.StringAttribute)
	if !ok || !accessKey.Sensitive || !accessKey.Required {
		t.Error("credentials.access_key must be required and sensitive")
	}
	secretKey, ok := credentials.Attributes["secret_key"].(schema.StringAttribute)
	if !ok || !secretKey.Sensitive || !secretKey.Required {
		t.Error("credentials.secret_key must be required and sensitive")
	}
	sessionToken, ok := credentials.Attributes["session_token"].(schema.StringAttribute)
	if !ok || !sessionToken.Sensitive {
		t.Error("credentials.session_token must be sensitive")
	}

	azure, ok := source.Attributes["azure"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("azure is %T", source.Attributes["azure"])
	}
	for _, name := range []string{"account_key", "sas_token"} {
		attr, ok := azure.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Sensitive {
			t.Errorf("source.azure.%s must be sensitive", name)
		}
	}
	gcs, ok := source.Attributes["gcs"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("gcs is %T", source.Attributes["gcs"])
	}
	serviceAccount, ok := gcs.Attributes["service_account_json"].(schema.StringAttribute)
	if !ok || !serviceAccount.Sensitive || !serviceAccount.Required {
		t.Error("source.gcs.service_account_json must be required and sensitive")
	}

	policy, ok := attrs["policy"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("policy is %T", attrs["policy"])
	}
	for _, name := range []string{
		"head", "range_get", "source_error", "list_through", "respect_local_delete_marker",
		"preserve_etag", "copy_tags", "emit_events", "negative_cache_ttl_secs", "inline_max_bytes",
		"multipart_part_size_bytes", "max_concurrent_pulls", "pull_queue_capacity", "source_timeout",
		"bandwidth_limit_bytes_per_sec",
	} {
		if _, ok := policy.Attributes[name]; !ok {
			t.Errorf("policy is missing %q", name)
		}
	}
	inline, ok := policy.Attributes["inline_max_bytes"].(schema.Int64Attribute)
	if !ok || inline.Default == nil || len(inline.Validators) == 0 {
		t.Error("policy.inline_max_bytes must carry a default and a range validator")
	}
	fields := map[string]schema.Attribute{
		"negative_cache_ttl_secs":   policy.Attributes["negative_cache_ttl_secs"],
		"inline_max_bytes":          policy.Attributes["inline_max_bytes"],
		"multipart_part_size_bytes": policy.Attributes["multipart_part_size_bytes"],
		"max_concurrent_pulls":      policy.Attributes["max_concurrent_pulls"],
		"pull_queue_capacity":       policy.Attributes["pull_queue_capacity"],
	}
	for name, attr := range fields {
		number, ok := attr.(schema.Int64Attribute)
		if !ok || len(number.Validators) == 0 {
			t.Errorf("policy.%s must carry a range validator", name)
		}
	}
	if _, ok := attrs["version"].(schema.Int64Attribute); !ok || !attrs["version"].IsComputed() {
		t.Error("version must be a computed Int64Attribute")
	}
}

func TestBucketOnDemandMigrationResourceMetadata(t *testing.T) {
	r := NewBucketOnDemandMigrationResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_on_demand_migration" {
		t.Errorf("expected rustfs_bucket_on_demand_migration, got %s", resp.TypeName)
	}
}

func odmTestCredentials() *odmCredentialsModel {
	return &odmCredentialsModel{
		AccessKey:    types.StringValue("source-access"),
		SecretKey:    types.StringValue("source-secret"),
		SessionToken: types.StringNull(),
	}
}

func odmValidPlan() *bucketOnDemandMigrationResourceModel {
	return &bucketOnDemandMigrationResourceModel{
		ID:        types.StringUnknown(),
		Bucket:    types.StringValue("photos"),
		Enabled:   types.BoolValue(true),
		Version:   types.Int64Unknown(),
		UpdatedAt: types.StringUnknown(),
		Source: &odmSourceModel{
			Provider:    types.StringValue("minio"),
			Endpoint:    types.StringValue("https://legacy.example.com:9000"),
			Region:      types.StringValue("us-east-1"),
			Bucket:      types.StringValue("legacy-photos"),
			PathStyle:   types.StringValue("auto"),
			Credentials: odmTestCredentials(),
		},
		Filter: &odmFilterModel{
			Prefix:       types.StringNull(),
			SourcePrefix: types.StringValue("photos/"),
		},
	}
}

func odmStateFromPlan(plan *bucketOnDemandMigrationResourceModel, enabled bool) *bucketOnDemandMigrationResourceModel {
	return &bucketOnDemandMigrationResourceModel{
		ID:        types.StringValue("photos"),
		Bucket:    types.StringValue("photos"),
		Enabled:   types.BoolValue(enabled),
		Version:   types.Int64Value(1),
		UpdatedAt: types.StringValue("2026-10-20T10:00:00Z"),
		Source: &odmSourceModel{
			Provider:    types.StringValue("minio"),
			Endpoint:    types.StringValue("https://legacy.example.com:9000"),
			Region:      types.StringValue("us-east-1"),
			Bucket:      types.StringValue("legacy-photos"),
			PathStyle:   types.StringValue("auto"),
			Credentials: odmTestCredentials(),
			TLS: &odmTLSModel{
				SkipVerify: types.BoolValue(false),
				CACertPEM:  types.StringNull(),
			},
		},
		Filter: &odmFilterModel{
			Prefix:       types.StringNull(),
			SourcePrefix: types.StringValue("photos/"),
		},
		Policy: &odmPolicyModel{
			Head:                     types.StringValue("proxy"),
			RangeGet:                 types.StringValue("serve_and_backfill"),
			SourceError:              types.StringValue("propagate"),
			ListThrough:              types.BoolValue(false),
			RespectLocalDeleteMarker: types.BoolValue(true),
			PreserveETag:             types.BoolValue(true),
			CopyTags:                 types.BoolValue(false),
			EmitEvents:               types.BoolValue(true),
			NegativeCacheTTLSecs:     types.Int64Value(30),
			InlineMaxBytes:           types.Int64Value(16777216),
			MultipartPartSizeBytes:   types.Int64Value(67108864),
			MaxConcurrentPulls:       types.Int64Value(8),
			PullQueueCapacity:        types.Int64Value(1024),
			SourceTimeout: &odmSourceTimeoutModel{
				ConnectMS:   types.Int64Value(5000),
				FirstByteMS: types.Int64Value(15000),
				IdleMS:      types.Int64Value(30000),
			},
			BandwidthLimitBytesPerSec: types.Int64Null(),
		},
	}
}

func odmConfigJSON(enabled bool) string {
	return fmt.Sprintf(`{
  "version": 1,
  "enabled": %t,
  "source": {
    "provider": "minio",
    "endpoint": "https://legacy.example.com:9000",
    "region": "us-east-1",
    "bucket": "legacy-photos",
    "path_style": "auto",
    "credentials": {"access_key": "source-access", "secret_key": "REDACTED", "session_token": null},
    "tls": {"skip_verify": false, "ca_cert_pem": null}
  },
  "filter": {"prefix": null, "source_prefix": "photos/"},
  "policy": {
    "head": "proxy", "range_get": "serve_and_backfill", "source_error": "propagate",
    "list_through": false, "respect_local_delete_marker": true, "preserve_etag": true,
    "copy_tags": false, "emit_events": true, "negative_cache_ttl_secs": 30,
    "inline_max_bytes": 16777216, "multipart_part_size_bytes": 67108864,
    "max_concurrent_pulls": 8, "pull_queue_capacity": 1024,
    "source_timeout": {"connect_ms": 5000, "first_byte_ms": 15000, "idle_ms": 30000},
    "bandwidth_limit_bytes_per_sec": null
  }
}`, enabled)
}

func TestBucketOnDemandMigrationResourceValidateConfig(t *testing.T) {
	harness := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), cannedClient(t, http.StatusOK, "{}"))

	tests := []struct {
		name    string
		mutate  func(*bucketOnDemandMigrationResourceModel)
		summary string
		detail  string
	}{
		{
			name:   "valid minio source",
			mutate: func(*bucketOnDemandMigrationResourceModel) {},
		},
		{
			name: "missing endpoint",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Provider = types.StringValue("s3")
				model.Source.Endpoint = types.StringNull()
			},
			summary: "Missing source endpoint",
			detail:  "source.endpoint is required",
		},
		{
			name: "endpoint with a path",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Endpoint = types.StringValue("https://legacy.example.com:9000/prefix")
			},
			summary: "Invalid source endpoint",
			detail:  "must not have a path",
		},
		{
			name: "credentials on a native provider",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Provider = types.StringValue("azure")
				model.Source.Azure = &odmAzureModel{
					Account:    types.StringValue("account"),
					AccountKey: types.StringValue("a2V5"),
					SASToken:   types.StringNull(),
				}
			},
			summary: "Invalid source credentials",
			detail:  "must be omitted for provider azure",
		},
		{
			name: "azure block on an s3 provider",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Azure = &odmAzureModel{Account: types.StringValue("account")}
			},
			summary: "Invalid source block",
			detail:  "only valid when source.provider is \"azure\"",
		},
		{
			name: "azure block missing",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Provider = types.StringValue("azure")
				model.Source.Credentials = nil
			},
			summary: "Missing source block",
			detail:  "source.azure is required",
		},
		{
			name: "azure needs exactly one credential",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Provider = types.StringValue("azure")
				model.Source.Credentials = nil
				model.Source.Azure = &odmAzureModel{
					Account:    types.StringValue("account"),
					AccountKey: types.StringValue("a2V5"),
					SASToken:   types.StringValue("sv=1"),
				}
			},
			summary: "Invalid Azure credentials",
			detail:  "Exactly one of source.azure.account_key and source.azure.sas_token",
		},
		{
			name: "gcs service account is not JSON",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Provider = types.StringValue("gcs_native")
				model.Source.Credentials = nil
				model.Source.Endpoint = types.StringNull()
				model.Source.Region = types.StringValue("auto")
				model.Source.GCS = &odmGCSModel{ServiceAccountJSON: types.StringValue("not json")}
			},
			summary: "Invalid service-account JSON",
			detail:  "not valid JSON",
		},
		{
			name: "auto region on aws",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Provider = types.StringValue("aws")
				model.Source.Endpoint = types.StringNull()
				model.Source.Region = types.StringValue("auto")
			},
			summary: "Invalid source region",
			detail:  "needs a real region",
		},
		{
			name: "credentials missing on an s3 provider",
			mutate: func(model *bucketOnDemandMigrationResourceModel) {
				model.Source.Credentials = nil
			},
			summary: "Missing source credentials",
			detail:  "does not support anonymous sources",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := odmValidPlan()
			tc.mutate(model)
			resp := harness.validateConfig(model)
			if tc.summary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.summary, tc.detail)
		})
	}
}

func TestBucketOnDemandMigrationResourceCreate(t *testing.T) {
	var puts int
	client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Query().Get("dry-run") == "true":
			puts++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":true,"config":`+odmConfigJSON(true)+`,"updated_at":null,"probe":{"reachable":true,"listable":true,"sample_key":null}}`)
		case r.Method == http.MethodPut:
			puts++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":false,"config":`+odmConfigJSON(true)+`,"updated_at":"2026-10-20T10:00:00Z","probe":{"reachable":true,"listable":true,"sample_key":null}}`)
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"bucket":"photos","config":`+odmConfigJSON(true)+`,"updated_at":"2026-10-20T10:00:00Z"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)

	plan := odmValidPlan()
	resp := h.create(plan)
	requireNoDiagnostics(t, resp.Diagnostics)
	if puts != 2 {
		t.Errorf("PUTs = %d, want a dry-run followed by the save", puts)
	}

	var state bucketOnDemandMigrationResourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
	assertStateString(t, resp.State, path.Root("id"), "photos")
	assertStateString(t, resp.State, path.Root("updated_at"), "2026-10-20T10:00:00Z")
	if state.Source == nil || state.Source.Credentials == nil {
		t.Fatal("source credentials were dropped from state")
	}
	if got := state.Source.Credentials.SecretKey.ValueString(); got != "source-secret" {
		t.Errorf("secret_key = %q, want the configured value despite the redacted read", got)
	}
	if state.Policy == nil || state.Policy.InlineMaxBytes.ValueInt64() != 16777216 {
		t.Errorf("policy = %+v, want the server defaults", state.Policy)
	}
	if state.Enabled.ValueBool() != true {
		t.Error("enabled = false, want true")
	}
}

func TestBucketOnDemandMigrationResourceCreateDryRunFailure(t *testing.T) {
	var puts int
	client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		puts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, s3ErrorXML("OnDemandMigrationSourceUnreachable", "source bucket probe failed: access_denied"))
	}))
	h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)

	plan := odmValidPlan()
	resp := h.create(plan)

	assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket on-demand migration", "access_denied")
	assertDiagnosticDetail(t, resp.Diagnostics, "nothing was saved")
	if puts != 1 {
		t.Errorf("PUTs = %d: a failed dry run must not be followed by a save", puts)
	}
	h.assertNoPartialState(resp, plan)
}

func TestBucketOnDemandMigrationResourceCreateClassifiedFailures(t *testing.T) {
	tests := []struct {
		code   string
		status int
		detail []string
	}{
		{
			code:   "OnDemandMigrationDisabled",
			status: http.StatusBadRequest,
			detail: []string{"RUSTFS_ON_DEMAND_MIGRATION_ENABLED", "disabled"},
		},
		{
			code:   "AccessDenied",
			status: http.StatusForbidden,
			detail: []string{"license", "admin:SetBucketOnDemandMigration"},
		},
		{
			code:   "OnDemandMigrationBackendNotCompiled",
			status: http.StatusNotImplemented,
			detail: []string{"gcs feature", "built without"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			client := cannedClient(t, tc.status, s3ErrorXML(tc.code, "server detail"))
			h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)
			resp := h.create(odmValidPlan())

			assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket on-demand migration", tc.detail[0])
			for _, want := range tc.detail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			h.assertNoPartialState(resp, odmValidPlan())
		})
	}
}

func TestBucketOnDemandMigrationResourceRead(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "configured bucket is refreshed",
			status: http.StatusOK,
			body:   `{"bucket":"photos","config":` + odmConfigJSON(true) + `,"updated_at":"2026-10-20T10:00:00Z"}`,
		},
		{
			name:        "unset configuration is removed from state",
			status:      http.StatusNotFound,
			body:        s3ErrorXML("NoSuchConfiguration", "not configured"),
			wantRemoved: true,
		},
		{
			name:        "deleted bucket is removed from state",
			status:      http.StatusNotFound,
			body:        s3ErrorXML("NoSuchBucket", "gone"),
			wantRemoved: true,
		},
		{
			name:        "permission failure keeps state",
			status:      http.StatusForbidden,
			body:        s3ErrorXML("AccessDenied", "denied"),
			wantSummary: "Error reading bucket on-demand migration",
			wantDetail:  "license",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := cannedClient(t, tc.status, tc.body)
			h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)
			resp := h.read(odmStateFromPlan(odmValidPlan(), true))

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				var state bucketOnDemandMigrationResourceModel
				requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
				if state.Source.Credentials.SecretKey.ValueString() != "source-secret" {
					t.Error("the configured secret was replaced by the redacted read")
				}
				assertStateString(t, resp.State, path.Root("updated_at"), "2026-10-20T10:00:00Z")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}
}

// Import supplies only the bucket, and the server never returns the source
// secret, so the first refresh after import must leave it unset instead of
// failing or writing a placeholder.
func TestBucketOnDemandMigrationResourceReadAfterImport(t *testing.T) {
	client := cannedClient(t, http.StatusOK,
		`{"bucket":"photos","config":`+odmConfigJSON(true)+`,"updated_at":"2026-10-20T10:00:00Z"}`)
	h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)

	resp := h.read(&bucketOnDemandMigrationResourceModel{
		ID:     types.StringNull(),
		Bucket: types.StringValue("photos"),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)

	var state bucketOnDemandMigrationResourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
	if state.Source == nil || state.Source.Credentials == nil {
		t.Fatal("read did not populate the source credentials")
	}
	if got := state.Source.Credentials.AccessKey.ValueString(); got != "source-access" {
		t.Errorf("access_key = %q, want source-access", got)
	}
	if !state.Source.Credentials.SecretKey.IsNull() {
		t.Errorf("secret_key = %q, want null: the server redacts it and import supplied none",
			state.Source.Credentials.SecretKey.ValueString())
	}
	assertStateString(t, resp.State, path.Root("id"), "photos")
}

func TestBucketOnDemandMigrationResourceUpdate(t *testing.T) {
	client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":false,"config":`+odmConfigJSON(false)+`,"updated_at":"2026-10-20T11:00:00Z","probe":{"reachable":true,"listable":true,"sample_key":null}}`)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"bucket":"photos","config":`+odmConfigJSON(false)+`,"updated_at":"2026-10-20T11:00:00Z"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)

	previous := odmStateFromPlan(odmValidPlan(), true)
	plan := odmStateFromPlan(odmValidPlan(), false)
	plan.Enabled = types.BoolValue(false)
	resp := h.update(plan, previous)
	requireNoDiagnostics(t, resp.Diagnostics)

	var state bucketOnDemandMigrationResourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
	if state.Enabled.ValueBool() {
		t.Error("enabled = true after disabling the source")
	}
	if state.Source.Credentials.SecretKey.ValueString() != "source-secret" {
		t.Error("credentials were lost by disabling the source")
	}
	assertStateString(t, resp.State, path.Root("updated_at"), "2026-10-20T11:00:00Z")
}

func TestBucketOnDemandMigrationResourceDelete(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantSummary string
		wantDetail  string
	}{
		{name: "idempotent delete", status: http.StatusNoContent},
		{name: "already deleted bucket", status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
		{
			name:        "module disabled",
			status:      http.StatusBadRequest,
			body:        s3ErrorXML("OnDemandMigrationDisabled", "disabled"),
			wantSummary: "Error deleting bucket on-demand migration",
			wantDetail:  "RUSTFS_ON_DEMAND_MIGRATION_ENABLED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := cannedClient(t, tc.status, tc.body)
			h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)
			resp := h.delete(odmStateFromPlan(odmValidPlan(), true))

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}
}

// odmLifecycleClient answers one successful create/update sequence: the
// dry-run probe, the save, and the read-back. puts counts the PUTs so a case
// can assert the save follows a successful dry run.
func odmLifecycleClient(t *testing.T, puts *int, enabled bool) *AllClient {
	t.Helper()
	return httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Query().Get("dry-run") == "true":
			*puts++
			_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":true,"config":`+odmConfigJSON(enabled)+
				`,"updated_at":null,"probe":{"reachable":true,"listable":true,"sample_key":null}}`)
		case r.Method == http.MethodPut:
			*puts++
			_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":false,"config":`+odmConfigJSON(enabled)+
				`,"updated_at":"2026-10-20T10:00:00Z","probe":{"reachable":true,"listable":true,"sample_key":null}}`)
		case r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"bucket":"photos","config":`+odmConfigJSON(enabled)+`,"updated_at":"2026-10-20T10:00:00Z"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

// TestBucketOnDemandMigrationResourceLifecyclePaths pins the cross-method
// contract in one place: the dry run gates the save, a failed apply writes no
// state, a vanished configuration is removed from state, a missing route is a
// capability gap that keeps state, and destroy stays idempotent while surfacing
// a disabled module.
func TestBucketOnDemandMigrationResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("configure rejects unexpected provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), cannedClient(t, http.StatusOK, "{}"))
		resp := h.configure("not-a-client")
		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient")
	})

	t.Run("create: the dry run gates the save and the read-back fills the state", func(t *testing.T) {
		var puts int
		h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), odmLifecycleClient(t, &puts, true))

		plan := odmValidPlan()
		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		if puts != 2 {
			t.Errorf("PUTs = %d, want a dry run followed by the save", puts)
		}
		assertStateString(t, resp.State, path.Root("id"), "photos")
		assertStateString(t, resp.State, path.Root("updated_at"), "2026-10-20T10:00:00Z")
		var state bucketOnDemandMigrationResourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
		if state.Version.ValueInt64() != 1 {
			t.Errorf("version = %d, want 1", state.Version.ValueInt64())
		}
	})

	createCases := []struct {
		name   string
		status int
		body   string
		detail []string
	}{
		{
			name:   "a disabled module names the environment switch",
			status: http.StatusBadRequest,
			body:   s3ErrorXML("OnDemandMigrationDisabled", "disabled"),
			detail: []string{"RUSTFS_ON_DEMAND_MIGRATION_ENABLED", "nothing was saved"},
		},
		{
			name:   "an unreachable source names the probe call",
			status: http.StatusBadRequest,
			body:   s3ErrorXML("OnDemandMigrationSourceUnreachable", "source bucket probe failed: access_denied"),
			detail: []string{"source bucket probe failed (access_denied)", "source.endpoint", "nothing was saved"},
		},
		{
			name:   "a licence or permission refusal names the admin action",
			status: http.StatusForbidden,
			body:   s3ErrorXML("AccessDenied", "Access Denied."),
			detail: []string{"license", "admin:SetBucketOnDemandMigration", "nothing was saved"},
		},
		{
			name:   "a backend that is not compiled names the build feature",
			status: http.StatusNotImplemented,
			body:   s3ErrorXML("OnDemandMigrationBackendNotCompiled", "gcs support is not compiled in"),
			detail: []string{"gcs feature", "nothing was saved"},
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), cannedClient(t, tc.status, tc.body))
			plan := odmValidPlan()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket on-demand migration", tc.detail[0])
			for _, want := range tc.detail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: a failed save after the dry run writes no state", func(t *testing.T) {
		var puts int
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPut && r.URL.Query().Get("dry-run") == "true":
				puts++
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":true,"config":`+odmConfigJSON(true)+
					`,"updated_at":null,"probe":{"reachable":true,"listable":true,"sample_key":null}}`)
			case r.Method == http.MethodPut:
				puts++
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, s3ErrorXML("InternalError", "boom"))
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)
		plan := odmValidPlan()

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket on-demand migration",
			"Could not create on-demand migration for bucket photos")
		if puts != 2 {
			t.Errorf("PUTs = %d, want the dry run and the failed save", puts)
		}
		h.assertNoPartialState(resp, plan)
	})

	t.Run("create: a failed read-back says the configuration was saved", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, s3ErrorXML("InternalError", "boom"))
				return
			}
			_, _ = fmt.Fprint(w, `{"bucket":"photos","dry_run":false,"config":`+odmConfigJSON(true)+
				`,"updated_at":"2026-10-20T10:00:00Z","probe":{"reachable":true,"listable":true,"sample_key":null}}`)
		}))
		h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)
		plan := odmValidPlan()

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating bucket on-demand migration", "reading it back failed")
		h.assertNoPartialState(resp, plan)
	})

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  []string
	}{
		{
			name: "a configured bucket is refreshed",
			routes: []requestRoute{
				{method: http.MethodGet, status: http.StatusOK,
					body: `{"bucket":"photos","config":` + odmConfigJSON(true) + `,"updated_at":"2026-10-20T10:00:00Z"}`},
			},
		},
		{
			name: "an unset configuration is removed from state",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: s3ErrorXML("NoSuchConfiguration", "not configured")},
			},
			wantRemoved: true,
		},
		{
			name: "a deleted bucket is removed from state",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
			},
			wantRemoved: true,
		},
		{
			name: "a permission failure keeps state",
			routes: []requestRoute{
				{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "denied")},
			},
			wantSummary: "Error reading bucket on-demand migration",
			wantDetail:  []string{"license", "admin:GetBucketOnDemandMigration"},
		},
		{
			// A bare 404 is the route missing, not the configuration: dropping
			// the record would forget a configuration the server still holds.
			name: "a missing route keeps state and names the route",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"1.0.3"}`},
				{status: http.StatusNotFound, body: "Not Found"},
			},
			wantSummary: "Error reading bucket on-demand migration",
			wantDetail:  []string{"RustFS 1.0.3 does not implement", "PUT|GET|DELETE /rustfs/admin/v3/on-demand-migration/{bucket}"},
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)

			resp := h.read(odmStateFromPlan(odmValidPlan(), true))

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				var state bucketOnDemandMigrationResourceModel
				requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
				if state.Source == nil || state.Source.Credentials == nil {
					t.Fatal("read dropped the source credentials")
				}
				if got := state.Source.Credentials.SecretKey.ValueString(); got != "source-secret" {
					t.Errorf("secret_key = %q, want the configured value despite the redacted read", got)
				}
				assertStateString(t, resp.State, path.Root("updated_at"), "2026-10-20T10:00:00Z")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail[0])
			for _, want := range tc.wantDetail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			assertStatePresent(t, resp.State)
		})
	}

	t.Run("update: disabling the source keeps the credentials and updates the stamp", func(t *testing.T) {
		var puts int
		h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), odmLifecycleClient(t, &puts, false))
		plan := odmStateFromPlan(odmValidPlan(), false)
		plan.Enabled = types.BoolValue(false)

		resp := h.update(plan, odmStateFromPlan(odmValidPlan(), true))

		requireNoDiagnostics(t, resp.Diagnostics)
		if puts != 2 {
			t.Errorf("PUTs = %d, want the dry run and the save", puts)
		}
		var state bucketOnDemandMigrationResourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
		if state.Enabled.ValueBool() {
			t.Error("enabled = true after disabling the source")
		}
		if state.Source == nil || state.Source.Credentials == nil ||
			state.Source.Credentials.SecretKey.ValueString() != "source-secret" {
			t.Error("the configured secret was lost by disabling the source")
		}
		assertStateString(t, resp.State, path.Root("updated_at"), "2026-10-20T10:00:00Z")
	})

	t.Run("update: a failed dry run reports the update and writes no state", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), cannedClient(t, http.StatusBadRequest,
			s3ErrorXML("OnDemandMigrationSourceUnreachable", "source bucket probe failed: access_denied")))
		previous := odmStateFromPlan(odmValidPlan(), true)
		plan := odmStateFromPlan(odmValidPlan(), false)

		resp := h.update(plan, previous)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket on-demand migration", "nothing was saved")
		var state bucketOnDemandMigrationResourceModel
		requireNoDiagnostics(t, resp.State.Get(context.Background(), &state))
		if !state.Enabled.ValueBool() {
			t.Error("a failed update disabled the source in state")
		}
	})

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  []string
	}{
		{
			name: "an existing configuration is deleted",
			routes: []requestRoute{
				{method: http.MethodDelete, status: http.StatusNoContent},
			},
		},
		{
			name: "an already-deleted bucket needs no error",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
			},
		},
		{
			name: "an already-cleared configuration needs no error",
			routes: []requestRoute{
				{status: http.StatusNotFound, body: s3ErrorXML("NoSuchConfiguration", "not configured")},
			},
		},
		{
			name: "a disabled module is surfaced, not swallowed",
			routes: []requestRoute{
				{status: http.StatusBadRequest, body: s3ErrorXML("OnDemandMigrationDisabled", "disabled")},
			},
			wantSummary: "Error deleting bucket on-demand migration",
			wantDetail:  []string{"RUSTFS_ON_DEMAND_MIGRATION_ENABLED"},
		},
		{
			name: "a missing route is surfaced, not treated as cleared",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"1.0.3"}`},
				{status: http.StatusNotFound, body: "Not Found"},
			},
			wantSummary: "Error deleting bucket on-demand migration",
			wantDetail:  []string{"RustFS 1.0.3 does not implement", "on-demand-migration/{bucket}"},
		},
	}

	for _, tc := range deleteCases {
		t.Run("destroy: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketOnDemandMigrationResource(), client)

			resp := h.delete(odmStateFromPlan(odmValidPlan(), true))

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail[0])
			for _, want := range tc.wantDetail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
		})
	}
}

func TestBucketOnDemandMigrationResourceImportState(t *testing.T) {
	r := NewBucketOnDemandMigrationResource()
	importer, ok := r.(resource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource must implement ResourceWithImportState")
	}

	resp := &resource.ImportStateResponse{State: resourceState(t, resourceSchemaOf(t, r), &bucketOnDemandMigrationResourceModel{
		Bucket: types.StringNull(),
	})}
	importer.ImportState(context.Background(), resource.ImportStateRequest{ID: "photos"}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("bucket"), "photos")
}
