// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const quotaCheckTestPayload = `{
	"bucket": "my-bucket",
	"operation_type": "PUT",
	"operation_size": 1024,
	"allowed": true,
	"current_usage": 2048,
	"quota_limit": 4096,
	"remaining_quota": 1024
}`

func quotaCheckConfig(bucket, operationType string, operationSize *int64) *QuotaCheckDataSourceModel {
	model := &QuotaCheckDataSourceModel{
		ID:             types.StringNull(),
		Bucket:         types.StringValue(bucket),
		OperationType:  types.StringNull(),
		OperationSize:  types.Int64Null(),
		Allowed:        types.BoolNull(),
		CurrentUsage:   types.Int64Null(),
		QuotaLimit:     types.Int64Null(),
		RemainingQuota: types.Int64Null(),
	}
	if operationType != "" {
		model.OperationType = types.StringValue(operationType)
	}
	if operationSize != nil {
		model.OperationSize = types.Int64Value(*operationSize)
	}
	return model
}

func readQuotaCheck(t *testing.T, d *QuotaCheckDataSource, sch datasourceschema.Schema, model *QuotaCheckDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestQuotaCheckDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewQuotaCheckDataSource())

	for _, name := range []string{"id", "allowed", "current_usage", "quota_limit", "remaining_quota"} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("attribute %q must be Computed only", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}

	bucket, ok := sch.Attributes["bucket"].(datasourceschema.StringAttribute)
	if !ok || !bucket.Required || bucket.Optional || bucket.Computed {
		t.Errorf("bucket must be Required only: %+v", sch.Attributes["bucket"])
	}
	if len(bucket.Validators) == 0 {
		t.Error("bucket must reject an empty name")
	}

	operationType, ok := sch.Attributes["operation_type"].(datasourceschema.StringAttribute)
	if !ok || !operationType.Optional || !operationType.Computed {
		t.Fatalf("operation_type must be Optional and Computed: %+v", sch.Attributes["operation_type"])
	}
	if operationType.GetDescription() == "" {
		t.Error("operation_type has no description")
	}
	if len(operationType.Validators) == 0 {
		t.Error("operation_type must validate the operation list")
	}

	operationSize, ok := sch.Attributes["operation_size"].(datasourceschema.Int64Attribute)
	if !ok || !operationSize.Optional || !operationSize.Computed {
		t.Fatalf("operation_size must be Optional and Computed: %+v", sch.Attributes["operation_size"])
	}
	if len(operationSize.Validators) == 0 {
		t.Error("operation_size must reject negative sizes")
	}
}

func TestQuotaCheckDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewQuotaCheckDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_quota_check" {
		t.Errorf("expected rustfs_quota_check, got %s", resp.TypeName)
	}
}

func TestQuotaCheckDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

// requestBody reads the JSON body of a recorded admin request.
func requestBody(t *testing.T, request adminRequest) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, request.Body)
	}
	return body
}

func TestQuotaCheckDataSourceRead(t *testing.T) {
	t.Run("sends the configured operation and surfaces the decision", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(quotaCheckTestPayload))
		}))
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		size := int64(1024)
		resp := readQuotaCheck(t, d, sch, quotaCheckConfig("my-bucket", "PUT", &size))
		requireNoDiagnostics(t, resp.Diagnostics)

		assertStateString(t, resp.State, path.Root("id"), "my-bucket/PUT/1024")
		assertStateString(t, resp.State, path.Root("operation_type"), "PUT")
		assertStateInt64(t, resp.State, path.Root("operation_size"), 1024)
		assertStateBool(t, resp.State, path.Root("allowed"), true)
		assertStateInt64(t, resp.State, path.Root("current_usage"), 2048)
		assertStateInt64(t, resp.State, path.Root("quota_limit"), 4096)
		assertStateInt64(t, resp.State, path.Root("remaining_quota"), 1024)
	})

	t.Run("defaults to a PUT of zero bytes and carries both inputs", func(t *testing.T) {
		var request adminRequest
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			request = adminRequest{Method: r.Method, Path: r.URL.Path, Body: body}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bucket":"my-bucket","operation_type":"PUT","operation_size":0,` +
				`"allowed":false,"current_usage":null,"quota_limit":null,"remaining_quota":null}`))
		}))
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readQuotaCheck(t, d, sch, quotaCheckConfig("my-bucket", "", nil))
		requireNoDiagnostics(t, resp.Diagnostics)

		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.Path != "/rustfs/admin/v3/quota-check/my-bucket" {
			t.Errorf("path = %q", request.Path)
		}
		body := requestBody(t, request)
		if body["operation_type"] != "PUT" {
			t.Errorf("operation_type = %v, want PUT", body["operation_type"])
		}
		if size, ok := body["operation_size"].(float64); !ok || size != 0 {
			t.Errorf("operation_size = %v, want 0", body["operation_size"])
		}

		assertStateString(t, resp.State, path.Root("id"), "my-bucket/PUT/0")
		assertStateString(t, resp.State, path.Root("operation_type"), "PUT")
		assertStateInt64(t, resp.State, path.Root("operation_size"), 0)
		assertStateBool(t, resp.State, path.Root("allowed"), false)
		assertStateNull(t, resp.State, path.Root("current_usage"))
		assertStateNull(t, resp.State, path.Root("quota_limit"))
		assertStateNull(t, resp.State, path.Root("remaining_quota"))
	})

	t.Run("passes a COPY operation size through", func(t *testing.T) {
		var request adminRequest
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			request = adminRequest{Method: r.Method, Path: r.URL.Path, Body: body}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bucket":"my-bucket","operation_type":"COPY","operation_size":4096,` +
				`"allowed":false,"current_usage":3000,"quota_limit":4096,"remaining_quota":1096}`))
		}))
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		size := int64(4096)
		resp := readQuotaCheck(t, d, sch, quotaCheckConfig("my-bucket", "COPY", &size))
		requireNoDiagnostics(t, resp.Diagnostics)

		body := requestBody(t, request)
		if body["operation_type"] != "COPY" {
			t.Errorf("operation_type = %v, want COPY", body["operation_type"])
		}
		if got, ok := body["operation_size"].(float64); !ok || got != 4096 {
			t.Errorf("operation_size = %v, want 4096", body["operation_size"])
		}
		assertStateString(t, resp.State, path.Root("id"), "my-bucket/COPY/4096")
		assertStateBool(t, resp.State, path.Root("allowed"), false)
		assertStateInt64(t, resp.State, path.Root("remaining_quota"), 1096)
	})

	t.Run("fails for a missing bucket instead of reporting zeros", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code><Message>bucket not found: missing</Message></Error>`))
		}))
		d := NewQuotaCheckDataSource().(*QuotaCheckDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readQuotaCheck(t, d, sch, quotaCheckConfig("missing", "", nil))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error checking bucket quota")
		if !strings.Contains(diagnostic.Detail(), "bucket not found: missing") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})
}
