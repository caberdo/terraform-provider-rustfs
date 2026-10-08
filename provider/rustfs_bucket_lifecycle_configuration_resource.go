package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                     = &bucketLifecycleConfigurationResource{}
	_ resource.ResourceWithImportState      = &bucketLifecycleConfigurationResource{}
	_ resource.ResourceWithConfigValidators = &bucketLifecycleConfigurationResource{}
)

// NewBucketLifecycleConfigurationResource is a helper function to simplify the provider implementation.
func NewBucketLifecycleConfigurationResource() resource.Resource {
	return &bucketLifecycleConfigurationResource{}
}

// bucketLifecycleConfigurationResource is the resource implementation.
type bucketLifecycleConfigurationResource struct {
	client *AllClient
}

type bucketLifecycleConfigurationModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	// TransitionDefaultMinimumObjectSize is exposed as Optional+Computed so the
	// server-side default is reflected without forcing the user to set it.
	TransitionDefaultMinimumObjectSize types.String `tfsdk:"transition_default_minimum_object_size"`
	Rule                               []ruleModel  `tfsdk:"rule"`
}

type ruleModel struct {
	Id                             types.String                         `tfsdk:"id"`
	Status                         types.String                         `tfsdk:"status"`
	Filter                         *filterModel                         `tfsdk:"filter"`
	Expiration                     *expirationModel                     `tfsdk:"expiration"`
	Transition                     []transitionModel                    `tfsdk:"transition"`
	NoncurrentVersionExpiration    *noncurrentVersionExpirationModel    `tfsdk:"noncurrent_version_expiration"`
	NoncurrentVersionTransition    []noncurrentVersionTransitionModel   `tfsdk:"noncurrent_version_transition"`
	AbortIncompleteMultipartUpload *abortIncompleteMultipartUploadModel `tfsdk:"abort_incomplete_multipart_upload"`
}

type filterModel struct {
	Prefix                types.String    `tfsdk:"prefix"`
	Tag                   *tagModel       `tfsdk:"tag"`
	ObjectSizeGreaterThan types.Int64     `tfsdk:"object_size_greater_than"`
	ObjectSizeLessThan    types.Int64     `tfsdk:"object_size_less_than"`
	And                   *filterAndModel `tfsdk:"and"`
}

type tagModel struct {
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
}

type filterAndModel struct {
	Prefix                types.String `tfsdk:"prefix"`
	Tags                  []tagModel   `tfsdk:"tags"`
	ObjectSizeGreaterThan types.Int64  `tfsdk:"object_size_greater_than"`
	ObjectSizeLessThan    types.Int64  `tfsdk:"object_size_less_than"`
}

type expirationModel struct {
	Days                      types.Int64  `tfsdk:"days"`
	Date                      types.String `tfsdk:"date"`
	ExpiredObjectDeleteMarker types.Bool   `tfsdk:"expired_object_delete_marker"`
}

type transitionModel struct {
	Days         types.Int64  `tfsdk:"days"`
	Date         types.String `tfsdk:"date"`
	StorageClass types.String `tfsdk:"storage_class"`
}

type noncurrentVersionExpirationModel struct {
	NoncurrentDays          types.Int64 `tfsdk:"noncurrent_days"`
	NewerNoncurrentVersions types.Int64 `tfsdk:"newer_noncurrent_versions"`
}

type noncurrentVersionTransitionModel struct {
	NoncurrentDays          types.Int64  `tfsdk:"noncurrent_days"`
	NewerNoncurrentVersions types.Int64  `tfsdk:"newer_noncurrent_versions"`
	StorageClass            types.String `tfsdk:"storage_class"`
}

type abortIncompleteMultipartUploadModel struct {
	DaysAfterInitiation types.Int64 `tfsdk:"days_after_initiation"`
}

// Metadata returns the resource type name.
func (r *bucketLifecycleConfigurationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_lifecycle_configuration"
}

