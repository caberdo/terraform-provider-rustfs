package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// unsupported: ReplicationRule.Prefix is deprecated; use filter.prefix instead.
// unsupported: Destination.StorageClass directory-bucket-only values
// (FSX_OPENZFS, FSX_ONTAP, AWS_BACKUP_*) are not accepted by RustFS.
// All other members of PutBucketReplicationInput, ReplicationConfiguration,
// ReplicationRule and Destination are exposed.

var (
	_ resource.Resource                = &BucketReplicationResource{}
	_ resource.ResourceWithImportState = &BucketReplicationResource{}
)

type BucketReplicationResource struct {
	client *AllClient
}

type bucketReplicationResourceModel struct {
	Bucket              types.String                 `tfsdk:"bucket"`
	Role                types.String                 `tfsdk:"role"`
	Token               types.String                 `tfsdk:"token"`
	ChecksumAlgorithm   types.String                 `tfsdk:"checksum_algorithm"`
	ContentMD5          types.String                 `tfsdk:"content_md5"`
	ExpectedBucketOwner types.String                 `tfsdk:"expected_bucket_owner"`
	Rule                []bucketReplicationRuleModel `tfsdk:"rule"`
}

type bucketReplicationRuleModel struct {
	Id                        types.String                           `tfsdk:"id"`
	Priority                  types.Int64                            `tfsdk:"priority"`
	Status                    types.String                           `tfsdk:"status"`
	Filter                    *bucketReplicationFilterModel          `tfsdk:"filter"`
	DeleteMarkerReplication   *bucketReplicationStatusModel          `tfsdk:"delete_marker_replication"`
	ExistingObjectReplication *bucketReplicationStatusModel          `tfsdk:"existing_object_replication"`
	SourceSelectionCriteria   *bucketReplicationSourceSelectionModel `tfsdk:"source_selection_criteria"`
	Destination               *bucketReplicationDestinationModel     `tfsdk:"destination"`
}

type bucketReplicationFilterModel struct {
	Prefix types.String                       `tfsdk:"prefix"`
	Tag    *bucketReplicationTagModel         `tfsdk:"tag"`
	And    *bucketReplicationAndOperatorModel `tfsdk:"and"`
}

type bucketReplicationTagModel struct {
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
}

type bucketReplicationAndOperatorModel struct {
	Prefix types.String                `tfsdk:"prefix"`
	Tags   []bucketReplicationTagModel `tfsdk:"tags"`
}

type bucketReplicationStatusModel struct {
	Status types.String `tfsdk:"status"`
}

type bucketReplicationSourceSelectionModel struct {
	ReplicaModifications   *bucketReplicationStatusModel `tfsdk:"replica_modifications"`
	SseKmsEncryptedObjects *bucketReplicationStatusModel `tfsdk:"sse_kms_encrypted_objects"`
}

type bucketReplicationDestinationModel struct {
	Bucket                   types.String                                    `tfsdk:"bucket"`
	Account                  types.String                                    `tfsdk:"account"`
	StorageClass             types.String                                    `tfsdk:"storage_class"`
	AccessControlTranslation *bucketReplicationAccessControlTranslationModel `tfsdk:"access_control_translation"`
	EncryptionConfiguration  *bucketReplicationEncryptionConfigurationModel  `tfsdk:"encryption_configuration"`
	Metrics                  *bucketReplicationMetricsModel                  `tfsdk:"metrics"`
	ReplicationTime          *bucketReplicationTimeModel                     `tfsdk:"replication_time"`
}

type bucketReplicationAccessControlTranslationModel struct {
	Owner types.String `tfsdk:"owner"`
}

type bucketReplicationEncryptionConfigurationModel struct {
	ReplicaKmsKeyId types.String `tfsdk:"replica_kms_key_id"`
}

type bucketReplicationMetricsModel struct {
	Status         types.String                     `tfsdk:"status"`
	EventThreshold *bucketReplicationTimeValueModel `tfsdk:"event_threshold"`
}

type bucketReplicationTimeModel struct {
	Status types.String                     `tfsdk:"status"`
	Time   *bucketReplicationTimeValueModel `tfsdk:"time"`
}

type bucketReplicationTimeValueModel struct {
	Minutes types.Int64 `tfsdk:"minutes"`
}

func NewBucketReplicationResource() resource.Resource {
	return &BucketReplicationResource{}
}

func (r *BucketReplicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_replication"
}

