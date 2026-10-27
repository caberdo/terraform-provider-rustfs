// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                   = &bucketOnDemandMigrationResource{}
	_ resource.ResourceWithImportState    = &bucketOnDemandMigrationResource{}
	_ resource.ResourceWithValidateConfig = &bucketOnDemandMigrationResource{}
)

// odmProviderValues is every source family the RustFS config model accepts.
var odmProviderValues = []string{
	"s3", "aws", "minio", "rustfs", "r2", "gcs", "azure", "gcs_native",
}

// odmAzureAccountPattern mirrors the server rule: the account name becomes the
// first label of the derived Azure host.
var odmAzureAccountPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// odmSourceBucketPattern mirrors the server rule: no path separator and no
// whitespace.
var odmSourceBucketPattern = regexp.MustCompile(`^[^/\s]+$`)

// odmAWSRegionPattern mirrors the server rule for an aws region that feeds a
// derived hostname.
var odmAWSRegionPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

func NewBucketOnDemandMigrationResource() resource.Resource {
	return &bucketOnDemandMigrationResource{}
}

// bucketOnDemandMigrationResource manages a bucket's on-demand migration
// configuration: an external S3-compatible source whose objects are pulled on
// first read and written back locally.
type bucketOnDemandMigrationResource struct {
	client *AllClient
}

type bucketOnDemandMigrationResourceModel struct {
	ID        types.String    `tfsdk:"id"`
	Bucket    types.String    `tfsdk:"bucket"`
	Enabled   types.Bool      `tfsdk:"enabled"`
	Version   types.Int64     `tfsdk:"version"`
	Source    *odmSourceModel `tfsdk:"source"`
	Filter    *odmFilterModel `tfsdk:"filter"`
	Policy    *odmPolicyModel `tfsdk:"policy"`
	UpdatedAt types.String    `tfsdk:"updated_at"`
}

type odmSourceModel struct {
	Provider    types.String         `tfsdk:"provider"`
	Endpoint    types.String         `tfsdk:"endpoint"`
	Region      types.String         `tfsdk:"region"`
	Bucket      types.String         `tfsdk:"bucket"`
	PathStyle   types.String         `tfsdk:"path_style"`
	Credentials *odmCredentialsModel `tfsdk:"credentials"`
	TLS         *odmTLSModel         `tfsdk:"tls"`
	Azure       *odmAzureModel       `tfsdk:"azure"`
	GCS         *odmGCSModel         `tfsdk:"gcs"`
}

type odmCredentialsModel struct {
	AccessKey    types.String `tfsdk:"access_key"`
	SecretKey    types.String `tfsdk:"secret_key"`
	SessionToken types.String `tfsdk:"session_token"`
}

type odmTLSModel struct {
	SkipVerify types.Bool   `tfsdk:"skip_verify"`
	CACertPEM  types.String `tfsdk:"ca_cert_pem"`
}

type odmAzureModel struct {
	Account    types.String `tfsdk:"account"`
	AccountKey types.String `tfsdk:"account_key"`
	SASToken   types.String `tfsdk:"sas_token"`
}

type odmGCSModel struct {
	ServiceAccountJSON types.String `tfsdk:"service_account_json"`
}

type odmFilterModel struct {
	Prefix       types.String `tfsdk:"prefix"`
	SourcePrefix types.String `tfsdk:"source_prefix"`
}

type odmPolicyModel struct {
	Head                      types.String           `tfsdk:"head"`
	RangeGet                  types.String           `tfsdk:"range_get"`
	SourceError               types.String           `tfsdk:"source_error"`
	ListThrough               types.Bool             `tfsdk:"list_through"`
	RespectLocalDeleteMarker  types.Bool             `tfsdk:"respect_local_delete_marker"`
	PreserveETag              types.Bool             `tfsdk:"preserve_etag"`
	CopyTags                  types.Bool             `tfsdk:"copy_tags"`
	EmitEvents                types.Bool             `tfsdk:"emit_events"`
	NegativeCacheTTLSecs      types.Int64            `tfsdk:"negative_cache_ttl_secs"`
	InlineMaxBytes            types.Int64            `tfsdk:"inline_max_bytes"`
	MultipartPartSizeBytes    types.Int64            `tfsdk:"multipart_part_size_bytes"`
	MaxConcurrentPulls        types.Int64            `tfsdk:"max_concurrent_pulls"`
	PullQueueCapacity         types.Int64            `tfsdk:"pull_queue_capacity"`
	SourceTimeout             *odmSourceTimeoutModel `tfsdk:"source_timeout"`
	BandwidthLimitBytesPerSec types.Int64            `tfsdk:"bandwidth_limit_bytes_per_sec"`
}

type odmSourceTimeoutModel struct {
	ConnectMS   types.Int64 `tfsdk:"connect_ms"`
	FirstByteMS types.Int64 `tfsdk:"first_byte_ms"`
	IdleMS      types.Int64 `tfsdk:"idle_ms"`
}

func (r *bucketOnDemandMigrationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_on_demand_migration"
}

