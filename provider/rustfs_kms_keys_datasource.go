// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &KmsKeysDataSource{}

// KmsKeysDataSource lists the KMS master keys the server reports
// (GET /rustfs/admin/v3/kms/keys). The listing is metadata only: the server
// never returns key material.
type KmsKeysDataSource struct {
	client *AllClient
}

type KmsKeysDataSourceModel struct {
	ID   types.String `tfsdk:"id"`
	Keys types.List   `tfsdk:"keys"`
}

type kmsKeyListModel struct {
	KeyID       types.String `tfsdk:"key_id"`
	Description types.String `tfsdk:"description"`
	Status      types.String `tfsdk:"status"`
	Usage       types.String `tfsdk:"usage"`
	Algorithm   types.String `tfsdk:"algorithm"`
	CreatedAt   types.String `tfsdk:"created_at"`
	CreatedBy   types.String `tfsdk:"created_by"`
	Tags        types.Map    `tfsdk:"tags"`
}

func NewKmsKeysDataSource() datasource.DataSource {
	return &KmsKeysDataSource{}
}

func (d *KmsKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_keys"
}

func (d *KmsKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List the RustFS KMS master keys",
		MarkdownDescription: "List the KMS master keys the RustFS server reports " +
			"(`GET /rustfs/admin/v3/kms/keys`), sorted by key id.\n\n" +
			"The route returns metadata only; no key material is ever returned. A key in " +
			"`pending_deletion` is already scheduled for destruction and its data becomes unreadable when " +
			"that happens.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"keys": schema.ListNestedAttribute{
				Computed:    true,
				Description: "KMS master keys sorted by key id.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key_id": schema.StringAttribute{
							Computed:    true,
							Description: "Key identifier, which is the identifier `rustfs_kms_key` manages.",
						},
						"description": schema.StringAttribute{
							Computed:    true,
							Description: "Key description. Null when the server reports none.",
						},
						"status": schema.StringAttribute{
							Computed:    true,
							Description: "Key lifecycle status the server reports, for example `Enabled`, `Disabled` or `PendingDeletion`.",
						},
						"usage": schema.StringAttribute{
							Computed:    true,
							Description: "Key usage the server reports, for example `ENCRYPT_DECRYPT`.",
						},
						"algorithm": schema.StringAttribute{
							Computed:    true,
							Description: "Key algorithm the server reports, for example `AES_256`.",
						},
						"created_at": schema.StringAttribute{
							Computed:    true,
							Description: "Creation timestamp the server reports, in RFC 3339.",
						},
						"created_by": schema.StringAttribute{
							Computed:    true,
							Description: "Principal that created the key. Null when the server reports none.",
						},
						"tags": schema.MapAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Key tags as reported by the server. Null when the server reports none.",
						},
					},
				},
			},
		},
	}
}

func (d *KmsKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *KmsKeysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config KmsKeysDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keys, err := d.client.RustClient.ListKmsKeys()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing KMS keys",
			"Could not list RustFS KMS keys: "+err.Error(),
		)
		return
	}

	slices.SortFunc(keys, func(a, b rustfs.KmsKeyInfo) int {
		return strings.Compare(a.KeyID, b.KeyID)
	})
	models, diags := kmsKeyListModels(ctx, keys)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	listed, diags := types.ListValueFrom(ctx, kmsKeyListObjectType(), models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue("kms-keys")
	config.Keys = listed
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// kmsKeyListModels converts the wire entries, keeping "the server reported
// nothing" as null instead of an empty string or an empty map.
func kmsKeyListModels(ctx context.Context, keys []rustfs.KmsKeyInfo) ([]kmsKeyListModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	models := make([]kmsKeyListModel, 0, len(keys))
	for _, key := range keys {
		tags := types.MapNull(types.StringType)
		if len(key.Tags) > 0 {
			value, tagDiags := types.MapValueFrom(ctx, types.StringType, key.Tags)
			diags.Append(tagDiags...)
			tags = value
		}
		models = append(models, kmsKeyListModel{
			KeyID:       types.StringValue(key.KeyID),
			Description: stringFromPtr(key.Description),
			Status:      types.StringValue(key.Status),
			Usage:       types.StringValue(key.Usage),
			Algorithm:   types.StringValue(key.Algorithm),
			CreatedAt:   types.StringValue(key.CreatedAt),
			CreatedBy:   stringFromPtr(key.CreatedBy),
			Tags:        tags,
		})
	}
	return models, diags
}

func kmsKeyListObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"key_id":      types.StringType,
			"description": types.StringType,
			"status":      types.StringType,
			"usage":       types.StringType,
			"algorithm":   types.StringType,
			"created_at":  types.StringType,
			"created_by":  types.StringType,
			"tags":        types.MapType{ElemType: types.StringType},
		},
	}
}

func init() {
	registerDataSource(NewKmsKeysDataSource)
}
