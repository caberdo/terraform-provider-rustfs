// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &SiteReplicationStatusDataSource{}

// SiteReplicationStatusDataSource reports the site-replication group of this
// deployment: the configured peers from site-replication/info, the per-site
// replication counters from site-replication/status, and the topology counts
// from site-replication/metainfo.
type SiteReplicationStatusDataSource struct {
	client *AllClient
}

type SiteReplicationStatusDataSourceModel struct {
	ID                types.String `tfsdk:"id"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	Name              types.String `tfsdk:"name"`
	DeploymentID      types.String `tfsdk:"deployment_id"`
	APIVersion        types.String `tfsdk:"api_version"`
	MaxBuckets        types.Int64  `tfsdk:"max_buckets"`
	MaxUsers          types.Int64  `tfsdk:"max_users"`
	MaxGroups         types.Int64  `tfsdk:"max_groups"`
	MaxPolicies       types.Int64  `tfsdk:"max_policies"`
	MaxILMExpiryRules types.Int64  `tfsdk:"max_ilm_expiry_rules"`
	StatusAvailable   types.Bool   `tfsdk:"status_available"`
	Sites             types.List   `tfsdk:"sites"`
	PeerErrors        types.List   `tfsdk:"peer_errors"`
	RetryPending      types.Int64  `tfsdk:"retry_pending"`
	RetryFailed       types.Int64  `tfsdk:"retry_failed"`
	RetryLastError    types.String `tfsdk:"retry_last_error"`
	MetaInfo          types.Object `tfsdk:"metainfo"`
}

// siteReplicationSiteModel is one peer as state records it.
type siteReplicationSiteModel struct {
	Name               types.String `tfsdk:"name"`
	Endpoint           types.String `tfsdk:"endpoint"`
	DeploymentID       types.String `tfsdk:"deployment_id"`
	SyncState          types.String `tfsdk:"sync_state"`
	SkipTLSVerify      types.Bool   `tfsdk:"skip_tls_verify"`
	ReplicateILMExpiry types.Bool   `tfsdk:"replicate_ilm_expiry"`
	Stats              types.Object `tfsdk:"stats"`
}

// siteReplicationStatsModel is one site's replicated/total counters.
type siteReplicationStatsModel struct {
	ReplicatedBuckets             types.Int64 `tfsdk:"replicated_buckets"`
	ReplicatedTags                types.Int64 `tfsdk:"replicated_tags"`
	ReplicatedBucketPolicies      types.Int64 `tfsdk:"replicated_bucket_policies"`
	ReplicatedIAMPolicies         types.Int64 `tfsdk:"replicated_iam_policies"`
	ReplicatedUsers               types.Int64 `tfsdk:"replicated_users"`
	ReplicatedGroups              types.Int64 `tfsdk:"replicated_groups"`
	ReplicatedLockConfig          types.Int64 `tfsdk:"replicated_lock_config"`
	ReplicatedSSEConfig           types.Int64 `tfsdk:"replicated_sse_config"`
	ReplicatedVersioningConfig    types.Int64 `tfsdk:"replicated_versioning_config"`
	ReplicatedQuotaConfig         types.Int64 `tfsdk:"replicated_quota_config"`
	ReplicatedUserPolicyMappings  types.Int64 `tfsdk:"replicated_user_policy_mappings"`
	ReplicatedGroupPolicyMappings types.Int64 `tfsdk:"replicated_group_policy_mappings"`
	ReplicatedILMExpiryRules      types.Int64 `tfsdk:"replicated_ilm_expiry_rules"`
	ReplicatedCorsConfig          types.Int64 `tfsdk:"replicated_cors_config"`
	TotalBucketsCount             types.Int64 `tfsdk:"total_buckets_count"`
	TotalTagsCount                types.Int64 `tfsdk:"total_tags_count"`
	TotalBucketPoliciesCount      types.Int64 `tfsdk:"total_bucket_policies_count"`
	TotalIAMPoliciesCount         types.Int64 `tfsdk:"total_iam_policies_count"`
	TotalLockConfigCount          types.Int64 `tfsdk:"total_lock_config_count"`
	TotalSSEConfigCount           types.Int64 `tfsdk:"total_sse_config_count"`
	TotalVersioningConfigCount    types.Int64 `tfsdk:"total_versioning_config_count"`
	TotalQuotaConfigCount         types.Int64 `tfsdk:"total_quota_config_count"`
	TotalUsersCount               types.Int64 `tfsdk:"total_users_count"`
	TotalGroupsCount              types.Int64 `tfsdk:"total_groups_count"`
	TotalUserPolicyMappingCount   types.Int64 `tfsdk:"total_user_policy_mapping_count"`
	TotalGroupPolicyMappingCount  types.Int64 `tfsdk:"total_group_policy_mapping_count"`
	TotalILMExpiryRulesCount      types.Int64 `tfsdk:"total_ilm_expiry_rules_count"`
	TotalCorsConfigCount          types.Int64 `tfsdk:"total_cors_config_count"`
}

// siteReplicationPeerErrorModel is one unreachable peer.
type siteReplicationPeerErrorModel struct {
	Name     types.String `tfsdk:"name"`
	Endpoint types.String `tfsdk:"endpoint"`
	Error    types.String `tfsdk:"error"`
}

// siteReplicationMetaInfoModel is the topology summary from the metainfo
// route. The payload itself is not retained: it holds every bucket, policy and
// IAM document of every site.
type siteReplicationMetaInfoModel struct {
	Name                   types.String `tfsdk:"name"`
	DeploymentID           types.String `tfsdk:"deployment_id"`
	StateName              types.String `tfsdk:"state_name"`
	StateUpdatedAt         types.String `tfsdk:"state_updated_at"`
	PeerCount              types.Int64  `tfsdk:"peer_count"`
	BucketCount            types.Int64  `tfsdk:"bucket_count"`
	PolicyCount            types.Int64  `tfsdk:"policy_count"`
	UserCount              types.Int64  `tfsdk:"user_count"`
	GroupCount             types.Int64  `tfsdk:"group_count"`
	UserPolicyCount        types.Int64  `tfsdk:"user_policy_count"`
	GroupPolicyCount       types.Int64  `tfsdk:"group_policy_count"`
	ILMExpiryRuleCount     types.Int64  `tfsdk:"ilm_expiry_rule_count"`
	ReplicationConfigCount types.Int64  `tfsdk:"replication_config_count"`
}

var siteReplicationStatsAttrTypes = map[string]attr.Type{
	"replicated_buckets":               types.Int64Type,
	"replicated_tags":                  types.Int64Type,
	"replicated_bucket_policies":       types.Int64Type,
	"replicated_iam_policies":          types.Int64Type,
	"replicated_users":                 types.Int64Type,
	"replicated_groups":                types.Int64Type,
	"replicated_lock_config":           types.Int64Type,
	"replicated_sse_config":            types.Int64Type,
	"replicated_versioning_config":     types.Int64Type,
	"replicated_quota_config":          types.Int64Type,
	"replicated_user_policy_mappings":  types.Int64Type,
	"replicated_group_policy_mappings": types.Int64Type,
	"replicated_ilm_expiry_rules":      types.Int64Type,
	"replicated_cors_config":           types.Int64Type,
	"total_buckets_count":              types.Int64Type,
	"total_tags_count":                 types.Int64Type,
	"total_bucket_policies_count":      types.Int64Type,
	"total_iam_policies_count":         types.Int64Type,
	"total_lock_config_count":          types.Int64Type,
	"total_sse_config_count":           types.Int64Type,
	"total_versioning_config_count":    types.Int64Type,
	"total_quota_config_count":         types.Int64Type,
	"total_users_count":                types.Int64Type,
	"total_groups_count":               types.Int64Type,
	"total_user_policy_mapping_count":  types.Int64Type,
	"total_group_policy_mapping_count": types.Int64Type,
	"total_ilm_expiry_rules_count":     types.Int64Type,
	"total_cors_config_count":          types.Int64Type,
}

var siteReplicationSiteAttrTypes = map[string]attr.Type{
	"name":                 types.StringType,
	"endpoint":             types.StringType,
	"deployment_id":        types.StringType,
	"sync_state":           types.StringType,
	"skip_tls_verify":      types.BoolType,
	"replicate_ilm_expiry": types.BoolType,
	"stats":                types.ObjectType{AttrTypes: siteReplicationStatsAttrTypes},
}

var siteReplicationPeerErrorAttrTypes = map[string]attr.Type{
	"name":     types.StringType,
	"endpoint": types.StringType,
	"error":    types.StringType,
}

var siteReplicationMetaInfoAttrTypes = map[string]attr.Type{
	"name":                     types.StringType,
	"deployment_id":            types.StringType,
	"state_name":               types.StringType,
	"state_updated_at":         types.StringType,
	"peer_count":               types.Int64Type,
	"bucket_count":             types.Int64Type,
	"policy_count":             types.Int64Type,
	"user_count":               types.Int64Type,
	"group_count":              types.Int64Type,
	"user_policy_count":        types.Int64Type,
	"group_policy_count":       types.Int64Type,
	"ilm_expiry_rule_count":    types.Int64Type,
	"replication_config_count": types.Int64Type,
}

func NewSiteReplicationStatusDataSource() datasource.DataSource {
	return &SiteReplicationStatusDataSource{}
}

func (d *SiteReplicationStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_replication_status"
}

func (d *SiteReplicationStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Read the site-replication state of the RustFS deployment: the peers from " +
		"GET /rustfs/admin/v3/site-replication/info, the per-site replication counters from " +
		"GET /rustfs/admin/v3/site-replication/status, and the topology counts from " +
		"GET /rustfs/admin/v3/site-replication/metainfo. Read-only: it never changes the group.\n\n" +
		"`status_available` reports whether the status route answered. RustFS 1.0.1 through 1.0.3 " +
		"implement it; a server that answers it as unimplemented still yields the info and metainfo " +
		"result with `status_available = false`, empty status collections and every site's " +
		"`sync_state` set to `unknown`, instead of failing the read. Any other status error fails the " +
		"read, and so does a metainfo error: the metainfo surface is what this data source is for."
	resp.Schema = schema.Schema{
		Description:         "Read the RustFS site-replication status",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"enabled": schema.BoolAttribute{
				Computed: true,
				Description: "Whether site replication is enabled on this deployment. Read from " +
					"`site-replication/info`, falling back to `site-replication/status` when info reports it " +
					"disabled but the live status reports it enabled.",
			},
			"name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of this (local) site.",
			},
			"deployment_id": schema.StringAttribute{
				Computed: true,
				Description: "Deployment ID of this site, as reported by the metainfo route; the info route " +
					"does not carry it.",
			},
			"api_version": schema.StringAttribute{
				Computed:    true,
				Description: "Site-replication API version the server reports.",
			},
			"max_buckets": schema.Int64Attribute{
				Computed:    true,
				Description: "Highest bucket count any site reports, or 0 when the status route is unavailable.",
			},
			"max_users": schema.Int64Attribute{
				Computed:    true,
				Description: "Highest IAM user count any site reports, or 0 when the status route is unavailable.",
			},
			"max_groups": schema.Int64Attribute{
				Computed:    true,
				Description: "Highest IAM group count any site reports, or 0 when the status route is unavailable.",
			},
			"max_policies": schema.Int64Attribute{
				Computed:    true,
				Description: "Highest IAM policy count any site reports, or 0 when the status route is unavailable.",
			},
			"max_ilm_expiry_rules": schema.Int64Attribute{
				Computed: true,
				Description: "Highest lifecycle-expiry rule count any site reports, or 0 when the status route " +
					"is unavailable.",
			},
			"status_available": schema.BoolAttribute{
				Computed: true,
				Description: "Whether the status route answered. When false, `sites`, `peer_errors` and the " +
					"status counters are empty and every `sync_state` is `unknown`.",
			},
			"sites": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Every site in the group, sorted by name.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Site name.",
						},
						"endpoint": schema.StringAttribute{
							Computed:    true,
							Description: "Admin endpoint of the site.",
						},
						"deployment_id": schema.StringAttribute{
							Computed:    true,
							Description: "Deployment ID that identifies the site to the server.",
						},
						"sync_state": schema.StringAttribute{
							Computed: true,
							Description: "Replication state the server reports for this site: `enable`, `disable`, " +
								"or `unknown` when the site is unreachable or the status route is unavailable.",
						},
						"skip_tls_verify": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether the provider's site-replication link skips TLS verification.",
						},
						"replicate_ilm_expiry": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether lifecycle-expiry rules replicate to this site.",
						},
						"stats": schema.SingleNestedAttribute{
							Computed: true,
							Description: "Replicated and total counters for this site. Zero counters when the " +
								"status route is unavailable.",
							Attributes: siteReplicationStatsSchemaAttributes(),
						},
					},
				},
			},
			"peer_errors": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Peers the status read could not reach, sorted by name.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Site name.",
						},
						"endpoint": schema.StringAttribute{
							Computed:    true,
							Description: "Admin endpoint of the site.",
						},
						"error": schema.StringAttribute{
							Computed:    true,
							Description: "Why the status read could not collect the site's state.",
						},
					},
				},
			},
			"retry_pending": schema.Int64Attribute{
				Computed:    true,
				Description: "Peer deliveries still queued for retry, or 0 when the status route is unavailable.",
			},
			"retry_failed": schema.Int64Attribute{
				Computed: true,
				Description: "Peer deliveries that exhausted their retries, or 0 when the status route is " +
					"unavailable.",
			},
			"retry_last_error": schema.StringAttribute{
				Computed: true,
				Description: "Most recent delivery error the retry queue recorded, empty when there is none or " +
					"the status route is unavailable.",
			},
			"metainfo": schema.SingleNestedAttribute{
				Computed: true,
				Description: "Topology summary of this site. The route returns every replicated document; only " +
					"its size and identity are exposed here.",
				Attributes: siteReplicationMetaInfoSchemaAttributes(),
			},
		},
	}
}

// siteReplicationStatsSchemaAttributes is the stats object shared by the schema
// and the attr types, so the two cannot drift.
func siteReplicationStatsSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"replicated_buckets": schema.Int64Attribute{
			Computed:    true,
			Description: "Buckets replicated to this site.",
		},
		"replicated_tags": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket tag sets replicated to this site.",
		},
		"replicated_bucket_policies": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket policies replicated to this site.",
		},
		"replicated_iam_policies": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM policies replicated to this site.",
		},
		"replicated_users": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM users replicated to this site.",
		},
		"replicated_groups": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM groups replicated to this site.",
		},
		"replicated_lock_config": schema.Int64Attribute{
			Computed:    true,
			Description: "Object-lock configurations replicated to this site.",
		},
		"replicated_sse_config": schema.Int64Attribute{
			Computed:    true,
			Description: "Server-side-encryption configurations replicated to this site.",
		},
		"replicated_versioning_config": schema.Int64Attribute{
			Computed:    true,
			Description: "Versioning configurations replicated to this site.",
		},
		"replicated_quota_config": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket quota configurations replicated to this site.",
		},
		"replicated_user_policy_mappings": schema.Int64Attribute{
			Computed:    true,
			Description: "User-to-policy mappings replicated to this site.",
		},
		"replicated_group_policy_mappings": schema.Int64Attribute{
			Computed:    true,
			Description: "Group-to-policy mappings replicated to this site.",
		},
		"replicated_ilm_expiry_rules": schema.Int64Attribute{
			Computed:    true,
			Description: "Lifecycle-expiry rules replicated to this site.",
		},
		"replicated_cors_config": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket CORS configurations replicated to this site.",
		},
		"total_buckets_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Buckets the site reports in total.",
		},
		"total_tags_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket tag sets the site reports in total.",
		},
		"total_bucket_policies_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket policies the site reports in total.",
		},
		"total_iam_policies_count": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM policies the site reports in total.",
		},
		"total_lock_config_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Object-lock configurations the site reports in total.",
		},
		"total_sse_config_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Server-side-encryption configurations the site reports in total.",
		},
		"total_versioning_config_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Versioning configurations the site reports in total.",
		},
		"total_quota_config_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket quota configurations the site reports in total.",
		},
		"total_users_count": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM users the site reports in total.",
		},
		"total_groups_count": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM groups the site reports in total.",
		},
		"total_user_policy_mapping_count": schema.Int64Attribute{
			Computed:    true,
			Description: "User-to-policy mappings the site reports in total.",
		},
		"total_group_policy_mapping_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Group-to-policy mappings the site reports in total.",
		},
		"total_ilm_expiry_rules_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Lifecycle-expiry rules the site reports in total.",
		},
		"total_cors_config_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket CORS configurations the site reports in total.",
		},
	}
}

// siteReplicationMetaInfoSchemaAttributes is the metainfo object shared by the
// schema and the attr types.
func siteReplicationMetaInfoSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"name": schema.StringAttribute{
			Computed:    true,
			Description: "Name of this (local) site in the replicated state.",
		},
		"deployment_id": schema.StringAttribute{
			Computed:    true,
			Description: "Deployment ID of this site.",
		},
		"state_name": schema.StringAttribute{
			Computed:    true,
			Description: "Name the persisted site-replication state records for this site.",
		},
		"state_updated_at": schema.StringAttribute{
			Computed:    true,
			Description: "When the persisted site-replication state last changed, in RFC 3339.",
		},
		"peer_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Peers recorded in the persisted site-replication state.",
		},
		"bucket_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Buckets in this site's replicated inventory.",
		},
		"policy_count": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM policies in this site's replicated inventory.",
		},
		"user_count": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM users in this site's replicated inventory.",
		},
		"group_count": schema.Int64Attribute{
			Computed:    true,
			Description: "IAM groups in this site's replicated inventory.",
		},
		"user_policy_count": schema.Int64Attribute{
			Computed:    true,
			Description: "User-to-policy mappings in this site's replicated inventory.",
		},
		"group_policy_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Group-to-policy mappings in this site's replicated inventory.",
		},
		"ilm_expiry_rule_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Lifecycle-expiry rules in this site's replicated inventory.",
		},
		"replication_config_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Bucket replication configurations this site reports.",
		},
	}
}

func (d *SiteReplicationStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SiteReplicationStatusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.RustClient.SiteReplicationInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading site replication info",
			"Could not read the site-replication topology: "+err.Error(),
		)
		return
	}

	var status rustfs.SiteReplicationStatusInfo
	statusAvailable := true
	status, err = d.client.RustClient.SiteReplicationStatus(ctx)
	if err != nil {
		if !rustfs.IsUnsupportedFeature(err) {
			resp.Diagnostics.AddError(
				"Error reading site replication status",
				"Could not read the site-replication status: "+err.Error(),
			)
			return
		}
		statusAvailable = false
		status = rustfs.SiteReplicationStatusInfo{}
	}

	metainfo, err := d.client.RustClient.SiteReplicationMetaInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading site replication metainfo",
			"Could not read the site-replication metainfo: "+err.Error(),
		)
		return
	}

	enabled := info.Enabled
	if !enabled && statusAvailable {
		// info and status read the same state, but info reports a disabled
		// deployment with no sites while status is the live view; fall back so
		// a mismatch does not hide an enabled group.
		enabled = status.Enabled
	}

	apiVersion := metainfo.APIVersion
	if statusAvailable && status.APIVersion != "" {
		apiVersion = status.APIVersion
	}

	state := SiteReplicationStatusDataSourceModel{
		ID:                types.StringValue("site-replication-status"),
		Enabled:           types.BoolValue(enabled),
		Name:              types.StringValue(info.Name),
		DeploymentID:      types.StringValue(metainfo.DeploymentID),
		APIVersion:        types.StringValue(apiVersion),
		MaxBuckets:        types.Int64Value(status.MaxBuckets),
		MaxUsers:          types.Int64Value(status.MaxUsers),
		MaxGroups:         types.Int64Value(status.MaxGroups),
		MaxPolicies:       types.Int64Value(status.MaxPolicies),
		MaxILMExpiryRules: types.Int64Value(status.MaxILMExpiryRules),
		StatusAvailable:   types.BoolValue(statusAvailable),
		RetryPending:      types.Int64Value(0),
		RetryFailed:       types.Int64Value(0),
		RetryLastError:    types.StringValue(""),
	}
	if status.RetryStats != nil {
		state.RetryPending = types.Int64Value(status.RetryStats.Pending)
		state.RetryFailed = types.Int64Value(status.RetryStats.Failed)
		state.RetryLastError = types.StringValue(status.RetryStats.LastError)
	}

	sites, diags := siteReplicationSitesValue(ctx, info, status, statusAvailable)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Sites = sites

	peerErrors, diags := siteReplicationPeerErrorsValue(ctx, status, statusAvailable)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.PeerErrors = peerErrors

	meta, diags := types.ObjectValueFrom(ctx, siteReplicationMetaInfoAttrTypes, siteReplicationMetaInfoModel{
		Name:                   types.StringValue(metainfo.Name),
		DeploymentID:           types.StringValue(metainfo.DeploymentID),
		StateName:              types.StringValue(metainfo.StateName),
		StateUpdatedAt:         types.StringValue(metainfo.StateUpdatedAt),
		PeerCount:              types.Int64Value(metainfo.PeerCount),
		BucketCount:            types.Int64Value(metainfo.BucketCount),
		PolicyCount:            types.Int64Value(metainfo.PolicyCount),
		UserCount:              types.Int64Value(metainfo.UserCount),
		GroupCount:             types.Int64Value(metainfo.GroupCount),
		UserPolicyCount:        types.Int64Value(metainfo.UserPolicyCount),
		GroupPolicyCount:       types.Int64Value(metainfo.GroupPolicyCount),
		ILMExpiryRuleCount:     types.Int64Value(metainfo.ILMExpiryRuleCount),
		ReplicationConfigCount: types.Int64Value(metainfo.ReplicationConfigCount),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.MetaInfo = meta

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// siteReplicationSitesValue merges the configured peers (info) with the live
// per-site counters (status) and sorts the result by name. A site the status
// route did not report keeps sync_state "unknown".
func siteReplicationSitesValue(
	ctx context.Context,
	info rustfs.SiteReplicationInfo,
	status rustfs.SiteReplicationStatusInfo,
	statusAvailable bool,
) (types.List, diag.Diagnostics) {
	byDeployment := make(map[string]rustfs.SiteReplicationPeerStatus, len(status.Sites))
	byName := make(map[string]rustfs.SiteReplicationPeerStatus, len(status.Sites))
	for key, peer := range status.Sites {
		// The status route keys its site map by deployment ID, so a value that
		// omits the field still identifies the site through the key.
		if peer.DeploymentID == "" {
			peer.DeploymentID = key
		}
		if peer.DeploymentID != "" {
			byDeployment[peer.DeploymentID] = peer
		}
		if peer.Name != "" {
			byName[peer.Name] = peer
		}
	}

	type mergedSite struct {
		name   string
		peer   rustfs.SiteReplicationPeer
		live   rustfs.SiteReplicationPeerStatus
		liveOK bool
	}

	sites := make([]mergedSite, 0, len(info.Sites))
	seen := make(map[string]struct{}, len(info.Sites))
	for _, peer := range info.Sites {
		live, ok := byDeployment[peer.DeploymentID]
		if !ok && peer.Name != "" {
			live, ok = byName[peer.Name]
		}
		if statusAvailable && ok {
			sites = append(sites, mergedSite{name: peer.Name, peer: peer, live: live, liveOK: true})
		} else {
			sites = append(sites, mergedSite{name: peer.Name, peer: peer})
		}
		seen[peer.DeploymentID] = struct{}{}
		if peer.Name != "" {
			seen["name/"+peer.Name] = struct{}{}
		}
	}

	// A status read can know a peer the info route did not list. Keep it rather
	// than silently dropping a site; its name comes from the status value.
	if statusAvailable {
		for key, live := range status.Sites {
			if live.DeploymentID == "" {
				live.DeploymentID = key
			}
			if deploymentID := live.DeploymentID; deploymentID != "" {
				if _, ok := seen[deploymentID]; ok {
					continue
				}
			}
			if live.Name != "" {
				if _, ok := seen["name/"+live.Name]; ok {
					continue
				}
			}
			sites = append(sites, mergedSite{
				name:   live.Name,
				peer:   rustfs.SiteReplicationPeer{Name: live.Name, Endpoint: live.Endpoint, DeploymentID: live.DeploymentID},
				live:   live,
				liveOK: true,
			})
		}
	}

	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].name != sites[j].name {
			return sites[i].name < sites[j].name
		}
		return sites[i].peer.DeploymentID < sites[j].peer.DeploymentID
	})

	models := make([]siteReplicationSiteModel, 0, len(sites))
	for _, site := range sites {
		peer := site.peer
		syncState := "unknown"
		skipTLSVerify := peer.SkipTLSVerify
		replicateILMExpiry := false
		deploymentID := peer.DeploymentID
		if site.liveOK {
			if site.live.SyncState != "" {
				syncState = site.live.SyncState
			}
			skipTLSVerify = site.live.SkipTLSVerify
			replicateILMExpiry = site.live.ReplicateILMExpiry
			if deploymentID == "" {
				deploymentID = site.live.DeploymentID
			}
		}

		summary := status.StatsSummary[deploymentID]
		stats, diags := types.ObjectValueFrom(ctx, siteReplicationStatsAttrTypes, siteReplicationStatsModel{
			ReplicatedBuckets:             types.Int64Value(summary.ReplicatedBuckets),
			ReplicatedTags:                types.Int64Value(summary.ReplicatedTags),
			ReplicatedBucketPolicies:      types.Int64Value(summary.ReplicatedBucketPolicies),
			ReplicatedIAMPolicies:         types.Int64Value(summary.ReplicatedIAMPolicies),
			ReplicatedUsers:               types.Int64Value(summary.ReplicatedUsers),
			ReplicatedGroups:              types.Int64Value(summary.ReplicatedGroups),
			ReplicatedLockConfig:          types.Int64Value(summary.ReplicatedLockConfig),
			ReplicatedSSEConfig:           types.Int64Value(summary.ReplicatedSSEConfig),
			ReplicatedVersioningConfig:    types.Int64Value(summary.ReplicatedVersioningConfig),
			ReplicatedQuotaConfig:         types.Int64Value(summary.ReplicatedQuotaConfig),
			ReplicatedUserPolicyMappings:  types.Int64Value(summary.ReplicatedUserPolicyMappings),
			ReplicatedGroupPolicyMappings: types.Int64Value(summary.ReplicatedGroupPolicyMappings),
			ReplicatedILMExpiryRules:      types.Int64Value(summary.ReplicatedILMExpiryRules),
			ReplicatedCorsConfig:          types.Int64Value(summary.ReplicatedCorsConfig),
			TotalBucketsCount:             types.Int64Value(summary.TotalBucketsCount),
			TotalTagsCount:                types.Int64Value(summary.TotalTagsCount),
			TotalBucketPoliciesCount:      types.Int64Value(summary.TotalBucketPoliciesCount),
			TotalIAMPoliciesCount:         types.Int64Value(summary.TotalIAMPoliciesCount),
			TotalLockConfigCount:          types.Int64Value(summary.TotalLockConfigCount),
			TotalSSEConfigCount:           types.Int64Value(summary.TotalSSEConfigCount),
			TotalVersioningConfigCount:    types.Int64Value(summary.TotalVersioningConfigCount),
			TotalQuotaConfigCount:         types.Int64Value(summary.TotalQuotaConfigCount),
			TotalUsersCount:               types.Int64Value(summary.TotalUsersCount),
			TotalGroupsCount:              types.Int64Value(summary.TotalGroupsCount),
			TotalUserPolicyMappingCount:   types.Int64Value(summary.TotalUserPolicyMappingCount),
			TotalGroupPolicyMappingCount:  types.Int64Value(summary.TotalGroupPolicyMappingCount),
			TotalILMExpiryRulesCount:      types.Int64Value(summary.TotalILMExpiryRulesCount),
			TotalCorsConfigCount:          types.Int64Value(summary.TotalCorsConfigCount),
		})
		if diags.HasError() {
			return types.ListNull(types.ObjectType{AttrTypes: siteReplicationSiteAttrTypes}), diags
		}

		models = append(models, siteReplicationSiteModel{
			Name:               types.StringValue(site.name),
			Endpoint:           types.StringValue(peer.Endpoint),
			DeploymentID:       types.StringValue(deploymentID),
			SyncState:          types.StringValue(syncState),
			SkipTLSVerify:      types.BoolValue(skipTLSVerify),
			ReplicateILMExpiry: types.BoolValue(replicateILMExpiry),
			Stats:              stats,
		})
	}

	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: siteReplicationSiteAttrTypes}, models)
}

// siteReplicationPeerErrorsValue sorts the peer errors by name so a status
// read writes a deterministic result.
func siteReplicationPeerErrorsValue(
	ctx context.Context,
	status rustfs.SiteReplicationStatusInfo,
	statusAvailable bool,
) (types.List, diag.Diagnostics) {
	if !statusAvailable {
		return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: siteReplicationPeerErrorAttrTypes}, []siteReplicationPeerErrorModel{})
	}

	peers := make([]rustfs.SiteReplicationPeerError, 0, len(status.PeerErrors))
	for _, peer := range status.PeerErrors {
		peers = append(peers, peer)
	}
	sort.SliceStable(peers, func(i, j int) bool {
		if peers[i].Name != peers[j].Name {
			return peers[i].Name < peers[j].Name
		}
		return peers[i].Endpoint < peers[j].Endpoint
	})

	models := make([]siteReplicationPeerErrorModel, 0, len(peers))
	for _, peer := range peers {
		models = append(models, siteReplicationPeerErrorModel{
			Name:     types.StringValue(peer.Name),
			Endpoint: types.StringValue(peer.Endpoint),
			Error:    types.StringValue(peer.Error),
		})
	}
	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: siteReplicationPeerErrorAttrTypes}, models)
}

func init() {
	registerDataSource(NewSiteReplicationStatusDataSource)
}