func (r *bucketOnDemandMigrationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a RustFS bucket's on-demand migration source",
		MarkdownDescription: "Attach an external S3-compatible **source bucket** to a local RustFS bucket and pull " +
			"objects lazily on first read (`PUT`/`GET`/`DELETE /rustfs/admin/v3/on-demand-migration/{bucket}`).\n\n" +
			"### Create probes the source\n\n" +
			"Every create and update first sends the configuration with `?dry-run=true`: RustFS validates the " +
			"configuration, probes the source (`HeadBucket` plus a one-key listing) and writes nothing. Only after " +
			"that succeeds does the provider save the configuration, so an unreachable source or a wrong key never " +
			"reaches bucket metadata.\n\n" +
			"### Secrets\n\n" +
			"The configuration read is redacted: `secret_key`, `session_token`, the Azure `account_key`/`sas_token` " +
			"and the GCS service-account JSON come back as `REDACTED`. The provider keeps the configured values in " +
			"state while the server reports them redacted, so a refresh produces no diff. Import sets only the " +
			"bucket, so an imported resource has no source secret in state until the next apply supplies it; the " +
			"server never returns the value.\n\n" +
			"### Prerequisites\n\n" +
			"The `RUSTFS_ON_DEMAND_MIGRATION_ENABLED` module switch must be on on every node, and the deployment " +
			"needs a RustFS license that covers on-demand migration. A `gcs_native` source additionally needs a " +
			"server built with the `gcs` feature. The admin routes exist in every supported release (1.0.1+).\n\n" +
			"Write-back needs namespace locking enabled and exactly one pool with one erasure set. Other " +
			"topologies can still read through the source, but a pull is refused as `unsupported`; the provider " +
			"does not retry it.\n\n" +
			"### Destroy\n\n" +
			"`Delete` clears the configuration idempotently. Already-pulled objects stay in the bucket. Backfill is " +
			"**not** managed by this resource: `POST /rustfs/admin/v3/on-demand-migration/{bucket}/backfill?op=start|cancel` " +
			"is a job whose `completed_with_failures` state has no Terraform equivalent.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The bucket name, mirrored for import compatibility.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the local bucket that receives the configuration. Changing it forces recreation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enabled": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				Description: "Whether source traffic is enabled. `false` keeps the source and credentials on " +
					"record while the runtime stops consulting the source. Defaults to `true`.",
			},
			"version": schema.Int64Attribute{
				Computed:    true,
				Description: "Wire version of the persisted configuration. RustFS only accepts `1`.",
			},
			"source": schema.SingleNestedAttribute{
				Required:    true,
				Description: "The external source bucket.",
				Attributes: map[string]schema.Attribute{
					"provider": schema.StringAttribute{
						Required: true,
						Description: "Source family: `s3`, `aws`, `minio`, `rustfs`, `r2`, `gcs`, `azure` or " +
							"`gcs_native`. Every value except `azure` and `gcs_native` speaks S3.",
						Validators: []validator.String{
							stringvalidator.OneOf(odmProviderValues...),
						},
					},
					"endpoint": schema.StringAttribute{
						Optional: true,
						Description: "`http(s)://host[:port]` of the source, without path, query, fragment or " +
							"userinfo. Required except for `aws` (derived from `region`), `azure` (derived from " +
							"the account) and `gcs_native` (fixed Google endpoint).",
					},
					"region": schema.StringAttribute{
						Required:    true,
						Description: "Signing region. `auto` is accepted for `r2`, `minio`, `rustfs`, `azure` and `gcs_native`; other providers need a real region.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
					"bucket": schema.StringAttribute{
						Required:    true,
						Description: "Source bucket name (Azure container name for `azure`). No `/` or whitespace.",
						Validators: []validator.String{
							stringvalidator.RegexMatches(odmSourceBucketPattern, "must not contain '/' or whitespace"),
						},
					},
					"path_style": schema.StringAttribute{
						Optional: true,
						Computed: true,
						Default:  stringdefault.StaticString("auto"),
						Description: "Bucket addressing style: `auto`, `path` or `virtual`. `auto` resolves to " +
							"path-style for IP-literal or localhost endpoints and for `s3`/`minio`/`rustfs`.",
						Validators: []validator.String{
							stringvalidator.OneOf("auto", "path", "virtual"),
						},
					},
					"credentials": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Static source credentials. Required for every S3-speaking provider; anonymous sources are not supported by RustFS.",
						Attributes: map[string]schema.Attribute{
							"access_key": schema.StringAttribute{
								Required:    true,
								Sensitive:   true,
								Description: "Source access key id.",
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
							"secret_key": schema.StringAttribute{
								Required:    true,
								Sensitive:   true,
								Description: "Source secret access key. The server returns it redacted; the configured value stays in state.",
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
							"session_token": schema.StringAttribute{
								Optional:    true,
								Sensitive:   true,
								Description: "Optional STS session token. The server returns it redacted; the configured value stays in state.",
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
						},
					},
					"tls": schema.SingleNestedAttribute{
						Optional:    true,
						Computed:    true,
						Description: "TLS settings for the outbound source connection.",
						Attributes: map[string]schema.Attribute{
							"skip_verify": schema.BoolAttribute{
								Optional:    true,
								Computed:    true,
								Default:     booldefault.StaticBool(false),
								Description: "Disable certificate verification for the source connection. Defaults to `false`.",
							},
							"ca_cert_pem": schema.StringAttribute{
								Optional:    true,
								Description: "PEM CA certificate used to verify the source. Must contain `-----BEGIN CERTIFICATE-----`.",
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
						},
					},
					"azure": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Native Azure Blob parameters. Required for `azure` and rejected for every other provider.",
						Attributes: map[string]schema.Attribute{
							"account": schema.StringAttribute{
								Required:    true,
								Description: "Storage account name (letters, digits and hyphens).",
								Validators: []validator.String{
									stringvalidator.RegexMatches(odmAzureAccountPattern, "must contain only letters, digits or hyphens"),
								},
							},
							"account_key": schema.StringAttribute{
								Optional:    true,
								Sensitive:   true,
								Description: "Base64 storage-account key. Mutually exclusive with `sas_token`.",
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
							"sas_token": schema.StringAttribute{
								Optional:    true,
								Sensitive:   true,
								Description: "SAS query string without the leading `?` and without whitespace. Mutually exclusive with `account_key`.",
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
						},
					},
					"gcs": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Native Google Cloud Storage parameters. Required for `gcs_native` and rejected for every other provider.",
						Attributes: map[string]schema.Attribute{
							"service_account_json": schema.StringAttribute{
								Required:    true,
								Sensitive:   true,
								Description: "Service-account key JSON (`type: service_account`). The server returns it redacted; the configured value stays in state.",
							},
						},
					},
				},
			},
			"filter": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Key filters. `prefix` restricts which local keys consult the source; `source_prefix` is prepended to the local key.",
				Attributes: map[string]schema.Attribute{
					"prefix": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Only local keys with this prefix consult the source. Null or non-empty.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
					"source_prefix": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Prefix prepended to the local key to form the source key. Null or non-empty.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
				},
			},
			"policy": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Read-path policy. Every field defaults to the documented server value.",
				Attributes:  odmPolicySchemaAttributes(),
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "RFC 3339 time the configuration was last saved, as reported by the server.",
			},
		},
	}
}

func odmPolicySchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"head": schema.StringAttribute{
			Optional:    true,
			Computed:    true,
			Default:     stringdefault.StaticString("proxy"),
			Description: "HEAD-miss handling: `proxy` consults the source; `local_only` answers 404 without source traffic. Defaults to `proxy`.",
			Validators: []validator.String{
				stringvalidator.OneOf("proxy", "local_only"),
			},
		},
		"range_get": schema.StringAttribute{
			Optional:    true,
			Computed:    true,
			Default:     stringdefault.StaticString("serve_and_backfill"),
			Description: "Range-GET handling: `serve_and_backfill` also queues a whole-object pull; `serve_only` queues nothing. Defaults to `serve_and_backfill`.",
			Validators: []validator.String{
				stringvalidator.OneOf("serve_and_backfill", "serve_only"),
			},
		},
		"source_error": schema.StringAttribute{
			Optional:    true,
			Computed:    true,
			Default:     stringdefault.StaticString("propagate"),
			Description: "Source failure handling: `propagate` answers 424 SourceUnavailable; `not_found` degrades to 404. Defaults to `propagate`.",
			Validators: []validator.String{
				stringvalidator.OneOf("propagate", "not_found"),
			},
		},
		"list_through": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(false),
			Description: "Merge the source listing into ListObjectsV2 so clients see the whole namespace during migration. Defaults to `false`.",
		},
		"respect_local_delete_marker": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(true),
			Description: "Treat a local delete marker as the final answer. Defaults to `true`.",
		},
		"preserve_etag": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(true),
			Description: "Keep the source ETag on the stored object unless the bucket encrypts by default. Defaults to `true`.",
		},
		"copy_tags": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(false),
			Description: "Copy source object tags. Costs one extra source call per inline pull. Defaults to `false`.",
		},
		"emit_events": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(true),
			Description: "Emit ObjectCreated notifications for write-backs. Defaults to `true`.",
		},
		"negative_cache_ttl_secs": schema.Int64Attribute{
			Optional:    true,
			Computed:    true,
			Default:     int64default.StaticInt64(30),
			Description: "Seconds a source 404 is negative-cached; `0` disables the cache. Range 0..3600. Defaults to `30`.",
			Validators: []validator.Int64{
				int64validator.Between(0, 3600),
			},
		},
		"inline_max_bytes": schema.Int64Attribute{
			Optional:    true,
			Computed:    true,
			Default:     int64default.StaticInt64(16 * 1024 * 1024),
			Description: "Objects at or below this size are teed inline on a GET miss; larger objects stream through and queue a background pull. Range 0..268435456. Defaults to 16 MiB.",
			Validators: []validator.Int64{
				int64validator.Between(0, 256*1024*1024),
			},
		},
		"multipart_part_size_bytes": schema.Int64Attribute{
			Optional:    true,
			Computed:    true,
			Default:     int64default.StaticInt64(64 * 1024 * 1024),
			Description: "Part size for multipart write-back. Range 5242880..5368709120. Defaults to 64 MiB.",
			Validators: []validator.Int64{
				int64validator.Between(5*1024*1024, 5*1024*1024*1024),
			},
		},
		"max_concurrent_pulls": schema.Int64Attribute{
			Optional:    true,
			Computed:    true,
			Default:     int64default.StaticInt64(8),
			Description: "Maximum concurrent source pulls, shared by inline and background paths. Range 1..256. Defaults to `8`.",
			Validators: []validator.Int64{
				int64validator.Between(1, 256),
			},
		},
		"pull_queue_capacity": schema.Int64Attribute{
			Optional:    true,
			Computed:    true,
			Default:     int64default.StaticInt64(1024),
			Description: "Background pull queue capacity; a full queue drops the background job, never the client response. Range 1..65536. Defaults to `1024`.",
			Validators: []validator.Int64{
				int64validator.Between(1, 65536),
			},
		},
		"source_timeout": schema.SingleNestedAttribute{
			Optional:    true,
			Computed:    true,
			Description: "Outbound source timeout budget in milliseconds.",
			Attributes: map[string]schema.Attribute{
				"connect_ms": schema.Int64Attribute{
					Optional:    true,
					Computed:    true,
					Default:     int64default.StaticInt64(5000),
					Description: "Connection timeout. Range 100..600000. Defaults to `5000`.",
					Validators: []validator.Int64{
						int64validator.Between(100, 600000),
					},
				},
				"first_byte_ms": schema.Int64Attribute{
					Optional:    true,
					Computed:    true,
					Default:     int64default.StaticInt64(15000),
					Description: "First-byte timeout, also the singleflight follower window. Range 100..600000. Defaults to `15000`.",
					Validators: []validator.Int64{
						int64validator.Between(100, 600000),
					},
				},
				"idle_ms": schema.Int64Attribute{
					Optional:    true,
					Computed:    true,
					Default:     int64default.StaticInt64(30000),
					Description: "Per-body-chunk idle timeout. Range 100..600000. Defaults to `30000`.",
					Validators: []validator.Int64{
						int64validator.Between(100, 600000),
					},
				},
			},
		},
		"bandwidth_limit_bytes_per_sec": schema.Int64Attribute{
			Optional:    true,
			Description: "Optional outbound bandwidth cap for the source connection, at least 65536 bytes per second. Null means unlimited.",
			Validators: []validator.Int64{
				int64validator.AtLeast(65536),
			},
		},
	}
}