// SDK fields intentionally not exposed:
//   - unsupported: s3types.LifecycleRule.Prefix is deprecated; filter.prefix is
//     the supported equivalent and is exposed there instead.
//   - unsupported: PutBucketLifecycleConfigurationInput.ExpectedBucketOwner is an
//     AWS-only account-ownership assertion that RustFS does not implement.
//   - unsupported: PutBucketLifecycleConfigurationInput.ChecksumAlgorithm is AWS
//     SDK request-integrity plumbing, not part of the lifecycle configuration.
//
// Schema defines the schema for the resource.
func (r *bucketLifecycleConfigurationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage S3 bucket lifecycle configurations in rustfs",
		MarkdownDescription: "Manage S3 bucket lifecycle configurations in rustfs",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Description: "Name of the bucket",
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Description: "The bucket name",
			},
			"transition_default_minimum_object_size": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Which default minimum object size behaviour applies to transitions in this configuration: " +
					"all_storage_classes_128K or varies_by_storage_class.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.TransitionDefaultMinimumObjectSize("").Values())...),
				},
			},
			"rule": schema.ListNestedAttribute{
				Required:    true,
				Description: "List of lifecycle rules",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Required:    true,
							Description: "Unique identifier for the rule",
						},
						"status": schema.StringAttribute{
							Required:    true,
							Description: "Whether the rule is currently applied: Enabled or Disabled",
							Validators: []validator.String{
								stringvalidator.OneOf(enumStrings(s3types.ExpirationStatus("").Values())...),
							},
						},
						"filter": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Filter identifying the objects to which the rule applies",
							Attributes: map[string]schema.Attribute{
								"prefix": schema.StringAttribute{
									Optional:    true,
									Description: "Object key prefix identifying one or more objects",
								},
								"tag": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Tag that must exist on an object for the rule to apply",
									Attributes: map[string]schema.Attribute{
										"key": schema.StringAttribute{
											Required:    true,
											Description: "Tag key",
										},
										"value": schema.StringAttribute{
											Required:    true,
											Description: "Tag value",
										},
									},
								},
								"object_size_greater_than": schema.Int64Attribute{
									Optional:    true,
									Description: "Minimum object size in bytes to which the rule applies",
								},
								"object_size_less_than": schema.Int64Attribute{
									Optional:    true,
									Description: "Maximum object size in bytes to which the rule applies",
								},
								"and": schema.SingleNestedAttribute{
									Optional:    true,
									Description: "Logical AND of two or more predicates that objects must all match",
									Attributes: map[string]schema.Attribute{
										"prefix": schema.StringAttribute{
											Optional:    true,
											Description: "Object key prefix identifying one or more objects",
										},
										"tags": schema.ListNestedAttribute{
											Optional:    true,
											Description: "Tags that must all exist on an object for the rule to apply",
											NestedObject: schema.NestedAttributeObject{
												Attributes: map[string]schema.Attribute{
													"key": schema.StringAttribute{
														Required:    true,
														Description: "Tag key",
													},
													"value": schema.StringAttribute{
														Required:    true,
														Description: "Tag value",
													},
												},
											},
										},
										"object_size_greater_than": schema.Int64Attribute{
											Optional:    true,
											Description: "Minimum object size in bytes to which the rule applies",
										},
										"object_size_less_than": schema.Int64Attribute{
											Optional:    true,
											Description: "Maximum object size in bytes to which the rule applies",
										},
									},
								},
							},
						},
						"expiration": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Configuration block for object expiration",
							Attributes: map[string]schema.Attribute{
								"days": schema.Int64Attribute{
									Optional:    true,
									Description: "Lifetime of the objects in days",
								},
								"date": schema.StringAttribute{
									Optional:    true,
									Description: "Date at which the objects expire (RFC3339, e.g. 2026-12-31T00:00:00Z)",
								},
								"expired_object_delete_marker": schema.BoolAttribute{
									Optional:    true,
									Description: "Whether to remove the delete marker of expired objects with no versions",
								},
							},
						},
						"transition": schema.ListNestedAttribute{
							Optional:    true,
							Description: "Configuration blocks for transitioning current object versions to an ILM tier",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"days": schema.Int64Attribute{
										Optional:    true,
										Description: "Lifetime of the objects in days before transition",
									},
									"date": schema.StringAttribute{
										Optional:    true,
										Description: "Date at which the objects are transitioned (RFC3339, e.g. 2026-12-31T00:00:00Z)",
									},
									"storage_class": schema.StringAttribute{
										Required:    true,
										Description: "Name of the RustFS ILM tier to transition objects to",
									},
								},
							},
						},
						"noncurrent_version_expiration": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Configuration block for expiring noncurrent object versions",
							Attributes: map[string]schema.Attribute{
								"noncurrent_days": schema.Int64Attribute{
									Optional:    true,
									Description: "Number of days an object is noncurrent before it expires",
								},
								"newer_noncurrent_versions": schema.Int64Attribute{
									Optional:    true,
									Description: "Number of noncurrent versions to retain",
								},
							},
						},
						"noncurrent_version_transition": schema.ListNestedAttribute{
							Optional:    true,
							Description: "Configuration blocks for transitioning noncurrent object versions to an ILM tier",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"noncurrent_days": schema.Int64Attribute{
										Optional:    true,
										Description: "Number of days an object is noncurrent before it is transitioned",
									},
									"newer_noncurrent_versions": schema.Int64Attribute{
										Optional:    true,
										Description: "Number of noncurrent versions to retain in the current storage class before transitioning",
									},
									"storage_class": schema.StringAttribute{
										Required:    true,
										Description: "Name of the RustFS ILM tier to transition noncurrent versions to",
									},
								},
							},
						},
						"abort_incomplete_multipart_upload": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Configuration block for aborting incomplete multipart uploads",
							Attributes: map[string]schema.Attribute{
								"days_after_initiation": schema.Int64Attribute{
									Optional:    true,
									Description: "Number of days after multipart upload initiation before the upload is aborted",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *bucketLifecycleConfigurationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ConfigValidators enforces the S3 LifecycleRuleFilter invariants that the
// schema alone cannot express.
func (r *bucketLifecycleConfigurationResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{lifecycleFilterConfigValidator{}}
}

// lifecycleFilterConfigValidator reports filter predicate-group violations.
type lifecycleFilterConfigValidator struct{}

func (lifecycleFilterConfigValidator) Description(context.Context) string {
	return "Ensures each lifecycle rule filter carries exactly one predicate group and that filter.and has at least two predicates."
}

func (v lifecycleFilterConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (lifecycleFilterConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config bucketLifecycleConfigurationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, violation := range validateLifecycleFilters(config.Rule) {
		resp.Diagnostics.AddAttributeError(
			path.Root("rule").AtListIndex(violation.RuleIndex).AtName("filter"),
			"Invalid lifecycle rule filter",
			violation.Message,
		)
	}
}

// Create creates the resource and sets the initial Terraform state.
func (r *bucketLifecycleConfigurationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketLifecycleConfigurationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.putLifecycleConfiguration(ctx, plan); err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket lifecycle configuration",
			"Could not create lifecycle configuration: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket lifecycle configuration resource")

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	resolveUnknownObjectSize(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the Terraform state with the latest data.
func (r *bucketLifecycleConfigurationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketLifecycleConfigurationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.client.S3.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(state.Bucket.ValueString()),
	})
	if err != nil {
		if isLifecycleNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket lifecycle configuration",
			"Could not read lifecycle configuration: "+err.Error(),
		)
		return
	}

	state.Rule = flattenLifecycleRules(out.Rules)
	if v := string(out.TransitionDefaultMinimumObjectSize); v != "" {
		state.TransitionDefaultMinimumObjectSize = types.StringValue(v)
	} else if state.TransitionDefaultMinimumObjectSize.IsUnknown() {
		state.TransitionDefaultMinimumObjectSize = types.StringNull()
	}
	state.Id = types.StringValue(state.Bucket.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *bucketLifecycleConfigurationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketLifecycleConfigurationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.putLifecycleConfiguration(ctx, plan); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket lifecycle configuration",
			"Could not update lifecycle configuration: "+err.Error(),
		)
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString())
	resolveUnknownObjectSize(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *bucketLifecycleConfigurationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketLifecycleConfigurationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.S3.DeleteBucketLifecycle(ctx, &s3.DeleteBucketLifecycleInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		if isLifecycleNotFound(err) {
			// Already deleted.
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket lifecycle configuration",
			"Could not delete lifecycle configuration: "+err.Error(),
		)
		return
	}
}

func (r *bucketLifecycleConfigurationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

// putLifecycleConfiguration writes the planned configuration to the bucket.
func (r *bucketLifecycleConfigurationResource) putLifecycleConfiguration(ctx context.Context, plan bucketLifecycleConfigurationModel) error {
	rules, err := expandLifecycleRules(plan.Rule)
	if err != nil {
		return err
	}

	input := &s3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(plan.Bucket.ValueString()),
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
			Rules: rules,
		},
	}
	if v := plan.TransitionDefaultMinimumObjectSize; !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		input.TransitionDefaultMinimumObjectSize = s3types.TransitionDefaultMinimumObjectSize(v.ValueString())
	}

	_, err = r.client.S3.PutBucketLifecycleConfiguration(ctx, input)
	return err
}