func (r *BucketReplicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS bucket replication",
		MarkdownDescription: "Manage RustFS bucket replication configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:      true,
				Description:   "Name of the source bucket.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Required:    true,
				Description: "Amazon Resource Name (ARN) of the IAM role RustFS assumes when replicating objects.",
			},
			"token": schema.StringAttribute{
				Optional:    true,
				Description: "Token that allows Object Lock to be enabled for an existing bucket.",
			},
			"checksum_algorithm": schema.StringAttribute{
				Optional:    true,
				Description: "Checksum algorithm used to create the request checksum.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.ChecksumAlgorithm("").Values())...),
				},
			},
			"content_md5": schema.StringAttribute{
				Optional:    true,
				Description: "Base64 encoded 128-bit MD5 digest of the request body.",
			},
			"expected_bucket_owner": schema.StringAttribute{
				Optional:    true,
				Description: "Account ID of the expected bucket owner.",
			},
			"rule": schema.ListNestedAttribute{
				Required:    true,
				Description: "Replication rules. Each rule maps one-to-one to an S3 replication rule.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Unique identifier for the rule.",
						},
						"priority": schema.Int64Attribute{
							Optional:    true,
							Computed:    true,
							Description: "Priority of the rule; the higher the number, the higher the precedence.",
						},
						"status": schema.StringAttribute{
							Required:    true,
							Description: "Whether the rule is enabled.",
							Validators: []validator.String{
								stringvalidator.OneOf(enumStrings(s3types.ReplicationRuleStatus("").Values())...),
							},
						},
						"filter": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Filter that identifies the subset of objects to which the rule applies.",
							Attributes: map[string]schema.Attribute{
								"prefix": schema.StringAttribute{
									Optional:    true,
									Description: "Object key prefix that identifies the subset of objects to which the rule applies.",
								},
								"tag": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Tag that objects must carry for the rule to apply.",
									Attributes: map[string]schema.Attribute{
										"key": schema.StringAttribute{
											Required:    true,
											Description: "Tag key.",
										},
										"value": schema.StringAttribute{
											Required:    true,
											Description: "Tag value.",
										},
									},
								},
								"and": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Combined filter requiring a prefix and/or multiple tags to match.",
									Attributes: map[string]schema.Attribute{
										"prefix": schema.StringAttribute{
											Optional:    true,
											Description: "Object key prefix used in the combined filter.",
										},
										"tags": schema.ListNestedAttribute{
											Optional:    true,
											Description: "Tags all of which must match.",
											NestedObject: schema.NestedAttributeObject{
												Attributes: map[string]schema.Attribute{
													"key": schema.StringAttribute{
														Required:    true,
														Description: "Tag key.",
													},
													"value": schema.StringAttribute{
														Required:    true,
														Description: "Tag value.",
													},
												},
											},
										},
									},
								},
							},
						},
						"delete_marker_replication": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Specifies whether delete markers are replicated.",
							Attributes: map[string]schema.Attribute{
								"status": schema.StringAttribute{
									Required:    true,
									Description: "Whether delete marker replication is enabled.",
									Validators: []validator.String{
										stringvalidator.OneOf(enumStrings(s3types.DeleteMarkerReplicationStatus("").Values())...),
									},
								},
							},
						},
						"existing_object_replication": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Specifies whether existing source bucket objects are replicated.",
							Attributes: map[string]schema.Attribute{
								"status": schema.StringAttribute{
									Required:    true,
									Description: "Whether existing object replication is enabled.",
									Validators: []validator.String{
										stringvalidator.OneOf(enumStrings(s3types.ExistingObjectReplicationStatus("").Values())...),
									},
								},
							},
						},
						"source_selection_criteria": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Additional filters for identifying the source objects to replicate.",
							Attributes: map[string]schema.Attribute{
								"replica_modifications": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Specifies whether replica modifications are replicated.",
									Attributes: map[string]schema.Attribute{
										"status": schema.StringAttribute{
											Required:    true,
											Description: "Whether replica modification replication is enabled.",
											Validators: []validator.String{
												stringvalidator.OneOf(enumStrings(s3types.ReplicaModificationsStatus("").Values())...),
											},
										},
									},
								},
								"sse_kms_encrypted_objects": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Specifies whether SSE-KMS encrypted objects are replicated.",
									Attributes: map[string]schema.Attribute{
										"status": schema.StringAttribute{
											Required:    true,
											Description: "Whether replication of SSE-KMS encrypted objects is enabled.",
											Validators: []validator.String{
												stringvalidator.OneOf(enumStrings(s3types.SseKmsEncryptedObjectsStatus("").Values())...),
											},
										},
									},
								},
							},
						},
						"destination": schema.SingleNestedAttribute{
							Required:    true,
							Description: "Destination bucket and its replication settings.",
							Attributes: map[string]schema.Attribute{
								"bucket": schema.StringAttribute{
									Required:    true,
									Description: "ARN of the destination bucket.",
								},
								"account": schema.StringAttribute{
									Optional:    true,
									Description: "Account ID of the destination bucket owner in a cross-account scenario.",
								},
								"storage_class": schema.StringAttribute{
									Optional:    true,
									Description: "Storage class to use when replicating objects.",
									Validators: []validator.String{
										stringvalidator.OneOf(enumStrings(s3types.StorageClass("").Values())...),
									},
								},
								"access_control_translation": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Replica ownership translation settings.",
									Attributes: map[string]schema.Attribute{
										"owner": schema.StringAttribute{
											Required:    true,
											Description: "Specifies the replica ownership.",
											Validators: []validator.String{
												stringvalidator.OneOf(enumStrings(s3types.OwnerOverride("").Values())...),
											},
										},
									},
								},
								"encryption_configuration": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Encryption settings for replicas written to the destination bucket.",
									Attributes: map[string]schema.Attribute{
										"replica_kms_key_id": schema.StringAttribute{
											Optional:    true,
											Description: "ARN of the KMS key used to encrypt replica objects.",
										},
									},
								},
								"metrics": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Replication metrics settings.",
									Attributes: map[string]schema.Attribute{
										"status": schema.StringAttribute{
											Required:    true,
											Description: "Whether replication metrics are enabled.",
											Validators: []validator.String{
												stringvalidator.OneOf(enumStrings(s3types.MetricsStatus("").Values())...),
											},
										},
										"event_threshold": schema.SingleNestedAttribute{
											Optional:    true,
											Description: "Time threshold for emitting the replication missed-threshold event.",
											Attributes: map[string]schema.Attribute{
												"minutes": schema.Int64Attribute{
													Required:    true,
													Description: "Time in minutes; valid value is 15.",
												},
											},
										},
									},
								},
								"replication_time": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "S3 Replication Time Control (RTC) settings.",
									Attributes: map[string]schema.Attribute{
										"status": schema.StringAttribute{
											Required:    true,
											Description: "Whether S3 Replication Time Control is enabled.",
											Validators: []validator.String{
												stringvalidator.OneOf(enumStrings(s3types.ReplicationTimeStatus("").Values())...),
											},
										},
										"time": schema.SingleNestedAttribute{
											Required:    true,
											Description: "Time by which all objects must be replicated.",
											Attributes: map[string]schema.Attribute{
												"minutes": schema.Int64Attribute{
													Required:    true,
													Description: "Time in minutes; valid value is 15.",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *BucketReplicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *BucketReplicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketReplicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketReplication(ctx, buildPutReplicationInput(plan)); err != nil {
		resp.Diagnostics.AddError("Error setting bucket replication", "Could not set replication: "+err.Error())
		return
	}

	tflog.Trace(ctx, "created a bucket replication resource")

	state := plan
	if err := r.readState(ctx, &state); err != nil {
		tflog.Warn(ctx, "could not read back bucket replication after create; keeping planned values")
		normalizeReplicationUnknowns(&state)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketReplicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketReplicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readState(ctx, &state); err != nil {
		if errors.Is(err, errReplicationConfigurationAbsent) || isReplicationNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading bucket replication", "Could not read: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketReplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketReplicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketReplication(ctx, buildPutReplicationInput(plan)); err != nil {
		resp.Diagnostics.AddError("Error updating bucket replication", "Could not update: "+err.Error())
		return
	}

	state := plan
	if err := r.readState(ctx, &state); err != nil {
		tflog.Warn(ctx, "could not read back bucket replication after update; keeping planned values")
		normalizeReplicationUnknowns(&state)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketReplicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketReplicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := &s3.DeleteBucketReplicationInput{Bucket: aws.String(data.Bucket.ValueString())}
	if v := data.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	if _, err := r.client.S3.DeleteBucketReplication(ctx, input); err != nil {
		if isReplicationNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error removing bucket replication", "Could not remove: "+err.Error())
		return
	}
}

func (r *BucketReplicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

// readState refreshes the server-returned portions of the replication configuration in place.
// Request-only fields (token, checksum_algorithm, content_md5, expected_bucket_owner) are not
// returned by GetBucketReplication and are preserved from the incoming state.
func (r *BucketReplicationResource) readState(ctx context.Context, state *bucketReplicationResourceModel) error {
	input := &s3.GetBucketReplicationInput{Bucket: aws.String(state.Bucket.ValueString())}
	if v := state.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	out, err := r.client.S3.GetBucketReplication(ctx, input)
	if err != nil {
		return err
	}

	config := out.ReplicationConfiguration
	if config == nil || len(config.Rules) == 0 {
		return errReplicationConfigurationAbsent
	}

	state.Role = types.StringValue(aws.ToString(config.Role))
	state.Rule = flattenReplicationRules(config.Rules)
	return nil
}

// errReplicationConfigurationAbsent signals that GetBucketReplication succeeded but
// reported no replication configuration. It is a sentinel rather than a diagnostic so
// Read can remove the resource from state instead of writing nulls into the Required
// role and rule attributes.
var errReplicationConfigurationAbsent = errors.New("bucket replication configuration is absent")

func isReplicationNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchBucket", "ReplicationConfigurationNotFoundError", "NotFound", "404":
			return true
		}
	}

	msg := err.Error()
	return strings.Contains(msg, "NoSuchReplicationConfiguration") ||
		strings.Contains(msg, "ReplicationConfigurationNotFoundError") ||
		strings.Contains(msg, "NoSuchBucket")
}

func normalizeReplicationUnknowns(state *bucketReplicationResourceModel) {
	for i := range state.Rule {
		if state.Rule[i].Id.IsUnknown() {
			state.Rule[i].Id = types.StringNull()
		}
		if state.Rule[i].Priority.IsUnknown() {
			state.Rule[i].Priority = types.Int64Null()
		}
	}
}

func buildPutReplicationInput(plan bucketReplicationResourceModel) *s3.PutBucketReplicationInput {
	input := &s3.PutBucketReplicationInput{
		Bucket:                   aws.String(plan.Bucket.ValueString()),
		ReplicationConfiguration: buildReplicationConfig(plan),
	}
	if v := plan.Token.ValueString(); v != "" {
		input.Token = aws.String(v)
	}
	if v := plan.ChecksumAlgorithm.ValueString(); v != "" {
		input.ChecksumAlgorithm = s3types.ChecksumAlgorithm(v)
	}
	if v := plan.ContentMD5.ValueString(); v != "" {
		input.ContentMD5 = aws.String(v)
	}
	if v := plan.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}
	return input
}

func buildReplicationConfig(plan bucketReplicationResourceModel) *s3types.ReplicationConfiguration {
	rules := make([]s3types.ReplicationRule, 0, len(plan.Rule))
	for _, rp := range plan.Rule {
		rule := s3types.ReplicationRule{
			Status: s3types.ReplicationRuleStatus(rp.Status.ValueString()),
		}
		if v := rp.Id.ValueString(); v != "" {
			rule.ID = aws.String(v)
		}
		if !rp.Priority.IsNull() && !rp.Priority.IsUnknown() {
			p := int32(rp.Priority.ValueInt64()) // #nosec G115 - priority is a small positive integer
			rule.Priority = &p
		}
		rule.Filter = buildReplicationFilter(rp.Filter)
		if rp.DeleteMarkerReplication != nil {
			rule.DeleteMarkerReplication = &s3types.DeleteMarkerReplication{
				Status: s3types.DeleteMarkerReplicationStatus(rp.DeleteMarkerReplication.Status.ValueString()),
			}
		}
		if rp.ExistingObjectReplication != nil {
			rule.ExistingObjectReplication = &s3types.ExistingObjectReplication{
				Status: s3types.ExistingObjectReplicationStatus(rp.ExistingObjectReplication.Status.ValueString()),
			}
		}
		if rp.SourceSelectionCriteria != nil {
			ssc := &s3types.SourceSelectionCriteria{}
			if rp.SourceSelectionCriteria.ReplicaModifications != nil {
				ssc.ReplicaModifications = &s3types.ReplicaModifications{
					Status: s3types.ReplicaModificationsStatus(rp.SourceSelectionCriteria.ReplicaModifications.Status.ValueString()),
				}
			}
			if rp.SourceSelectionCriteria.SseKmsEncryptedObjects != nil {
				ssc.SseKmsEncryptedObjects = &s3types.SseKmsEncryptedObjects{
					Status: s3types.SseKmsEncryptedObjectsStatus(rp.SourceSelectionCriteria.SseKmsEncryptedObjects.Status.ValueString()),
				}
			}
			rule.SourceSelectionCriteria = ssc
		}
		rule.Destination = buildReplicationDestination(rp.Destination)
		rules = append(rules, rule)
	}

	return &s3types.ReplicationConfiguration{
		Role:  aws.String(plan.Role.ValueString()),
		Rules: rules,
	}
}

func buildReplicationFilter(f *bucketReplicationFilterModel) *s3types.ReplicationRuleFilter {
	if f == nil {
		return nil
	}
	filter := &s3types.ReplicationRuleFilter{}
	if v := f.Prefix.ValueString(); v != "" {
		filter.Prefix = aws.String(v)
	}
	if f.Tag != nil {
		filter.Tag = &s3types.Tag{
			Key:   aws.String(f.Tag.Key.ValueString()),
			Value: aws.String(f.Tag.Value.ValueString()),
		}
	}
	if f.And != nil {
		and := &s3types.ReplicationRuleAndOperator{}
		if v := f.And.Prefix.ValueString(); v != "" {
			and.Prefix = aws.String(v)
		}
		for _, t := range f.And.Tags {
			and.Tags = append(and.Tags, s3types.Tag{
				Key:   aws.String(t.Key.ValueString()),
				Value: aws.String(t.Value.ValueString()),
			})
		}
		filter.And = and
	}
	return filter
}

func buildReplicationDestination(m *bucketReplicationDestinationModel) *s3types.Destination {
	if m == nil {
		return nil
	}
	dest := &s3types.Destination{Bucket: aws.String(m.Bucket.ValueString())}
	if v := m.Account.ValueString(); v != "" {
		dest.Account = aws.String(v)
	}
	if v := m.StorageClass.ValueString(); v != "" {
		dest.StorageClass = s3types.StorageClass(v)
	}
	if m.AccessControlTranslation != nil {
		dest.AccessControlTranslation = &s3types.AccessControlTranslation{
			Owner: s3types.OwnerOverride(m.AccessControlTranslation.Owner.ValueString()),
		}
	}
	if m.EncryptionConfiguration != nil {
		enc := &s3types.EncryptionConfiguration{}
		if v := m.EncryptionConfiguration.ReplicaKmsKeyId.ValueString(); v != "" {
			enc.ReplicaKmsKeyID = aws.String(v)
		}
		dest.EncryptionConfiguration = enc
	}
	if m.Metrics != nil {
		metrics := &s3types.Metrics{Status: s3types.MetricsStatus(m.Metrics.Status.ValueString())}
		metrics.EventThreshold = buildReplicationTimeValue(m.Metrics.EventThreshold)
		dest.Metrics = metrics
	}
	if m.ReplicationTime != nil {
		rt := &s3types.ReplicationTime{Status: s3types.ReplicationTimeStatus(m.ReplicationTime.Status.ValueString())}
		rt.Time = buildReplicationTimeValue(m.ReplicationTime.Time)
		dest.ReplicationTime = rt
	}
	return dest
}

func buildReplicationTimeValue(m *bucketReplicationTimeValueModel) *s3types.ReplicationTimeValue {
	if m == nil || m.Minutes.IsNull() || m.Minutes.IsUnknown() {
		return nil
	}
	return &s3types.ReplicationTimeValue{Minutes: aws.Int32(int32(m.Minutes.ValueInt64()))} // #nosec G115 - bounded by API
}

func flattenReplicationRules(rules []s3types.ReplicationRule) []bucketReplicationRuleModel {
	out := make([]bucketReplicationRuleModel, 0, len(rules))
	for _, rule := range rules {
		m := bucketReplicationRuleModel{
			Status: types.StringValue(string(rule.Status)),
		}
		if v := aws.ToString(rule.ID); v != "" {
			m.Id = types.StringValue(v)
		}
		if rule.Priority != nil {
			m.Priority = types.Int64Value(int64(*rule.Priority))
		}
		m.Filter = flattenReplicationFilter(rule.Filter)
		if rule.DeleteMarkerReplication != nil {
			m.DeleteMarkerReplication = &bucketReplicationStatusModel{
				Status: types.StringValue(string(rule.DeleteMarkerReplication.Status)),
			}
		}
		if rule.ExistingObjectReplication != nil {
			m.ExistingObjectReplication = &bucketReplicationStatusModel{
				Status: types.StringValue(string(rule.ExistingObjectReplication.Status)),
			}
		}
		if rule.SourceSelectionCriteria != nil {
			ssc := &bucketReplicationSourceSelectionModel{}
			if rule.SourceSelectionCriteria.ReplicaModifications != nil {
				ssc.ReplicaModifications = &bucketReplicationStatusModel{
					Status: types.StringValue(string(rule.SourceSelectionCriteria.ReplicaModifications.Status)),
				}
			}
			if rule.SourceSelectionCriteria.SseKmsEncryptedObjects != nil {
				ssc.SseKmsEncryptedObjects = &bucketReplicationStatusModel{
					Status: types.StringValue(string(rule.SourceSelectionCriteria.SseKmsEncryptedObjects.Status)),
				}
			}
			m.SourceSelectionCriteria = ssc
		}
		m.Destination = flattenReplicationDestination(rule.Destination)
		out = append(out, m)
	}
	return out
}

func flattenReplicationFilter(f *s3types.ReplicationRuleFilter) *bucketReplicationFilterModel {
	if f == nil {
		return nil
	}
	m := &bucketReplicationFilterModel{}
	if f.Prefix != nil {
		m.Prefix = types.StringValue(aws.ToString(f.Prefix))
	}
	if f.Tag != nil {
		m.Tag = &bucketReplicationTagModel{
			Key:   types.StringValue(aws.ToString(f.Tag.Key)),
			Value: types.StringValue(aws.ToString(f.Tag.Value)),
		}
	}
	if f.And != nil {
		and := &bucketReplicationAndOperatorModel{}
		if f.And.Prefix != nil {
			and.Prefix = types.StringValue(aws.ToString(f.And.Prefix))
		}
		for _, t := range f.And.Tags {
			and.Tags = append(and.Tags, bucketReplicationTagModel{
				Key:   types.StringValue(aws.ToString(t.Key)),
				Value: types.StringValue(aws.ToString(t.Value)),
			})
		}
		m.And = and
	}
	return m
}

func flattenReplicationDestination(d *s3types.Destination) *bucketReplicationDestinationModel {
	if d == nil {
		return nil
	}
	m := &bucketReplicationDestinationModel{
		Bucket: types.StringValue(aws.ToString(d.Bucket)),
	}
	if v := aws.ToString(d.Account); v != "" {
		m.Account = types.StringValue(v)
	}
	if v := string(d.StorageClass); v != "" {
		m.StorageClass = types.StringValue(v)
	}
	if d.AccessControlTranslation != nil {
		m.AccessControlTranslation = &bucketReplicationAccessControlTranslationModel{
			Owner: types.StringValue(string(d.AccessControlTranslation.Owner)),
		}
	}
	if d.EncryptionConfiguration != nil {
		enc := &bucketReplicationEncryptionConfigurationModel{}
		if v := aws.ToString(d.EncryptionConfiguration.ReplicaKmsKeyID); v != "" {
			enc.ReplicaKmsKeyId = types.StringValue(v)
		}
		m.EncryptionConfiguration = enc
	}
	if d.Metrics != nil {
		metrics := &bucketReplicationMetricsModel{
			Status: types.StringValue(string(d.Metrics.Status)),
		}
		metrics.EventThreshold = flattenReplicationTimeValue(d.Metrics.EventThreshold)
		m.Metrics = metrics
	}
	if d.ReplicationTime != nil {
		rt := &bucketReplicationTimeModel{
			Status: types.StringValue(string(d.ReplicationTime.Status)),
		}
		rt.Time = flattenReplicationTimeValue(d.ReplicationTime.Time)
		m.ReplicationTime = rt
	}
	return m
}

func flattenReplicationTimeValue(v *s3types.ReplicationTimeValue) *bucketReplicationTimeValueModel {
	if v == nil {
		return nil
	}
	m := &bucketReplicationTimeValueModel{}
	if v.Minutes != nil {
		m.Minutes = types.Int64Value(int64(*v.Minutes))
	}
	return m
}