func (r *bucketOnDemandMigrationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.client = client
}

// ValidateConfig rejects the shapes the server would refuse only after a probe:
// a missing endpoint, credentials on a native provider, a provider block on the
// wrong provider, an out-of-contract region, or a malformed endpoint. Every
// error names the attribute to fix.
func (r *bucketOnDemandMigrationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config bucketOnDemandMigrationResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Source == nil || config.Source.Provider.IsNull() || config.Source.Provider.IsUnknown() {
		return
	}

	source := config.Source
	provider := source.Provider.ValueString()

	if provider != "azure" && source.Azure != nil {
		resp.Diagnostics.AddAttributeError(path.Root("source").AtName("azure"), "Invalid source block",
			"source.azure is only valid when source.provider is \"azure\".")
	}
	if provider != "gcs_native" && source.GCS != nil {
		resp.Diagnostics.AddAttributeError(path.Root("source").AtName("gcs"), "Invalid source block",
			"source.gcs is only valid when source.provider is \"gcs_native\".")
	}

	native := provider == "azure" || provider == "gcs_native"
	if native && source.Credentials != nil {
		resp.Diagnostics.AddAttributeError(path.Root("source").AtName("credentials"), "Invalid source credentials",
			"source.credentials must be omitted for provider "+provider+"; native backends carry their own credentials in the provider block.")
	}
	if !native && source.Credentials == nil {
		resp.Diagnostics.AddAttributeError(path.Root("source").AtName("credentials"), "Missing source credentials",
			"source.credentials is required for provider "+provider+"; RustFS does not support anonymous sources.")
	}

	switch provider {
	case "azure":
		if source.Azure == nil {
			resp.Diagnostics.AddAttributeError(path.Root("source").AtName("azure"), "Missing source block",
				"source.azure is required when source.provider is \"azure\".")
		} else if !source.Azure.Account.IsNull() && !source.Azure.Account.IsUnknown() && !odmAzureAccountPattern.MatchString(source.Azure.Account.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root("source").AtName("azure").AtName("account"), "Invalid Azure account",
				"source.azure.account must contain only letters, digits or hyphens.")
		}
		if source.Azure != nil {
			hasKey := !source.Azure.AccountKey.IsNull() && !source.Azure.AccountKey.IsUnknown()
			hasSAS := !source.Azure.SASToken.IsNull() && !source.Azure.SASToken.IsUnknown()
			if hasKey == hasSAS {
				resp.Diagnostics.AddAttributeError(path.Root("source").AtName("azure"), "Invalid Azure credentials",
					"Exactly one of source.azure.account_key and source.azure.sas_token is required.")
			}
			if hasSAS {
				sas := source.Azure.SASToken.ValueString()
				if strings.HasPrefix(sas, "?") || strings.ContainsAny(sas, " \t\r\n") {
					resp.Diagnostics.AddAttributeError(path.Root("source").AtName("azure").AtName("sas_token"), "Invalid SAS token",
						"source.azure.sas_token must not start with '?' or contain whitespace.")
				}
			}
		}
	case "gcs_native":
		if source.GCS == nil {
			resp.Diagnostics.AddAttributeError(path.Root("source").AtName("gcs"), "Missing source block",
				"source.gcs is required when source.provider is \"gcs_native\".")
		} else if !source.GCS.ServiceAccountJSON.IsNull() && !source.GCS.ServiceAccountJSON.IsUnknown() {
			if err := validateODMServiceAccountJSON(source.GCS.ServiceAccountJSON.ValueString()); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("source").AtName("gcs").AtName("service_account_json"), "Invalid service-account JSON", err.Error())
			}
		}
	}

	// endpoint may only be derived for aws, azure and gcs_native.
	if source.Endpoint.IsNull() || source.Endpoint.IsUnknown() {
		if provider != "aws" && provider != "azure" && provider != "gcs_native" {
			resp.Diagnostics.AddAttributeError(path.Root("source").AtName("endpoint"), "Missing source endpoint",
				"source.endpoint is required for provider "+provider+".")
		}
	} else if err := validateODMEndpoint(source.Endpoint.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("source").AtName("endpoint"), "Invalid source endpoint", err.Error())
	}

	if !source.Region.IsNull() && !source.Region.IsUnknown() {
		region := source.Region.ValueString()
		if region == "auto" && !odmProviderAcceptsAutoRegion(provider) {
			resp.Diagnostics.AddAttributeError(path.Root("source").AtName("region"), "Invalid source region",
				"source.region \"auto\" is only accepted for r2, minio, rustfs, azure and gcs_native; provider "+provider+" needs a real region.")
		}
		if provider == "aws" && (source.Endpoint.IsNull() || source.Endpoint.IsUnknown()) && !odmAWSRegionPattern.MatchString(region) {
			resp.Diagnostics.AddAttributeError(path.Root("source").AtName("region"), "Invalid source region",
				"provider \"aws\" derives its endpoint from the region, so source.region may contain only letters, digits or hyphens.")
		}
	}
}

