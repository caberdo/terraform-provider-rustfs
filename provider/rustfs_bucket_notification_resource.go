package provider

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// unsupported: EventBridgeConfiguration has no members; its presence alone
// enables delivery and is exposed as event_bridge.event_bridge_enabled.
// All members of NotificationConfiguration and
// PutBucketNotificationConfigurationInput are exposed, and MinIO/RustFS
// extension events are added to the event validator.

// MinIO/RustFS extension events that are valid on the wire but are absent
// from the AWS SDK s3types.Event enum.
var minioExtensionEvents = []string{
	"s3:ObjectAccessed:*",
	"s3:ObjectAccessed:Get",
	"s3:ObjectAccessed:Head",
	"s3:ObjectAccessed:GetRetention",
	"s3:ObjectAccessed:GetLegalHold",
}

func notificationEventValues() []string {
	return append(enumStrings(s3types.Event("").Values()), minioExtensionEvents...)
}

var (
	_ resource.Resource                = &BucketNotificationResource{}
	_ resource.ResourceWithImportState = &BucketNotificationResource{}
)

type BucketNotificationResource struct {
	client *AllClient
}

type bucketNotificationTargetModel struct {
	Id     types.String                   `tfsdk:"id"`
	Arn    types.String                   `tfsdk:"arn"`
	Events types.Set                      `tfsdk:"events"`
	Filter *bucketNotificationFilterModel `tfsdk:"filter"`
}

type bucketNotificationFilterModel struct {
	Prefix types.String `tfsdk:"prefix"`
	Suffix types.String `tfsdk:"suffix"`
}

type bucketNotificationEventBridgeModel struct {
	EventBridgeEnabled types.Bool `tfsdk:"event_bridge_enabled"`
}

type bucketNotificationResourceModel struct {
	Bucket                    types.String                        `tfsdk:"bucket"`
	ExpectedBucketOwner       types.String                        `tfsdk:"expected_bucket_owner"`
	SkipDestinationValidation types.Bool                          `tfsdk:"skip_destination_validation"`
	EventBridge               *bucketNotificationEventBridgeModel `tfsdk:"event_bridge"`
	Queue                     []bucketNotificationTargetModel     `tfsdk:"queue"`
	Topic                     []bucketNotificationTargetModel     `tfsdk:"topic"`
	Lambda                    []bucketNotificationTargetModel     `tfsdk:"lambda"`
}

func NewBucketNotificationResource() resource.Resource {
	return &BucketNotificationResource{}
}

func (r *BucketNotificationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_notification"
}

func (r *BucketNotificationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	eventsValidator := []validator.Set{
		setvalidator.ValueStringsAre(
			stringvalidator.OneOf(notificationEventValues()...),
		),
	}

	resp.Schema = schema.Schema{
		Description:         "Manage RustFS bucket event notifications",
		MarkdownDescription: "Manage RustFS bucket event notification configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"expected_bucket_owner": schema.StringAttribute{
				Optional:    true,
				Description: "Account ID of the expected bucket owner.",
			},
			"skip_destination_validation": schema.BoolAttribute{
				Optional:    true,
				Description: "Skips validation of the SQS, SNS and Lambda destinations.",
			},
			"event_bridge": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Amazon EventBridge notification settings.",
				Attributes: map[string]schema.Attribute{
					"event_bridge_enabled": schema.BoolAttribute{
						Required:    true,
						Description: "Whether event delivery to Amazon EventBridge is enabled.",
					},
				},
			},
			"queue": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Amazon SQS queue notification configurations.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Optional unique identifier for the configuration.",
						},
						"arn": schema.StringAttribute{
							Required:    true,
							Description: "ARN of the queue target (e.g., arn:minio:sqs::PRIMARY:amqp).",
						},
						"events": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "S3 event types (e.g., s3:ObjectCreated:*, s3:ObjectRemoved:*).",
							Validators:  eventsValidator,
						},
						"filter": notificationFilterAttribute(),
					},
				},
			},
			"topic": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Amazon SNS topic notification configurations.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Optional unique identifier for the configuration.",
						},
						"arn": schema.StringAttribute{
							Required:    true,
							Description: "ARN of the topic target (e.g., arn:minio:sns::PRIMARY:topic).",
						},
						"events": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "S3 event types (e.g., s3:ObjectCreated:*, s3:ObjectRemoved:*).",
							Validators:  eventsValidator,
						},
						"filter": notificationFilterAttribute(),
					},
				},
			},
			"lambda": schema.ListNestedAttribute{
				Optional:    true,
				Description: "AWS Lambda function notification configurations.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Optional unique identifier for the configuration.",
						},
						"arn": schema.StringAttribute{
							Required:    true,
							Description: "ARN of the Lambda function target.",
						},
						"events": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "S3 event types (e.g., s3:ObjectCreated:*, s3:ObjectRemoved:*).",
							Validators:  eventsValidator,
						},
						"filter": notificationFilterAttribute(),
					},
				},
			},
		},
	}
}

func notificationFilterAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Object key name prefix/suffix filtering rules.",
		Attributes: map[string]schema.Attribute{
			"prefix": schema.StringAttribute{
				Optional:    true,
				Description: "Filter events by object key prefix.",
			},
			"suffix": schema.StringAttribute{
				Optional:    true,
				Description: "Filter events by object key suffix.",
			},
		},
	}
}

func (r *BucketNotificationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *BucketNotificationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketNotificationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketNotificationConfiguration(ctx, buildPutNotificationInput(ctx, plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error setting bucket notification",
			"Could not set bucket notification: "+err.Error(),
		)
		return
	}

	tflog.Trace(ctx, "created a bucket notification resource")

	state := plan
	if err := r.readState(ctx, &state); err != nil {
		tflog.Warn(ctx, "could not read back bucket notification after create; keeping planned values")
		normalizeNotificationUnknowns(&state)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketNotificationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketNotificationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readState(ctx, &state); err != nil {
		if isNotificationNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket notification",
			"Could not read bucket notification: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketNotificationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketNotificationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.PutBucketNotificationConfiguration(ctx, buildPutNotificationInput(ctx, plan)); err != nil {
		resp.Diagnostics.AddError(
			"Error updating bucket notification",
			"Could not update bucket notification: "+err.Error(),
		)
		return
	}

	state := plan
	if err := r.readState(ctx, &state); err != nil {
		tflog.Warn(ctx, "could not read back bucket notification after update; keeping planned values")
		normalizeNotificationUnknowns(&state)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BucketNotificationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketNotificationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := &s3.PutBucketNotificationConfigurationInput{
		Bucket:                    aws.String(data.Bucket.ValueString()),
		NotificationConfiguration: &s3types.NotificationConfiguration{},
	}
	if v := data.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	if _, err := r.client.S3.PutBucketNotificationConfiguration(ctx, input); err != nil {
		resp.Diagnostics.AddError(
			"Error removing bucket notification",
			"Could not remove bucket notification: "+err.Error(),
		)
		return
	}
}

func (r *BucketNotificationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
}

// readState refreshes the server-returned portions of the notification configuration in place.
// Request-only fields (skip_destination_validation, expected_bucket_owner) are not returned by
// GetBucketNotificationConfiguration and are preserved from the incoming state.
func (r *BucketNotificationResource) readState(ctx context.Context, state *bucketNotificationResourceModel) error {
	input := &s3.GetBucketNotificationConfigurationInput{Bucket: aws.String(state.Bucket.ValueString())}
	if v := state.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}

	out, err := r.client.S3.GetBucketNotificationConfiguration(ctx, input)
	if err != nil {
		return err
	}

	state.Queue = flattenQueueTargets(out.QueueConfigurations)
	state.Topic = flattenTopicTargets(out.TopicConfigurations)
	state.Lambda = flattenLambdaTargets(out.LambdaFunctionConfigurations)

	reconcileEventBridge(state, out.EventBridgeConfiguration != nil)
	return nil
}

// reconcileEventBridge refreshes the event_bridge block from the server response.
// event_bridge is Optional and not Computed, so a configured
// `event_bridge = { event_bridge_enabled = false }` must survive a read when the
// server reports no EventBridge configuration; only an omitted block collapses to nil.
func reconcileEventBridge(state *bucketNotificationResourceModel, serverConfigured bool) {
	if serverConfigured {
		state.EventBridge = &bucketNotificationEventBridgeModel{EventBridgeEnabled: types.BoolValue(true)}
		return
	}
	if state.EventBridge != nil {
		state.EventBridge.EventBridgeEnabled = types.BoolValue(false)
	}
}

func isNotificationNotFound(err error) bool {
	return isBucketSubresourceAbsent(err, "NoSuchConfiguration")
}

func normalizeNotificationUnknowns(state *bucketNotificationResourceModel) {
	normalizeNotificationTargetIds(state.Queue)
	normalizeNotificationTargetIds(state.Topic)
	normalizeNotificationTargetIds(state.Lambda)
}

func normalizeNotificationTargetIds(targets []bucketNotificationTargetModel) {
	for i := range targets {
		if targets[i].Id.IsUnknown() {
			targets[i].Id = types.StringNull()
		}
	}
}

func buildPutNotificationInput(ctx context.Context, plan bucketNotificationResourceModel) *s3.PutBucketNotificationConfigurationInput {
	input := &s3.PutBucketNotificationConfigurationInput{
		Bucket:                    aws.String(plan.Bucket.ValueString()),
		NotificationConfiguration: buildNotificationConfig(ctx, plan),
	}
	if !plan.SkipDestinationValidation.IsNull() && !plan.SkipDestinationValidation.IsUnknown() {
		input.SkipDestinationValidation = aws.Bool(plan.SkipDestinationValidation.ValueBool())
	}
	if v := plan.ExpectedBucketOwner.ValueString(); v != "" {
		input.ExpectedBucketOwner = aws.String(v)
	}
	return input
}

func buildNotificationConfig(ctx context.Context, plan bucketNotificationResourceModel) *s3types.NotificationConfiguration {
	config := &s3types.NotificationConfiguration{}

	for _, q := range plan.Queue {
		qc := s3types.QueueConfiguration{
			QueueArn: aws.String(q.Arn.ValueString()),
			Events:   notificationEvents(ctx, q.Events),
			Filter:   buildNotificationFilter(q.Filter),
		}
		if v := q.Id.ValueString(); v != "" {
			qc.Id = aws.String(v)
		}
		config.QueueConfigurations = append(config.QueueConfigurations, qc)
	}

	for _, t := range plan.Topic {
		tc := s3types.TopicConfiguration{
			TopicArn: aws.String(t.Arn.ValueString()),
			Events:   notificationEvents(ctx, t.Events),
			Filter:   buildNotificationFilter(t.Filter),
		}
		if v := t.Id.ValueString(); v != "" {
			tc.Id = aws.String(v)
		}
		config.TopicConfigurations = append(config.TopicConfigurations, tc)
	}

	for _, l := range plan.Lambda {
		lc := s3types.LambdaFunctionConfiguration{
			LambdaFunctionArn: aws.String(l.Arn.ValueString()),
			Events:            notificationEvents(ctx, l.Events),
			Filter:            buildNotificationFilter(l.Filter),
		}
		if v := l.Id.ValueString(); v != "" {
			lc.Id = aws.String(v)
		}
		config.LambdaFunctionConfigurations = append(config.LambdaFunctionConfigurations, lc)
	}

	if plan.EventBridge != nil && plan.EventBridge.EventBridgeEnabled.ValueBool() {
		config.EventBridgeConfiguration = &s3types.EventBridgeConfiguration{}
	}

	return config
}

func buildNotificationFilter(f *bucketNotificationFilterModel) *s3types.NotificationConfigurationFilter {
	if f == nil {
		return nil
	}
	var rules []s3types.FilterRule
	if v := f.Prefix.ValueString(); v != "" {
		rules = append(rules, s3types.FilterRule{Name: s3types.FilterRuleNamePrefix, Value: aws.String(v)})
	}
	if v := f.Suffix.ValueString(); v != "" {
		rules = append(rules, s3types.FilterRule{Name: s3types.FilterRuleNameSuffix, Value: aws.String(v)})
	}
	if len(rules) == 0 {
		return nil
	}
	return &s3types.NotificationConfigurationFilter{
		Key: &s3types.S3KeyFilter{FilterRules: rules},
	}
}

func notificationEvents(ctx context.Context, s types.Set) []s3types.Event {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var raw []string
	s.ElementsAs(ctx, &raw, false)
	out := make([]s3types.Event, 0, len(raw))
	for _, e := range raw {
		out = append(out, s3types.Event(e))
	}
	return out
}

func flattenQueueTargets(configs []s3types.QueueConfiguration) []bucketNotificationTargetModel {
	if len(configs) == 0 {
		return nil
	}
	out := make([]bucketNotificationTargetModel, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, newNotificationTarget(cfg.Id, cfg.QueueArn, cfg.Events, cfg.Filter))
	}
	return out
}

