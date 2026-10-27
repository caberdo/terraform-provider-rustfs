// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &RuntimeCapabilitiesDataSource{}

var runtimeStorageClassAttrTypes = map[string]attr.Type{
	"contract_version":        types.Int64Type,
	"supported_write_classes": types.ListType{ElemType: types.StringType},
	"unsupported_write_error": types.StringType,
	"legacy_label_behavior":   types.StringType,
}

type RuntimeCapabilitiesDataSource struct {
	client *AllClient
}

type RuntimeCapabilitiesDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	JSON           types.String `tfsdk:"json"`
	StorageClasses types.Object `tfsdk:"storage_classes"`
}

type runtimeStorageClassModel struct {
	ContractVersion       types.Int64  `tfsdk:"contract_version"`
	SupportedWriteClasses types.List   `tfsdk:"supported_write_classes"`
	UnsupportedWriteError types.String `tfsdk:"unsupported_write_error"`
	LegacyLabelBehavior   types.String `tfsdk:"legacy_label_behavior"`
}

func NewRuntimeCapabilitiesDataSource() datasource.DataSource {
	return &RuntimeCapabilitiesDataSource{}
}

func (d *RuntimeCapabilitiesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_runtime_capabilities"
}

func (d *RuntimeCapabilitiesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Runtime capabilities advertised by the RustFS v4 admin API " +
		"(GET /rustfs/admin/v4/runtime/capabilities). `storage_classes` is the versioned storage-class " +
		"write contract: check `contract_version` before gating writes on `supported_write_classes`. " +
		"The v4 admin API is a preview surface in RustFS 1.0.3; `json` keeps the full payload so a " +
		"newer server can add fields without a provider release. A server that predates the v4 namespace " +
		"answers `511 Upgrade Required`, which Terraform reports as a negotiation diagnostic naming the " +
		"route and the server version."
	resp.Schema = schema.Schema{
		Description:         description,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON response as returned by the RustFS admin API.",
			},
			"storage_classes": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Versioned storage-class write contract.",
				Attributes: map[string]schema.Attribute{
					"contract_version": schema.Int64Attribute{
						Computed:    true,
						Description: "Version of the storage-class capability contract.",
					},
					"supported_write_classes": schema.ListAttribute{
						Computed:    true,
						ElementType: types.StringType,
						Description: "Storage classes the server accepts on object writes and copies.",
					},
					"unsupported_write_error": schema.StringAttribute{
						Computed:    true,
						Description: "Error code the server returns for an unsupported storage class.",
					},
					"legacy_label_behavior": schema.StringAttribute{
						Computed:    true,
						Description: "How the server treats a legacy storage-class label.",
					},
				},
			},
		},
	}
}

func (d *RuntimeCapabilitiesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *RuntimeCapabilitiesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	capabilities, err := d.client.RustClient.GetRuntimeCapabilities(ctx)
	if err != nil {
		var unsupported *rustfs.UnsupportedFeatureError
		if errors.As(err, &unsupported) {
			resp.Diagnostics.AddError(
				"Runtime capabilities are not available on this RustFS server",
				unsupported.Error()+". Upgrade RustFS to a release that serves the v4 admin API, or remove the "+
					"rustfs_runtime_capabilities data source from the configuration.",
			)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading runtime capabilities",
			"Could not read runtime capabilities, unexpected error: "+err.Error(),
		)
		return
	}

	writeClasses, diags := types.ListValueFrom(ctx, types.StringType, capabilities.StorageClasses.SupportedWriteClasses)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	storageClasses, diags := types.ObjectValueFrom(ctx, runtimeStorageClassAttrTypes, runtimeStorageClassModel{
		ContractVersion:       types.Int64Value(int64(capabilities.StorageClasses.ContractVersion)),
		SupportedWriteClasses: writeClasses,
		UnsupportedWriteError: types.StringValue(capabilities.StorageClasses.UnsupportedWriteError),
		LegacyLabelBehavior:   types.StringValue(capabilities.StorageClasses.LegacyLabelBehavior),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := RuntimeCapabilitiesDataSourceModel{
		ID:             types.StringValue("runtime-capabilities"),
		JSON:           types.StringValue(capabilities.RawJSON),
		StorageClasses: storageClasses,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func init() {
	registerDataSource(NewRuntimeCapabilitiesDataSource)
}