func odmProviderAcceptsAutoRegion(provider string) bool {
	switch provider {
	case "r2", "minio", "rustfs", "azure", "gcs_native":
		return true
	default:
		return false
	}
}

func validateODMEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("source.endpoint must be an absolute URL such as https://source.example.com:9000; got %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("source.endpoint scheme must be http or https; got %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("source.endpoint has no host: %q", raw)
	}
	if parsed.User != nil {
		return errors.New("source.endpoint must not embed credentials")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return errors.New("source.endpoint must not have a path")
	}
	if parsed.RawQuery != "" {
		return errors.New("source.endpoint must not have a query")
	}
	if parsed.Fragment != "" {
		return errors.New("source.endpoint must not have a fragment")
	}
	return nil
}

func validateODMServiceAccountJSON(raw string) error {
	var key map[string]any
	if err := json.Unmarshal([]byte(raw), &key); err != nil {
		return errors.New("source.gcs.service_account_json is not valid JSON")
	}
	if key["type"] != "service_account" {
		return errors.New("source.gcs.service_account_json must be a service_account key")
	}
	for _, field := range []string{"client_email", "private_key"} {
		value, ok := key[field].(string)
		if !ok || value == "" {
			return fmt.Errorf("source.gcs.service_account_json is missing %s", field)
		}
	}
	return nil
}

