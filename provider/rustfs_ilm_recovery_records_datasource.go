// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ILMRecoveryRecordsDataSource{}

type ILMRecoveryRecordsDataSource struct {
	client *AllClient
}

type ILMRecoveryRecordsDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	Protocol       types.String `tfsdk:"protocol"`
	Classification types.String `tfsdk:"classification"`
	Limit          types.Int64  `tfsdk:"limit"`
	Marker         types.String `tfsdk:"marker"`
	Records        types.List   `tfsdk:"records"`
	NextMarker     types.String `tfsdk:"next_marker"`
	Truncated      types.Bool   `tfsdk:"truncated"`
	Incomplete     types.Bool   `tfsdk:"incomplete"`
	JSON           types.String `tfsdk:"json"`
}

var ilmRecoveryRecordAttrTypes = map[string]attr.Type{
	"control_id":                  types.StringType,
	"protocol":                    types.StringType,
	"classification":              types.StringType,
	"schema":                      types.StringType,
	"revision":                    types.Int64Type,
	"attempt_count":               types.Int64Type,
	"consecutive_failure_count":   types.Int64Type,
	"first_seen_at_unix_nanos":    types.Int64Type,
	"first_failure_at_unix_nanos": types.Int64Type,
	"last_failure_at_unix_nanos":  types.Int64Type,
	"next_attempt_at_unix_nanos":  types.Int64Type,
	"last_error_code":             types.StringType,
	"source_schema":               types.StringType,
	"source_generation_sha256":    types.StringType,
	"copy_set_sha256":             types.StringType,
	"source_copy_count":           types.Int64Type,
}

func NewILMRecoveryRecordsDataSource() datasource.DataSource {
	return &ILMRecoveryRecordsDataSource{}
}

func (d *ILMRecoveryRecordsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ilm_recovery_records"
}

func (d *ILMRecoveryRecordsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Reads the durable ILM recovery controls the server keeps for interrupted transitions and tier " +
		"deletions. Recovery controls are the server's own repair queue, not Terraform state: this data source is " +
		"read-only and never retries, abandons or exports a record."
	resp.Schema = schema.Schema{
		Description:         "Read ILM recovery records",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"protocol": schema.StringAttribute{
				Required: true,
				Description: "Recovery protocol to list. The server requires it: `transition_transaction`, " +
					"`tier_delete_journal` or `tier_delete_manifest`.",
				Validators: []validator.String{
					stringvalidator.OneOf("transition_transaction", "tier_delete_journal", "tier_delete_manifest"),
				},
			},
			"classification": schema.StringAttribute{
				Optional: true,
				Description: "Only return records with this classification: `retrying`, `retained_ambiguous`, " +
					"`corrupt`, `operator_required`, `abandoned` or `terminal`.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						"retrying",
						"retained_ambiguous",
						"corrupt",
						"operator_required",
						"abandoned",
						"terminal",
					),
				},
			},
			"limit": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Maximum records to return in this page. The server defaults to 100 and accepts 1..1000.",
				Validators: []validator.Int64{
					int64validator.Between(1, 1000),
				},
			},
			"marker": schema.StringAttribute{
				Optional: true,
				Description: "Pagination marker returned as `next_marker` by a previous read. Leave unset for the " +
					"first page.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, derived from the query.",
			},
			"records": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Recovery controls in this page.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"control_id": schema.StringAttribute{
							Computed:    true,
							Description: "Stable identifier of the recovery control.",
						},
						"protocol": schema.StringAttribute{
							Computed:    true,
							Description: "Recovery protocol that owns the control.",
						},
						"classification": schema.StringAttribute{
							Computed:    true,
							Description: "Server classification of the control.",
						},
						"schema": schema.StringAttribute{
							Computed:    true,
							Description: "Control schema the record was written with.",
						},
						"revision": schema.Int64Attribute{
							Computed:    true,
							Description: "Control revision.",
						},
						"attempt_count": schema.Int64Attribute{
							Computed:    true,
							Description: "Recovery attempts so far.",
						},
						"consecutive_failure_count": schema.Int64Attribute{
							Computed:    true,
							Description: "Consecutive failed attempts.",
						},
						"first_seen_at_unix_nanos": schema.Int64Attribute{
							Computed:    true,
							Description: "When the control was first observed, in Unix nanoseconds.",
						},
						"first_failure_at_unix_nanos": schema.Int64Attribute{
							Computed:    true,
							Description: "First failure time in Unix nanoseconds; null when it never failed.",
						},
						"last_failure_at_unix_nanos": schema.Int64Attribute{
							Computed:    true,
							Description: "Most recent failure time in Unix nanoseconds; null when it never failed.",
						},
						"next_attempt_at_unix_nanos": schema.Int64Attribute{
							Computed:    true,
							Description: "Scheduled next attempt in Unix nanoseconds; null when none is scheduled.",
						},
						"last_error_code": schema.StringAttribute{
							Computed:    true,
							Description: "Last error code the server recorded.",
						},
						"source_schema": schema.StringAttribute{
							Computed:    true,
							Description: "Schema of the observed source generation.",
						},
						"source_generation_sha256": schema.StringAttribute{
							Computed:    true,
							Description: "SHA-256 of the observed source generation.",
						},
						"copy_set_sha256": schema.StringAttribute{
							Computed:    true,
							Description: "SHA-256 of the observed copy set.",
						},
						"source_copy_count": schema.Int64Attribute{
							Computed:    true,
							Description: "Number of source copies observed.",
						},
					},
				},
			},
			"next_marker": schema.StringAttribute{
				Computed:    true,
				Description: "Marker for the next page; null when this page is the last one.",
			},
			"truncated": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether more records match than this page returned.",
			},
			"incomplete": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the fleet could not be inventoried in full, so the page may be incomplete.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON response as returned by the RustFS admin API.",
			},
		},
	}
}