func flattenTopicTargets(configs []s3types.TopicConfiguration) []bucketNotificationTargetModel {
	if len(configs) == 0 {
		return nil
	}
	out := make([]bucketNotificationTargetModel, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, newNotificationTarget(cfg.Id, cfg.TopicArn, cfg.Events, cfg.Filter))
	}
	return out
}

func flattenLambdaTargets(configs []s3types.LambdaFunctionConfiguration) []bucketNotificationTargetModel {
	if len(configs) == 0 {
		return nil
	}
	out := make([]bucketNotificationTargetModel, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, newNotificationTarget(cfg.Id, cfg.LambdaFunctionArn, cfg.Events, cfg.Filter))
	}
	return out
}

func newNotificationTarget(id, arn *string, events []s3types.Event, filter *s3types.NotificationConfigurationFilter) bucketNotificationTargetModel {
	m := bucketNotificationTargetModel{
		Arn:    types.StringValue(aws.ToString(arn)),
		Events: bucketNotificationStringSet(events),
	}
	if v := aws.ToString(id); v != "" {
		m.Id = types.StringValue(v)
	}
	m.Filter = flattenNotificationFilter(filter)
	return m
}

func flattenNotificationFilter(f *s3types.NotificationConfigurationFilter) *bucketNotificationFilterModel {
	if f == nil || f.Key == nil {
		return nil
	}
	m := &bucketNotificationFilterModel{}
	for _, rule := range f.Key.FilterRules {
		switch rule.Name {
		case s3types.FilterRuleNamePrefix:
			m.Prefix = types.StringValue(aws.ToString(rule.Value))
		case s3types.FilterRuleNameSuffix:
			m.Suffix = types.StringValue(aws.ToString(rule.Value))
		}
	}
	if m.Prefix.IsNull() && m.Suffix.IsNull() {
		return nil
	}
	return m
}

func bucketNotificationStringSet(events []s3types.Event) types.Set {
	elements := make([]attr.Value, len(events))
	for i, e := range events {
		elements[i] = types.StringValue(string(e))
	}
	return types.SetValueMust(types.StringType, elements)
}