func (r *bucketOnDemandMigrationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketOnDemandMigrationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.upsert(ctx, &plan, "create", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	tflog.Trace(ctx, "created a bucket on-demand migration resource", map[string]any{"bucket": plan.Bucket.ValueString()})
}

func (r *bucketOnDemandMigrationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketOnDemandMigrationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := state.Bucket.ValueString()
	result, err := r.client.RustClient.GetBucketOnDemandMigration(ctx, bucket)
	if err != nil {
		if odmConfigurationGone(err) {
			tflog.Trace(ctx, "bucket on-demand migration is no longer configured", map[string]any{"bucket": bucket})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket on-demand migration",
			"Could not read on-demand migration for bucket "+bucket+": "+odmErrorText(err),
		)
		return
	}

	applyODMConfig(&state, result.Config, result.UpdatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketOnDemandMigrationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketOnDemandMigrationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.upsert(ctx, &plan, "update", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	tflog.Trace(ctx, "updated a bucket on-demand migration resource", map[string]any{"bucket": plan.Bucket.ValueString()})
}

// upsert runs the dry-run probe first, saves only when it succeeds, then reads
// the persisted configuration back so state carries the server's stored values.
func (r *bucketOnDemandMigrationResource) upsert(ctx context.Context, plan *bucketOnDemandMigrationResourceModel, action string, diags *diag.Diagnostics) {
	bucket := plan.Bucket.ValueString()
	config := odmConfigFromModel(plan)
	summary := "Error creating bucket on-demand migration"
	if action == "update" {
		summary = "Error updating bucket on-demand migration"
	}

	if _, err := r.client.RustClient.SetBucketOnDemandMigration(ctx, bucket, config, true); err != nil {
		diags.AddError(
			summary,
			"Could not "+action+" on-demand migration for bucket "+bucket+": "+odmErrorText(err)+
				" The dry-run request failed, so nothing was saved.",
		)
		return
	}

	if _, err := r.client.RustClient.SetBucketOnDemandMigration(ctx, bucket, config, false); err != nil {
		diags.AddError(
			summary,
			"Could not "+action+" on-demand migration for bucket "+bucket+": "+odmErrorText(err),
		)
		return
	}

	result, err := r.client.RustClient.GetBucketOnDemandMigration(ctx, bucket)
	if err != nil {
		diags.AddError(
			summary,
			"On-demand migration for bucket "+bucket+" was saved, but reading it back failed: "+odmErrorText(err),
		)
		return
	}
	applyODMConfig(plan, result.Config, result.UpdatedAt)
}

func (r *bucketOnDemandMigrationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketOnDemandMigrationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := state.Bucket.ValueString()
	if err := r.client.RustClient.DeleteBucketOnDemandMigration(ctx, bucket); err != nil {
		if odmConfigurationGone(err) {
			tflog.Trace(ctx, "bucket on-demand migration was already cleared", map[string]any{"bucket": bucket})
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket on-demand migration",
			"Could not delete on-demand migration for bucket "+bucket+": "+odmErrorText(err),
		)
		return
	}

	tflog.Trace(ctx, "deleted a bucket on-demand migration resource", map[string]any{"bucket": bucket})
}

func (r *bucketOnDemandMigrationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

// odmConfigFromModel maps the plan to the wire configuration, substituting the
// documented defaults for omitted computed values so an update never silently
// falls back to a different server default.
func odmConfigFromModel(model *bucketOnDemandMigrationResourceModel) rustfs.ODMConfig {
	config := rustfs.ODMConfig{
		Version: 1,
		Enabled: boolOr(model.Enabled, true),
		Filter: rustfs.ODMFilter{
			Prefix:       stringPointer(model.FilterPrefix()),
			SourcePrefix: stringPointer(model.FilterSourcePrefix()),
		},
		Policy: rustfs.ODMPolicy{
			Head:                     stringOr(model.PolicyHead(), "proxy"),
			RangeGet:                 stringOr(model.PolicyRangeGet(), "serve_and_backfill"),
			SourceError:              stringOr(model.PolicySourceError(), "propagate"),
			ListThrough:              boolOr(model.PolicyListThrough(), false),
			RespectLocalDeleteMarker: boolOr(model.PolicyRespectLocalDeleteMarker(), true),
			PreserveETag:             boolOr(model.PolicyPreserveETag(), true),
			CopyTags:                 boolOr(model.PolicyCopyTags(), false),
			EmitEvents:               boolOr(model.PolicyEmitEvents(), true),
			NegativeCacheTTLSecs:     uint64Or(model.PolicyNegativeCacheTTLSecs(), 30),
			InlineMaxBytes:           uint64Or(model.PolicyInlineMaxBytes(), 16*1024*1024),
			MultipartPartSizeBytes:   uint64Or(model.PolicyMultipartPartSizeBytes(), 64*1024*1024),
			MaxConcurrentPulls:       uint32Or(model.PolicyMaxConcurrentPulls(), 8),
			PullQueueCapacity:        uint32Or(model.PolicyPullQueueCapacity(), 1024),
			SourceTimeout: rustfs.ODMSourceTimeout{
				ConnectMS:   uint64Or(model.PolicyConnectMS(), 5000),
				FirstByteMS: uint64Or(model.PolicyFirstByteMS(), 15000),
				IdleMS:      uint64Or(model.PolicyIdleMS(), 30000),
			},
			BandwidthLimitBytesPerSec: uint64PointerOrNil(model.PolicyBandwidthLimit()),
		},
	}

	if model.Source != nil {
		source := model.Source
		config.Source = rustfs.ODMSource{
			Provider:  stringOr(source.Provider, ""),
			Endpoint:  stringPointer(source.Endpoint),
			Region:    stringOr(source.Region, ""),
			Bucket:    stringOr(source.Bucket, ""),
			PathStyle: stringOr(source.PathStyle, "auto"),
			TLS: rustfs.ODMTLS{
				SkipVerify: boolOr(source.TLSSkipVerify(), false),
				CACertPEM:  stringPointer(source.TLSCACertPEM()),
			},
		}
		if source.Credentials != nil {
			config.Source.Credentials = &rustfs.ODMCredentials{
				AccessKey:    stringOr(source.Credentials.AccessKey, ""),
				SecretKey:    stringOr(source.Credentials.SecretKey, ""),
				SessionToken: stringPointer(source.Credentials.SessionToken),
			}
		}
		if source.Azure != nil {
			config.Source.Azure = &rustfs.ODMAzure{
				Account:    stringOr(source.Azure.Account, ""),
				AccountKey: stringPointer(source.Azure.AccountKey),
				SASToken:   stringPointer(source.Azure.SASToken),
			}
		}
		if source.GCS != nil {
			config.Source.GCS = &rustfs.ODMGCS{
				ServiceAccountJSON: stringOr(source.GCS.ServiceAccountJSON, ""),
			}
		}
	}

	return config
}

// applyODMConfig writes the server's (redacted) configuration back to the
// model, preserving every configured secret the server replaced with REDACTED.
// prior is the plan on create/update and the state on read.
func applyODMConfig(model *bucketOnDemandMigrationResourceModel, config rustfs.ODMConfig, updatedAt string) {
	model.Enabled = types.BoolValue(config.Enabled)
	model.Version = types.Int64Value(int64(config.Version))
	model.ID = types.StringValue(model.Bucket.ValueString())

	priorSource := model.Source
	priorCredentials := (*odmCredentialsModel)(nil)
	priorAzure := (*odmAzureModel)(nil)
	priorGCS := (*odmGCSModel)(nil)
	if priorSource != nil {
		priorCredentials = priorSource.Credentials
		priorAzure = priorSource.Azure
		priorGCS = priorSource.GCS
	}

	source := &odmSourceModel{
		Provider:  types.StringValue(config.Source.Provider),
		Endpoint:  stringValueOrNull(config.Source.Endpoint),
		Region:    types.StringValue(config.Source.Region),
		Bucket:    types.StringValue(config.Source.Bucket),
		PathStyle: types.StringValue(config.Source.PathStyle),
		TLS: &odmTLSModel{
			SkipVerify: types.BoolValue(config.Source.TLS.SkipVerify),
			CACertPEM:  stringValueOrNull(config.Source.TLS.CACertPEM),
		},
	}
	if config.Source.Credentials != nil {
		source.Credentials = &odmCredentialsModel{
			AccessKey:    types.StringValue(config.Source.Credentials.AccessKey),
			SecretKey:    preservedSecret(config.Source.Credentials.SecretKey, priorString(priorCredentials, true)),
			SessionToken: preservedOptionalSecret(config.Source.Credentials.SessionToken, priorOptionalString(priorCredentials, true)),
		}
	}
	if config.Source.Azure != nil {
		source.Azure = &odmAzureModel{
			Account:    types.StringValue(config.Source.Azure.Account),
			AccountKey: preservedOptionalSecret(config.Source.Azure.AccountKey, priorAzureKey(priorAzure)),
			SASToken:   preservedOptionalSecret(config.Source.Azure.SASToken, priorAzureSAS(priorAzure)),
		}
	}
	if config.Source.GCS != nil {
		source.GCS = &odmGCSModel{
			ServiceAccountJSON: preservedSecret(config.Source.GCS.ServiceAccountJSON, priorGCSJSON(priorGCS)),
		}
	}
	model.Source = source

	if model.Filter != nil || config.Filter.Prefix != nil || config.Filter.SourcePrefix != nil {
		model.Filter = &odmFilterModel{
			Prefix:       stringValueOrNull(config.Filter.Prefix),
			SourcePrefix: stringValueOrNull(config.Filter.SourcePrefix),
		}
	}

	model.Policy = &odmPolicyModel{
		Head:                     types.StringValue(config.Policy.Head),
		RangeGet:                 types.StringValue(config.Policy.RangeGet),
		SourceError:              types.StringValue(config.Policy.SourceError),
		ListThrough:              types.BoolValue(config.Policy.ListThrough),
		RespectLocalDeleteMarker: types.BoolValue(config.Policy.RespectLocalDeleteMarker),
		PreserveETag:             types.BoolValue(config.Policy.PreserveETag),
		CopyTags:                 types.BoolValue(config.Policy.CopyTags),
		EmitEvents:               types.BoolValue(config.Policy.EmitEvents),
		NegativeCacheTTLSecs:     types.Int64Value(int64(config.Policy.NegativeCacheTTLSecs)),
		InlineMaxBytes:           types.Int64Value(int64(config.Policy.InlineMaxBytes)),
		MultipartPartSizeBytes:   types.Int64Value(int64(config.Policy.MultipartPartSizeBytes)),
		MaxConcurrentPulls:       types.Int64Value(int64(config.Policy.MaxConcurrentPulls)),
		PullQueueCapacity:        types.Int64Value(int64(config.Policy.PullQueueCapacity)),
		SourceTimeout: &odmSourceTimeoutModel{
			ConnectMS:   types.Int64Value(int64(config.Policy.SourceTimeout.ConnectMS)),
			FirstByteMS: types.Int64Value(int64(config.Policy.SourceTimeout.FirstByteMS)),
			IdleMS:      types.Int64Value(int64(config.Policy.SourceTimeout.IdleMS)),
		},
		BandwidthLimitBytesPerSec: int64ValueOrNull(config.Policy.BandwidthLimitBytesPerSec),
	}

	if updatedAt == "" {
		model.UpdatedAt = types.StringNull()
	} else {
		model.UpdatedAt = types.StringValue(updatedAt)
	}
}

func preservedSecret(remote string, prior types.String) types.String {
	if remote == rustfs.RedactedSecret && !prior.IsNull() && !prior.IsUnknown() {
		return prior
	}
	if remote == rustfs.RedactedSecret {
		return types.StringNull()
	}
	return types.StringValue(remote)
}

func preservedOptionalSecret(remote *string, prior types.String) types.String {
	if remote == nil {
		return types.StringNull()
	}
	return preservedSecret(*remote, prior)
}

// odmConfigurationGone reports a refresh/delete for a configuration or bucket
// that no longer exists. Route-level 404s are already converted to
// UnsupportedFeatureError by the client and stay errors.
func odmConfigurationGone(err error) bool {
	if rustfs.IsUnsupportedFeature(err) {
		return false
	}
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == "NoSuchConfiguration" ||
		apiErr.Code == "NoSuchBucket" ||
		(apiErr.Code == "" && apiErr.StatusCode == 404)
}

// odmErrorText turns the server's ODM-specific error classes into an
// actionable diagnostic instead of a raw payload.
func odmErrorText(err error) string {
	if rustfs.IsUnsupportedFeature(err) {
		return err.Error()
	}

	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return err.Error()
	}

	switch apiErr.Code {
	case "OnDemandMigrationDisabled":
		return "the RustFS on-demand migration module is disabled; set RUSTFS_ON_DEMAND_MIGRATION_ENABLED=true on every node, restart, and retry"
	case "AccessDenied":
		return "the server denied the request: on-demand migration requires a RustFS license that covers it, and the admin credentials need the matching admin:SetBucketOnDemandMigration or admin:GetBucketOnDemandMigration action. Server said: " + apiErr.Message
	case "OnDemandMigrationBackendNotCompiled":
		return "the server was built without the requested source backend; rebuild with the gcs feature for a gcs_native source. Server said: " + apiErr.Message
	case "OnDemandMigrationSourceUnreachable":
		class := apiErr.Message
		if at := strings.LastIndex(class, ": "); at >= 0 {
			class = class[at+2:]
		}
		return "the source bucket probe failed (" + class + "); check source.endpoint, source.region, source.bucket, the credentials and the TLS settings"
	case "NoSuchConfiguration":
		return "on-demand migration is not configured for this bucket"
	case "NoSuchBucket":
		return "the bucket does not exist"
	}

	if apiErr.Message != "" {
		return apiErr.Code + ": " + apiErr.Message
	}
	return err.Error()
}

func stringOr(value types.String, fallback string) string {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueString()
}

func boolOr(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

func uint64Or(value types.Int64, fallback uint64) uint64 {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return uint64(value.ValueInt64())
}

func uint32Or(value types.Int64, fallback uint32) uint32 {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return uint32(value.ValueInt64())
}

func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	text := value.ValueString()
	return &text
}

func uint64PointerOrNil(value types.Int64) *uint64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	number := uint64(value.ValueInt64())
	return &number
}

func stringValueOrNull(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func int64ValueOrNull(value *uint64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

func priorString(model *odmCredentialsModel, secret bool) types.String {
	if model == nil {
		return types.StringNull()
	}
	if secret {
		return model.SecretKey
	}
	return model.AccessKey
}

func priorOptionalString(model *odmCredentialsModel, sessionToken bool) types.String {
	if model == nil {
		return types.StringNull()
	}
	if sessionToken {
		return model.SessionToken
	}
	return types.StringNull()
}

func priorAzureKey(model *odmAzureModel) types.String {
	if model == nil {
		return types.StringNull()
	}
	return model.AccountKey
}

func priorAzureSAS(model *odmAzureModel) types.String {
	if model == nil {
		return types.StringNull()
	}
	return model.SASToken
}

func priorGCSJSON(model *odmGCSModel) types.String {
	if model == nil {
		return types.StringNull()
	}
	return model.ServiceAccountJSON
}

func (m *bucketOnDemandMigrationResourceModel) FilterPrefix() types.String {
	if m.Filter == nil {
		return types.StringNull()
	}
	return m.Filter.Prefix
}

func (m *bucketOnDemandMigrationResourceModel) FilterSourcePrefix() types.String {
	if m.Filter == nil {
		return types.StringNull()
	}
	return m.Filter.SourcePrefix
}

func (m *bucketOnDemandMigrationResourceModel) PolicyHead() types.String {
	if m.Policy == nil {
		return types.StringNull()
	}
	return m.Policy.Head
}

func (m *bucketOnDemandMigrationResourceModel) PolicyRangeGet() types.String {
	if m.Policy == nil {
		return types.StringNull()
	}
	return m.Policy.RangeGet
}

func (m *bucketOnDemandMigrationResourceModel) PolicySourceError() types.String {
	if m.Policy == nil {
		return types.StringNull()
	}
	return m.Policy.SourceError
}

func (m *bucketOnDemandMigrationResourceModel) PolicyListThrough() types.Bool {
	if m.Policy == nil {
		return types.BoolNull()
	}
	return m.Policy.ListThrough
}

func (m *bucketOnDemandMigrationResourceModel) PolicyRespectLocalDeleteMarker() types.Bool {
	if m.Policy == nil {
		return types.BoolNull()
	}
	return m.Policy.RespectLocalDeleteMarker
}

func (m *bucketOnDemandMigrationResourceModel) PolicyPreserveETag() types.Bool {
	if m.Policy == nil {
		return types.BoolNull()
	}
	return m.Policy.PreserveETag
}

func (m *bucketOnDemandMigrationResourceModel) PolicyCopyTags() types.Bool {
	if m.Policy == nil {
		return types.BoolNull()
	}
	return m.Policy.CopyTags
}

func (m *bucketOnDemandMigrationResourceModel) PolicyEmitEvents() types.Bool {
	if m.Policy == nil {
		return types.BoolNull()
	}
	return m.Policy.EmitEvents
}

func (m *bucketOnDemandMigrationResourceModel) PolicyNegativeCacheTTLSecs() types.Int64 {
	if m.Policy == nil {
		return types.Int64Null()
	}
	return m.Policy.NegativeCacheTTLSecs
}

func (m *bucketOnDemandMigrationResourceModel) PolicyInlineMaxBytes() types.Int64 {
	if m.Policy == nil {
		return types.Int64Null()
	}
	return m.Policy.InlineMaxBytes
}

func (m *bucketOnDemandMigrationResourceModel) PolicyMultipartPartSizeBytes() types.Int64 {
	if m.Policy == nil {
		return types.Int64Null()
	}
	return m.Policy.MultipartPartSizeBytes
}

func (m *bucketOnDemandMigrationResourceModel) PolicyMaxConcurrentPulls() types.Int64 {
	if m.Policy == nil {
		return types.Int64Null()
	}
	return m.Policy.MaxConcurrentPulls
}

func (m *bucketOnDemandMigrationResourceModel) PolicyPullQueueCapacity() types.Int64 {
	if m.Policy == nil {
		return types.Int64Null()
	}
	return m.Policy.PullQueueCapacity
}

func (m *bucketOnDemandMigrationResourceModel) PolicyConnectMS() types.Int64 {
	if m.Policy == nil || m.Policy.SourceTimeout == nil {
		return types.Int64Null()
	}
	return m.Policy.SourceTimeout.ConnectMS
}

func (m *bucketOnDemandMigrationResourceModel) PolicyFirstByteMS() types.Int64 {
	if m.Policy == nil || m.Policy.SourceTimeout == nil {
		return types.Int64Null()
	}
	return m.Policy.SourceTimeout.FirstByteMS
}

func (m *bucketOnDemandMigrationResourceModel) PolicyIdleMS() types.Int64 {
	if m.Policy == nil || m.Policy.SourceTimeout == nil {
		return types.Int64Null()
	}
	return m.Policy.SourceTimeout.IdleMS
}

func (m *bucketOnDemandMigrationResourceModel) PolicyBandwidthLimit() types.Int64 {
	if m.Policy == nil {
		return types.Int64Null()
	}
	return m.Policy.BandwidthLimitBytesPerSec
}

func (s *odmSourceModel) TLSSkipVerify() types.Bool {
	if s.TLS == nil {
		return types.BoolNull()
	}
	return s.TLS.SkipVerify
}

func (s *odmSourceModel) TLSCACertPEM() types.String {
	if s.TLS == nil {
		return types.StringNull()
	}
	return s.TLS.CACertPEM
}

func init() {
	registerResource(NewBucketOnDemandMigrationResource)
}