func (d *ILMRecoveryRecordsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *ILMRecoveryRecordsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ILMRecoveryRecordsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	protocol := config.Protocol.ValueString()
	if protocol == "" {
		resp.Diagnostics.AddError(
			"Error reading ILM recovery records",
			"protocol is required: RustFS only lists recovery records for one protocol at a time.",
		)
		return
	}

	limit := config.Limit.ValueInt64()
	if limit == 0 {
		// The server's default page size. Sending it explicitly keeps the
		// attribute known after every read.
		limit = 100
	}
	page, err := d.client.RustClient.ListILMRecoveryRecords(ctx, rustfs.ILMRecoveryRecordQuery{
		Protocol:       protocol,
		Classification: config.Classification.ValueString(),
		Limit:          limit,
		Marker:         config.Marker.ValueString(),
	})
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"ILM recovery records are not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
					"rustfs_ilm_recovery_records data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading ILM recovery records",
			"Could not list recovery records for protocol "+protocol+": "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue("recovery-records/" + protocol)
	config.Limit = types.Int64Value(limit)
	config.Records = ilmRecoveryRecordsValue(page.Records)
	config.NextMarker = nullableString(page.NextMarker)
	config.Truncated = types.BoolValue(page.Truncated)
	config.Incomplete = types.BoolValue(page.Incomplete)
	if len(page.Raw) > 0 {
		config.JSON = types.StringValue(string(page.Raw))
	} else {
		config.JSON = types.StringValue("{}")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func ilmRecoveryRecordsValue(records []rustfs.ILMRecoveryRecord) types.List {
	values := make([]attr.Value, 0, len(records))
	for _, record := range records {
		values = append(values, types.ObjectValueMust(ilmRecoveryRecordAttrTypes, map[string]attr.Value{
			"control_id":                  types.StringValue(record.ControlID),
			"protocol":                    types.StringValue(record.Protocol),
			"classification":              types.StringValue(record.Classification),
			"schema":                      types.StringValue(record.Schema),
			"revision":                    types.Int64Value(record.Revision),
			"attempt_count":               types.Int64Value(record.AttemptCount),
			"consecutive_failure_count":   types.Int64Value(record.ConsecutiveFailureCount),
			"first_seen_at_unix_nanos":    types.Int64Value(record.FirstSeenAtUnixNanos),
			"first_failure_at_unix_nanos": nullableInt64(record.FirstFailureAtUnixNanos),
			"last_failure_at_unix_nanos":  nullableInt64(record.LastFailureAtUnixNanos),
			"next_attempt_at_unix_nanos":  nullableInt64(record.NextAttemptAtUnixNanos),
			"last_error_code":             types.StringValue(record.LastErrorCode),
			"source_schema":               types.StringValue(record.SourceSchema),
			"source_generation_sha256":    types.StringValue(record.SourceGenerationSHA256),
			"copy_set_sha256":             types.StringValue(record.CopySetSHA256),
			"source_copy_count":           types.Int64Value(record.SourceCopyCount),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: ilmRecoveryRecordAttrTypes}, values)
}

func nullableInt64(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

func init() {
	registerDataSource(NewILMRecoveryRecordsDataSource)
}
