// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	dsbucketmetadatabackup "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/bucket_metadata_backup"
	dsgroups "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/groups"
	dshealthinfo "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/health_info"
	dsiambackup "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/iam_backup"
	dsiampolicies "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/iam_policies"
	dsiampolicy "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/iam_policy"
	dsilmtierstats "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/ilm_tier_stats"
	dskmsconfig "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/kms_config"
	dskmsstatus "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/kms_status"
	dsmetrics "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/metrics"
	dspools "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/pools"
	dsquota "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/quota"
	dsreplicationmetrics "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/replication_metrics"
	dsserverinfo "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/server_info"
	dsstorageinfo "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/storage_info"
	dsusermfa "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/user_mfa"
	dsusers "github.com/weinmann-emt/terraform-provider-rustfs/internal/datasources/users"

	rsaudittarget "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/audit_target"
	rsbucket "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket"
	rsbucketcors "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_cors"
	rsbucketdurability "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_durability"
	rsbucketencryption "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_encryption"
	rsbucketlifecycleconfiguration "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_lifecycle_configuration"
	rsbucketmetadatabackupimport "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_metadata_backup_import"
	rsbucketnotification "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_notification"
	rsbucketobjectlock "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_object_lock"
	rsbucketpolicy "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_policy"
	rsbucketpublicaccessblock "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_public_access_block"
	rsbucketreplication "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_replication"
	rsbuckettags "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_tags"
	rsbucketversioning "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/bucket_versioning"
	rsconfig "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/config"
	rsgroup "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/group"
	rsgrouppolicyattachment "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/group_policy_attachment"
	rsiambackupimport "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/iam_backup_import"
	rskmskey "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/kms_key"
	rsldappolicyattachment "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/ldap_policy_attachment"
	rsldapserviceaccount "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/ldap_service_account"
	rsmoduleswitch "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/module_switch"
	rspolicy "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/policy"
	rsquota "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/quota"
	rsrebalance "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/rebalance"
	rsremotetarget "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/remote_target"
	rsserviceaccount "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/service_account"
	rssitereplication "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/site_replication"
	rstier "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/tier"
	rsuser "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/user"
	rsuserpolicyattachment "github.com/weinmann-emt/terraform-provider-rustfs/internal/resources/user_policy_attachment"
)

// Ensure RustfsProvider satisfies various provider interfaces.
var _ provider.Provider = &RustfsProvider{}

// RustfsProvider defines the provider implementation.
type RustfsProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// RustfsProviderModel describes the provider data model.
type RustfsProviderModel struct {
	Endpoint     types.String `tfsdk:"endpoint"`
	AccessKey    types.String `tfsdk:"access_key"`
	SecretKey    types.String `tfsdk:"secret_key"`
	AccessSecret types.String `tfsdk:"access_secret"`
	Ssl          types.Bool   `tfsdk:"ssl"`
	Insecure     types.Bool   `tfsdk:"insecure"`
}

func (p *RustfsProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "rustfs"
	resp.Version = p.version
}

func (p *RustfsProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Interact with rustfs",
		MarkdownDescription: "Provider to access with RustFS",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "RUSTFS server endpoint in the format host:port. Defaults to RUSTFS_ENDPOINT environment variable.",
			},
			"access_key": schema.StringAttribute{
				Optional:    true,
				Description: "Username or access key. Defaults to RUSTFS_USER environment variable.",
			},
			"secret_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Secret key to be used as pass. Defaults to RUSTFS_SECRET environment variable.",
			},
			"access_secret": schema.StringAttribute{
				Optional:           true,
				Sensitive:          true,
				Description:        "Secret to be used as pass. Defaults to RUSTFS_SECRET environment variable.",
				DeprecationMessage: "Use secret_key instead. access_secret will be removed in a future release.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Insecure skip SSL validation",
			},
			"ssl": schema.BoolAttribute{
				Optional:    true,
				Description: "Use SSL transport",
			},
		},
	}
}

