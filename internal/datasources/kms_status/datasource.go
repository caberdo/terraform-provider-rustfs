package kms_status

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/models"
)

var _ datasource.DataSource = &KmsStatusDataSource{}

type KmsStatusDataSource struct {
	client *client.AllClient
}

func NewKmsStatusDataSource() datasource.DataSource {
	return &KmsStatusDataSource{}
}

func (d *KmsStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kms_status"
}

func (d *KmsStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Get the RustFS KMS service status",
		MarkdownDescription: "Fetch the current RustFS KMS service status, including backend health, cache stats, capabilities, and cluster config.",
		Attributes: map[string]schema.Attribute{
			"backend_type": schema.StringAttribute{
				Computed:    true,
				Description: "KMS backend type (local, vault-kv2, vault-transit, static, aws).",
			},
			"backend_status": schema.StringAttribute{
				Computed:    true,
				Description: "KMS backend health status (healthy, unhealthy, error).",
			},
			"cache_enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the KMS key cache is enabled.",
			},
			"cache_stats": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "KMS cache statistics. Null when caching is disabled.",
				Attributes: map[string]schema.Attribute{
					"hit_count": schema.Int64Attribute{
						Computed:    true,
						Description: "Number of cache hits.",
					},
					"miss_count": schema.Int64Attribute{
						Computed:    true,
						Description: "Number of cache misses.",
					},
					"entry_count": schema.Int64Attribute{
						Computed:    true,
						Description: "Number of cache entries.",
					},
					"eviction_count": schema.Int64Attribute{
						Computed:    true,
						Description: "Number of cache evictions.",
					},
				},
			},
			"default_key_id": schema.StringAttribute{
				Computed:    true,
				Description: "Default KMS key ID. Null when no default key is configured.",
			},
			"capabilities": schema.MapAttribute{
				Computed:    true,
				ElementType: types.BoolType,
				Description: "Map of KMS backend capabilities. Present only on servers that report it.",
			},
			"cluster_config": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Cluster-wide KMS configuration state. Present only on servers that report it.",
				Attributes: map[string]schema.Attribute{
					"consistent": schema.BoolAttribute{
						Computed:    true,
						Description: "Whether all nodes report the same KMS config fingerprint.",
					},
					"nodes": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"host": schema.StringAttribute{
									Computed:    true,
									Description: "Node host or address.",
								},
								"config_fingerprint": schema.StringAttribute{
									Computed:    true,
									Description: "Node KMS config fingerprint. Null when the node has no KMS config.",
								},
								"error": schema.StringAttribute{
									Computed:    true,
									Description: "Node KMS error, if any.",
								},
							},
						},
						Description: "Per-node KMS configuration state.",
					},
				},
			},
		},
	}
}

func (d *KmsStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = c
}

func (d *KmsStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config models.KmsStatusDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := d.client.RustClient.GetKmsStatus()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading KMS status",
			"Could not read KMS status: "+err.Error(),
		)
		return
	}

	config, diags := kmsStatusModelFromStatus(ctx, status)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func kmsStatusModelFromStatus(ctx context.Context, status *client.KmsStatus) (models.KmsStatusDataSourceModel, diag.Diagnostics) {
	var config models.KmsStatusDataSourceModel
	var diags diag.Diagnostics

	config.BackendType = types.StringValue(status.BackendType)
	config.BackendStatus = types.StringValue(status.BackendStatus)
	config.CacheEnabled = types.BoolValue(status.CacheEnabled)
	config.DefaultKeyID = stringFromPtr(status.DefaultKeyID)

	if status.CacheStats != nil {
		config.CacheStats = &models.KmsCacheStatsModel{
			HitCount:      types.Int64Value(int64(status.CacheStats.HitCount)),      // #nosec G115
			MissCount:     types.Int64Value(int64(status.CacheStats.MissCount)),     // #nosec G115
			EntryCount:    types.Int64Value(int64(status.CacheStats.EntryCount)),    // #nosec G115
			EvictionCount: types.Int64Value(int64(status.CacheStats.EvictionCount)), // #nosec G115
		}
	}

	if status.Capabilities != nil {
		capabilities, d := types.MapValueFrom(ctx, types.BoolType, status.Capabilities)
		diags.Append(d...)
		if diags.HasError() {
			return config, diags
		}
		config.Capabilities = capabilities
	} else {
		config.Capabilities = types.MapNull(types.BoolType)
	}

	if status.ClusterConfig != nil {
		nodes := make([]models.KmsClusterNodeModel, 0, len(status.ClusterConfig.Nodes))
		for _, node := range status.ClusterConfig.Nodes {
			nodes = append(nodes, models.KmsClusterNodeModel{
				Host:              types.StringValue(node.Host),
				ConfigFingerprint: stringFromPtr(node.ConfigFingerprint),
				Error:             stringFromPtr(node.Error),
			})
		}
		config.ClusterConfig = &models.KmsClusterConfigModel{
			Consistent: types.BoolValue(status.ClusterConfig.Consistent),
			Nodes:      nodes,
		}
	}

	return config, diags
}
