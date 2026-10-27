// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &QuotaCheckDataSource{}

// QuotaCheckDataSource asks the quota subsystem whether an operation would be
// admitted for a bucket (POST /rustfs/admin/v3/quota-check/{bucket}).
type QuotaCheckDataSource struct {
	client *AllClient
}

type QuotaCheckDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	Bucket         types.String `tfsdk:"bucket"`
	OperationType  types.String `tfsdk:"operation_type"`
	OperationSize  types.Int64  `tfsdk:"operation_size"`
	Allowed        types.Bool   `tfsdk:"allowed"`
	CurrentUsage   types.Int64  `tfsdk:"current_usage"`
	QuotaLimit     types.Int64  `tfsdk:"quota_limit"`
	RemainingQuota types.Int64  `tfsdk:"remaining_quota"`
}

// Defaults for the optional inputs. They are applied in Read because the
// framework's data source schema has no default support: an unset optional
// attribute arrives as unknown or null.
const (
	defaultQuotaCheckOperationType = "PUT"
	defaultQuotaCheckOperationSize = int64(0)
)

func NewQuotaCheckDataSource() datasource.DataSource {
	return &QuotaCheckDataSource{}
}

func (d *QuotaCheckDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_quota_check"
}

func (d *QuotaCheckDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Evaluate whether an operation would be admitted against a bucket's quota " +
		"(POST /rustfs/admin/v3/quota-check/{bucket}). The check is read-only: RustFS computes the bucket's " +
		"current usage and the admission decision without reserving bytes or changing quota, objects or usage. " +
		"`usage_percentage` is not reported here; use `rustfs_quota_stats` for the percentage and the limit " +
		"breakdown. A field the server cannot report is null, never zero."
	resp.Schema = schema.Schema{
		Description:         "Check whether an operation fits within a bucket's quota",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, `bucket/operation_type/operation_size`.",
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket to evaluate the operation against.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"operation_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Operation to evaluate: `PUT`, `POST`, `COPY` or `DELETE`. Defaults to `PUT`. " +
					"RustFS treats an unrecognized value as `PUT`; the provider rejects values outside the list " +
					"before sending the request.",
				Validators: []validator.String{
					stringvalidator.OneOf("PUT", "POST", "COPY", "DELETE"),
				},
			},
			"operation_size": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "Operation size in bytes. Defaults to 0, which asks whether any operation of that " +
					"kind would be admitted. The check never changes usage.",
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			"allowed": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the server would admit the operation against the bucket's current usage and quota.",
			},
			"current_usage": schema.Int64Attribute{
				Computed: true,
				Description: "Usage in bytes the decision was based on. Null when the server could not report a " +
					"usage figure, never silently zero.",
			},
			"quota_limit": schema.Int64Attribute{
				Computed:    true,
				Description: "Configured hard quota in bytes. Null when the bucket has no quota configured.",
			},
			"remaining_quota": schema.Int64Attribute{
				Computed:    true,
				Description: "Bytes still allowed before the quota is reached. Null when the server could not report it.",
			},
		},
	}
}

func (d *QuotaCheckDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *QuotaCheckDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config QuotaCheckDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := config.Bucket.ValueString()

	operationType := defaultQuotaCheckOperationType
	if !config.OperationType.IsNull() && !config.OperationType.IsUnknown() {
		operationType = config.OperationType.ValueString()
	}

	operationSize := defaultQuotaCheckOperationSize
	if !config.OperationSize.IsNull() && !config.OperationSize.IsUnknown() {
		operationSize = config.OperationSize.ValueInt64()
	}

	check, err := d.client.RustClient.CheckQuota(ctx, bucket, operationType, operationSize)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error checking bucket quota",
			"Could not check the quota for bucket "+bucket+": "+err.Error(),
		)
		return
	}

	config.ID = types.StringValue(bucket + "/" + operationType + "/" + strconv.FormatInt(operationSize, 10))
	config.OperationType = types.StringValue(operationType)
	config.OperationSize = types.Int64Value(operationSize)
	config.Allowed = types.BoolValue(check.Allowed)
	config.CurrentUsage = uint64NullInt64(check.CurrentUsage)
	config.QuotaLimit = uint64NullInt64(check.QuotaLimit)
	config.RemainingQuota = uint64NullInt64(check.RemainingQuota)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func init() {
	registerDataSource(NewQuotaCheckDataSource)
}