func (p *RustfsProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config RustfsProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	generatedConfig := generateRustClientConfig(config)

	endpoint := envOrDefault("RUSTFS_ENDPOINT", config.Endpoint.ValueString())
	if endpoint == "" {
		resp.Diagnostics.AddError(
			"Missing RUSTFS endpoint",
			"Set the endpoint in the provider block or via the RUSTFS_ENDPOINT environment variable.",
		)
		return
	}

	accessKey := envOrDefault("RUSTFS_USER", config.AccessKey.ValueString())
	secretKey := envOrDefault("RUSTFS_SECRET", config.secretKey())

	// Example client configuration for data sources and resources
	tr, err := minio.DefaultTransport(config.Ssl.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError(err.Error(), err.Error())
		return
	}
	usEast01 := "us-east-1"
	minio_client, err := minio.New(endpoint, &minio.Options{
		Secure:    config.Ssl.ValueBool(),
		Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
		Transport: tr,
		Region:    usEast01,
	})
	if err != nil {
		resp.Diagnostics.AddError(err.Error(), err.Error())
		return
	}
	client := &AllClient{
		Minio:      minio_client,
		RustClient: client.New(generatedConfig),
	}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *RustfsProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		rsuser.NewUserResource,
		rspolicy.NewPolicyResource,
		rsserviceaccount.NewServiceAccountResource,
		rsldapserviceaccount.NewLDAPServiceAccountResource,
		rsbucket.NewBucketResource,
		rsquota.NewQuotaResource,
		rsuserpolicyattachment.NewUserPolicyAttachmentResource,
		rsbucketpolicy.NewBucketPolicyResource,
		rsiambackupimport.NewIamBackupImportResource,
		rsbucketmetadatabackupimport.NewBucketMetadataBackupImportResource,
		rsgroup.NewGroupResource,
		rsgrouppolicyattachment.NewGroupPolicyAttachmentResource,
		rsbucketlifecycleconfiguration.NewBucketLifecycleConfigurationResource,
		rstier.NewTierResource,
		rsbucketobjectlock.NewBucketObjectLockResource,
		rsbucketnotification.NewBucketNotificationResource,
		rsrebalance.NewRebalanceResource,
		rsbucketreplication.NewBucketReplicationResource,
		rsbucketencryption.NewBucketEncryptionResource,
		rsbucketversioning.NewBucketVersioningResource,
		rsaudittarget.NewAuditTargetResource,
		rsmoduleswitch.NewModuleSwitchResource,
		rskmskey.NewKmsKeyResource,
		rsconfig.NewConfigResource,
		rsbucketdurability.NewBucketDurabilityResource,
		rssitereplication.NewSiteReplicationResource,
		rsremotetarget.NewRemoteTargetResource,
		rsbucketpublicaccessblock.NewBucketPublicAccessBlockResource,
		rsbuckettags.NewBucketTagsResource,
		rsbucketcors.NewBucketCorsResource,
		rsldappolicyattachment.NewLDAPPolicyAttachmentResource,
	}
}

func (p *RustfsProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		dspools.NewPoolsDataSource,
		dsiambackup.NewIamBackupDataSource,
		dsbucketmetadatabackup.NewBucketMetadataBackupDataSource,
		dsusers.NewUsersDataSource,
		dsusermfa.NewUserMfaDataSource,
		dsiampolicies.NewIAMPoliciesDataSource,
		dsiampolicy.NewIAMPolicyDataSource,
		dskmsstatus.NewKmsStatusDataSource,
		dskmsconfig.NewKmsConfigDataSource,
		dsquota.NewQuotaDataSource,
		dsgroups.NewGroupsDataSource,
		dsmetrics.NewMetricsDataSource,
		dshealthinfo.NewHealthInfoDataSource,
		dsstorageinfo.NewStorageInfoDataSource,
		dsserverinfo.NewServerInfoDataSource,
		dsreplicationmetrics.NewReplicationMetricsDataSource,
		dsilmtierstats.NewIlmTierStatsDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &RustfsProvider{
			version: version,
		}
	}
}