// isLifecycleNotFound reports whether err means the bucket or its lifecycle
// configuration is absent, in which case a read or delete is a no-op.
func isLifecycleNotFound(err error) bool {
	if err == nil {
		return false
	}
	var noSuchBucket *s3types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return true
	}
	return isBucketSubresourceAbsent(err, "NoSuchLifecycleConfiguration")
}

// resolveUnknownObjectSize replaces an unresolved Optional+Computed value with
// null so no unknown value is ever written into state.
func resolveUnknownObjectSize(m *bucketLifecycleConfigurationModel) {
	if m.TransitionDefaultMinimumObjectSize.IsUnknown() {
		m.TransitionDefaultMinimumObjectSize = types.StringNull()
	}
}

// lifecycleFilterViolation describes a single LifecycleRuleFilter invariant
// violation, so the config validator can point at the offending rule.
type lifecycleFilterViolation struct {
	RuleIndex int
	RuleID    string
	Message   string
}

// validateLifecycleFilters returns every filter invariant violation across the
// rules. S3 requires a LifecycleRuleFilter to carry exactly one predicate
// group: "and" may not be combined at the same level with prefix, tag,
// object_size_greater_than or object_size_less_than, and "and" itself needs at
// least two predicates. An empty result means the configuration is acceptable.
func validateLifecycleFilters(rules []ruleModel) []lifecycleFilterViolation {
	var out []lifecycleFilterViolation
	for i, rule := range rules {
		f := rule.Filter
		if f == nil || f.And == nil {
			continue
		}

		if knownStringSet(f.Prefix) || f.Tag != nil ||
			knownIntSet(f.ObjectSizeGreaterThan) || knownIntSet(f.ObjectSizeLessThan) {
			out = append(out, lifecycleFilterViolation{
				RuleIndex: i,
				RuleID:    rule.Id.ValueString(),
				Message: "filter.and cannot be combined with prefix, tag, object_size_greater_than " +
					"or object_size_less_than at the same level: the filter must carry exactly one predicate group",
			})
		}

		// Skip the predicate count while values are still unknown, otherwise a
		// plan with interpolated values is rejected before it can be evaluated.
		if f.And.Prefix.IsUnknown() || f.And.ObjectSizeGreaterThan.IsUnknown() || f.And.ObjectSizeLessThan.IsUnknown() {
			continue
		}
		predicates := len(f.And.Tags)
		if knownStringSet(f.And.Prefix) {
			predicates++
		}
		if knownIntSet(f.And.ObjectSizeGreaterThan) {
			predicates++
		}
		if knownIntSet(f.And.ObjectSizeLessThan) {
			predicates++
		}
		if predicates < 2 {
			out = append(out, lifecycleFilterViolation{
				RuleIndex: i,
				RuleID:    rule.Id.ValueString(),
				Message: "filter.and must contain at least two predicates " +
					"(prefix, tags, object_size_greater_than or object_size_less_than)",
			})
		}
	}
	return out
}

func knownStringSet(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func knownIntSet(v types.Int64) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func expandLifecycleRules(rules []ruleModel) ([]s3types.LifecycleRule, error) {
	out := make([]s3types.LifecycleRule, 0, len(rules))
	for _, r := range rules {
		rule := s3types.LifecycleRule{
			ID:     aws.String(r.Id.ValueString()),
			Status: s3types.ExpirationStatus(r.Status.ValueString()),
		}

		if r.Filter != nil {
			filter, err := expandLifecycleFilter(r.Filter)
			if err != nil {
				return nil, fmt.Errorf("rule %q: %w", r.Id.ValueString(), err)
			}
			rule.Filter = filter
		}

		if r.Expiration != nil {
			expiration, err := expandExpiration(r.Expiration)
			if err != nil {
				return nil, fmt.Errorf("rule %q: %w", r.Id.ValueString(), err)
			}
			rule.Expiration = expiration
		}

		for _, t := range r.Transition {
			transition, err := expandTransition(t)
			if err != nil {
				return nil, fmt.Errorf("rule %q: %w", r.Id.ValueString(), err)
			}
			rule.Transitions = append(rule.Transitions, *transition)
		}

		if r.NoncurrentVersionExpiration != nil {
			rule.NoncurrentVersionExpiration = &s3types.NoncurrentVersionExpiration{
				NoncurrentDays:          int32Pointer(r.NoncurrentVersionExpiration.NoncurrentDays),
				NewerNoncurrentVersions: int32Pointer(r.NoncurrentVersionExpiration.NewerNoncurrentVersions),
			}
		}

		for _, t := range r.NoncurrentVersionTransition {
			rule.NoncurrentVersionTransitions = append(rule.NoncurrentVersionTransitions, s3types.NoncurrentVersionTransition{
				NoncurrentDays:          int32Pointer(t.NoncurrentDays),
				NewerNoncurrentVersions: int32Pointer(t.NewerNoncurrentVersions),
				StorageClass:            s3types.TransitionStorageClass(t.StorageClass.ValueString()),
			})
		}

		if r.AbortIncompleteMultipartUpload != nil {
			rule.AbortIncompleteMultipartUpload = &s3types.AbortIncompleteMultipartUpload{
				DaysAfterInitiation: int32Pointer(r.AbortIncompleteMultipartUpload.DaysAfterInitiation),
			}
		}

		out = append(out, rule)
	}
	return out, nil
}

func expandLifecycleFilter(m *filterModel) (*s3types.LifecycleRuleFilter, error) {
	filter := &s3types.LifecycleRuleFilter{
		ObjectSizeGreaterThan: int64Pointer(m.ObjectSizeGreaterThan),
		ObjectSizeLessThan:    int64Pointer(m.ObjectSizeLessThan),
	}
	if s := m.Prefix; !s.IsNull() && !s.IsUnknown() {
		filter.Prefix = aws.String(s.ValueString())
	}
	if m.Tag != nil {
		filter.Tag = &s3types.Tag{
			Key:   aws.String(m.Tag.Key.ValueString()),
			Value: aws.String(m.Tag.Value.ValueString()),
		}
	}
	if m.And != nil {
		and := &s3types.LifecycleRuleAndOperator{
			ObjectSizeGreaterThan: int64Pointer(m.And.ObjectSizeGreaterThan),
			ObjectSizeLessThan:    int64Pointer(m.And.ObjectSizeLessThan),
		}
		if s := m.And.Prefix; !s.IsNull() && !s.IsUnknown() {
			and.Prefix = aws.String(s.ValueString())
		}
		for _, t := range m.And.Tags {
			and.Tags = append(and.Tags, s3types.Tag{
				Key:   aws.String(t.Key.ValueString()),
				Value: aws.String(t.Value.ValueString()),
			})
		}
		filter.And = and
	}
	return filter, nil
}

func expandExpiration(m *expirationModel) (*s3types.LifecycleExpiration, error) {
	expiration := &s3types.LifecycleExpiration{
		Days: int32Pointer(m.Days),
	}
	date, err := datePointer(m.Date)
	if err != nil {
		return nil, err
	}
	expiration.Date = date
	if b := m.ExpiredObjectDeleteMarker; !b.IsNull() && !b.IsUnknown() {
		expiration.ExpiredObjectDeleteMarker = aws.Bool(b.ValueBool())
	}
	return expiration, nil
}

func expandTransition(m transitionModel) (*s3types.Transition, error) {
	transition := &s3types.Transition{
		Days:         int32Pointer(m.Days),
		StorageClass: s3types.TransitionStorageClass(m.StorageClass.ValueString()),
	}
	date, err := datePointer(m.Date)
	if err != nil {
		return nil, err
	}
	transition.Date = date
	return transition, nil
}

func flattenLifecycleRules(rules []s3types.LifecycleRule) []ruleModel {
	out := make([]ruleModel, 0, len(rules))
	for _, r := range rules {
		rule := ruleModel{
			Id:     types.StringValue(aws.ToString(r.ID)),
			Status: types.StringValue(string(r.Status)),
		}
		rule.Filter = flattenLifecycleFilter(r.Filter)
		rule.Expiration = flattenExpiration(r.Expiration)
		for _, t := range r.Transitions {
			rule.Transition = append(rule.Transition, flattenTransition(t))
		}
		rule.NoncurrentVersionExpiration = flattenNoncurrentVersionExpiration(r.NoncurrentVersionExpiration)
		for _, t := range r.NoncurrentVersionTransitions {
			rule.NoncurrentVersionTransition = append(rule.NoncurrentVersionTransition, flattenNoncurrentVersionTransition(t))
		}
		rule.AbortIncompleteMultipartUpload = flattenAbortIncompleteMultipartUpload(r.AbortIncompleteMultipartUpload)
		out = append(out, rule)
	}
	return out
}

func flattenLifecycleFilter(f *s3types.LifecycleRuleFilter) *filterModel {
	if f == nil {
		return nil
	}
	if f.Prefix == nil && f.Tag == nil && f.ObjectSizeGreaterThan == nil && f.ObjectSizeLessThan == nil && f.And == nil {
		return nil
	}

	m := &filterModel{
		Prefix:                types.StringNull(),
		ObjectSizeGreaterThan: types.Int64Null(),
		ObjectSizeLessThan:    types.Int64Null(),
	}
	if f.Prefix != nil {
		m.Prefix = types.StringValue(*f.Prefix)
	}
	if f.Tag != nil {
		m.Tag = &tagModel{
			Key:   types.StringValue(aws.ToString(f.Tag.Key)),
			Value: types.StringValue(aws.ToString(f.Tag.Value)),
		}
	}
	if f.ObjectSizeGreaterThan != nil {
		m.ObjectSizeGreaterThan = types.Int64Value(*f.ObjectSizeGreaterThan)
	}
	if f.ObjectSizeLessThan != nil {
		m.ObjectSizeLessThan = types.Int64Value(*f.ObjectSizeLessThan)
	}
	if f.And != nil {
		and := &filterAndModel{
			Prefix:                types.StringNull(),
			ObjectSizeGreaterThan: types.Int64Null(),
			ObjectSizeLessThan:    types.Int64Null(),
		}
		if f.And.Prefix != nil {
			and.Prefix = types.StringValue(*f.And.Prefix)
		}
		for _, t := range f.And.Tags {
			and.Tags = append(and.Tags, tagModel{
				Key:   types.StringValue(aws.ToString(t.Key)),
				Value: types.StringValue(aws.ToString(t.Value)),
			})
		}
		if f.And.ObjectSizeGreaterThan != nil {
			and.ObjectSizeGreaterThan = types.Int64Value(*f.And.ObjectSizeGreaterThan)
		}
		if f.And.ObjectSizeLessThan != nil {
			and.ObjectSizeLessThan = types.Int64Value(*f.And.ObjectSizeLessThan)
		}
		m.And = and
	}
	return m
}

func flattenExpiration(e *s3types.LifecycleExpiration) *expirationModel {
	if e == nil || (e.Days == nil && e.Date == nil && e.ExpiredObjectDeleteMarker == nil) {
		return nil
	}
	m := &expirationModel{
		Days:                      types.Int64Null(),
		Date:                      types.StringNull(),
		ExpiredObjectDeleteMarker: types.BoolNull(),
	}
	if e.Days != nil {
		m.Days = types.Int64Value(int64(*e.Days))
	}
	if e.Date != nil {
		m.Date = types.StringValue(e.Date.UTC().Format(time.RFC3339))
	}
	if e.ExpiredObjectDeleteMarker != nil {
		m.ExpiredObjectDeleteMarker = types.BoolValue(*e.ExpiredObjectDeleteMarker)
	}
	return m
}

func flattenTransition(t s3types.Transition) transitionModel {
	m := transitionModel{
		Days:         types.Int64Null(),
		Date:         types.StringNull(),
		StorageClass: types.StringValue(string(t.StorageClass)),
	}
	if t.Days != nil {
		m.Days = types.Int64Value(int64(*t.Days))
	}
	if t.Date != nil {
		m.Date = types.StringValue(t.Date.UTC().Format(time.RFC3339))
	}
	return m
}

func flattenNoncurrentVersionExpiration(e *s3types.NoncurrentVersionExpiration) *noncurrentVersionExpirationModel {
	if e == nil || (e.NoncurrentDays == nil && e.NewerNoncurrentVersions == nil) {
		return nil
	}
	m := &noncurrentVersionExpirationModel{
		NoncurrentDays:          types.Int64Null(),
		NewerNoncurrentVersions: types.Int64Null(),
	}
	if e.NoncurrentDays != nil {
		m.NoncurrentDays = types.Int64Value(int64(*e.NoncurrentDays))
	}
	if e.NewerNoncurrentVersions != nil {
		m.NewerNoncurrentVersions = types.Int64Value(int64(*e.NewerNoncurrentVersions))
	}
	return m
}

func flattenNoncurrentVersionTransition(t s3types.NoncurrentVersionTransition) noncurrentVersionTransitionModel {
	m := noncurrentVersionTransitionModel{
		NoncurrentDays:          types.Int64Null(),
		NewerNoncurrentVersions: types.Int64Null(),
		StorageClass:            types.StringValue(string(t.StorageClass)),
	}
	if t.NoncurrentDays != nil {
		m.NoncurrentDays = types.Int64Value(int64(*t.NoncurrentDays))
	}
	if t.NewerNoncurrentVersions != nil {
		m.NewerNoncurrentVersions = types.Int64Value(int64(*t.NewerNoncurrentVersions))
	}
	return m
}

func flattenAbortIncompleteMultipartUpload(a *s3types.AbortIncompleteMultipartUpload) *abortIncompleteMultipartUploadModel {
	if a == nil || a.DaysAfterInitiation == nil {
		return nil
	}
	return &abortIncompleteMultipartUploadModel{
		DaysAfterInitiation: types.Int64Value(int64(*a.DaysAfterInitiation)),
	}
}

// int32Pointer converts a framework Int64 into the *int32 the SDK expects,
// leaving absent values as nil.
func int32Pointer(v types.Int64) *int32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return aws.Int32(int32(v.ValueInt64())) // #nosec G115
}

// int64Pointer converts a framework Int64 into the *int64 the SDK expects,
// leaving absent values as nil.
func int64Pointer(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	n := v.ValueInt64()
	return &n
}

// datePointer parses an RFC3339 date string into the *time.Time the SDK
// expects, leaving absent values as nil.
func datePointer(v types.String) (*time.Time, error) {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v.ValueString())
	if err != nil {
		return nil, fmt.Errorf("invalid RFC3339 date %q: %w", v.ValueString(), err)
	}
	t = t.UTC()
	return &t, nil
}
