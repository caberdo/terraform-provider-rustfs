// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                   = &notificationTargetResource{}
	_ resource.ResourceWithImportState    = &notificationTargetResource{}
	_ resource.ResourceWithValidateConfig = &notificationTargetResource{}
)

// NewNotificationTargetResource is a helper function to simplify the provider implementation.
func NewNotificationTargetResource() resource.Resource {
	return &notificationTargetResource{}
}

// notificationTargetResource is the resource implementation.
type notificationTargetResource struct {
	client *AllClient
}

type notificationTargetResourceModel struct {
	TargetType   types.String `tfsdk:"target_type"`
	TargetName   types.String `tfsdk:"target_name"`
	Enable       types.Bool   `tfsdk:"enable"`
	Comment      types.String `tfsdk:"comment"`
	Status       types.String `tfsdk:"status"`
	HealthState  types.String `tfsdk:"health_state"`
	HealthReason types.String `tfsdk:"health_reason"`

	Webhook  *targetWebhookConfigModel  `tfsdk:"webhook_config"`
	Amqp     *targetAmqpConfigModel     `tfsdk:"amqp_config"`
	Kafka    *targetKafkaConfigModel    `tfsdk:"kafka_config"`
	Mqtt     *targetMqttConfigModel     `tfsdk:"mqtt_config"`
	Nats     *targetNatsConfigModel     `tfsdk:"nats_config"`
	Postgres *targetPostgresConfigModel `tfsdk:"postgres_config"`
	Mysql    *targetMysqlConfigModel    `tfsdk:"mysql_config"`
	Redis    *targetRedisConfigModel    `tfsdk:"redis_config"`
	Pulsar   *targetPulsarConfigModel   `tfsdk:"pulsar_config"`
}

// configBlocks assembles the per-type configuration blocks held on the model
// into the shared targetConfigBlocks container.
func (m *notificationTargetResourceModel) configBlocks() targetConfigBlocks {
	return targetConfigBlocks{
		Webhook:  m.Webhook,
		Amqp:     m.Amqp,
		Kafka:    m.Kafka,
		Mqtt:     m.Mqtt,
		Nats:     m.Nats,
		Postgres: m.Postgres,
		Mysql:    m.Mysql,
		Redis:    m.Redis,
		Pulsar:   m.Pulsar,
	}
}

// Metadata returns the resource type name.
func (r *notificationTargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_notification_target"
}

// Schema defines the schema for the resource.
func (r *notificationTargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"target_type": schema.StringAttribute{
			Required:      true,
			Description:   "Notification target subsystem (for example notify_amqp). Changing this forces recreation.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators: []validator.String{stringvalidator.OneOf(
				"notify_webhook",
				"notify_amqp",
				"notify_kafka",
				"notify_mqtt",
				"notify_nats",
				"notify_postgres",
				"notify_mysql",
				"notify_redis",
				"notify_pulsar",
			)},
		},
		"target_name": schema.StringAttribute{
			Required:      true,
			Description:   "Name of the notification target. Changing this forces recreation.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"enable": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(true),
			Description: "Whether the notification target is enabled.",
		},
		"comment": schema.StringAttribute{
			Optional:    true,
			Description: "Optional comment describing the notification target.",
		},
		"status": schema.StringAttribute{
			Computed:    true,
			Description: "Runtime status of the notification target.",
		},
		"health_state": schema.StringAttribute{
			Computed:    true,
			Description: "Runtime health state of the notification target (offline/online/...).",
		},
		"health_reason": schema.StringAttribute{
			Computed:    true,
			Description: "Runtime health reason for the notification target.",
		},
	}

	for name, attr := range targetConfigBlockSchema() {
		attributes[name] = attr
	}

	resp.Schema = schema.Schema{
		Description:         "Manage RustFS event-notification targets",
		MarkdownDescription: "Manage RustFS event-notification targets",
		Attributes:          attributes,
	}
}

func (r *notificationTargetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the "exactly one matching block" invariant that the
// schema alone cannot express.
func (r *notificationTargetResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model notificationTargetResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := model.validateConfig(); err != nil {
		resp.Diagnostics.AddError(
			"Invalid notification target configuration",
			err.Error(),
		)
	}
}

// validateConfig enforces the "exactly one matching block" invariant that the
// schema alone cannot express. target_type is Required, so an unknown or null
// value means the framework will report its own "required" diagnostic; skip the
// invariant in that case rather than normalizing "" and falsely rejecting a
// matching block.
func (m *notificationTargetResourceModel) validateConfig() error {
	if m.TargetType.IsUnknown() || m.TargetType.IsNull() {
		return nil
	}
	return m.configBlocks().validate(m.TargetType.ValueString())
}

// Create adds the notification target and sets the initial Terraform state.
func (r *notificationTargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan notificationTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := plan.configBlocks().keyValues(plan.TargetType.ValueString(), plan.Enable.ValueBool(), plan.Comment.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid notification target configuration",
			err.Error(),
		)
		return
	}

	if err := r.client.RustClient.SetNotificationTarget(
		ctx, plan.TargetType.ValueString(),
		plan.TargetName.ValueString(),
		kvs,
	); err != nil {
		resp.Diagnostics.AddError(
			"Error creating notification target",
			"Could not create notification target, unexpected error: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "created a notification target")
	if err := r.refreshHealth(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading notification target",
			"Could not read notification target after create, unexpected error: "+err.Error(),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// refreshHealth lists the notification targets and populates the computed
// health metadata on the model when the target is present.
func (r *notificationTargetResource) refreshHealth(ctx context.Context, model *notificationTargetResourceModel) error {
	targets, err := r.client.RustClient.ListNotificationTargets(ctx)
	if err != nil {
		return err
	}

	key := model.TargetName.ValueString()
	service := targetServiceName(model.TargetType.ValueString())
	for i := range targets {
		t := &targets[i]
		if t.AccountID == key && t.Service == service {
			model.Status = types.StringValue(t.Status)
			model.HealthState = types.StringValue(t.HealthState)
			model.HealthReason = types.StringValue(t.HealthReason)
			return nil
		}
	}
	return fmt.Errorf("notification target %s not found in list", key)
}

// Read refreshes the Terraform state with the latest data. The admin list
// endpoint does not echo the target configuration (endpoint, credentials, ...),
// only identity and health metadata, so the config fields are preserved from
// the prior state and only the health/computed metadata is refreshed.
func (r *notificationTargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state notificationTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	targets, err := r.client.RustClient.ListNotificationTargets(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing notification targets",
			"Could not list notification targets, unexpected error: "+err.Error(),
		)
		return
	}

	key := state.TargetName.ValueString()
	service := targetServiceName(state.TargetType.ValueString())
	var found *rustfs.NotificationTarget
	for i := range targets {
		t := &targets[i]
		if t.AccountID == key && t.Service == service {
			found = t
			break
		}
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Status = types.StringValue(found.Status)
	state.HealthState = types.StringValue(found.HealthState)
	state.HealthReason = types.StringValue(found.HealthReason)
	// target_type, target_name, enable, comment and the per-type config blocks
	// are preserved from state; the list response is not trusted to echo them
	// back.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	tflog.Trace(ctx, "read a notification target")
}

// Update edits the notification target and updates the Terraform state on success.
func (r *notificationTargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan notificationTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := plan.configBlocks().keyValues(plan.TargetType.ValueString(), plan.Enable.ValueBool(), plan.Comment.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid notification target configuration",
			err.Error(),
		)
		return
	}

	if err := r.client.RustClient.SetNotificationTarget(
		ctx, plan.TargetType.ValueString(),
		plan.TargetName.ValueString(),
		kvs,
	); err != nil {
		resp.Diagnostics.AddError(
			"Error updating notification target",
			"Could not update notification target, unexpected error: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "updated a notification target")
	if err := r.refreshHealth(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading notification target",
			"Could not read notification target after update, unexpected error: "+err.Error(),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete resets the notification target to its default configuration and
// removes the Terraform state. The admin API exposes no plain delete for
// targets, so destroy goes through the reset endpoint.
func (r *notificationTargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state notificationTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A reset of a target that is already gone is answered 200 OK: the RustFS
	// handler removes the target's config entry when it is present and reports
	// success either way (rustfs admin event handler, RemoveNotificationTarget
	// -> remove_target_config, which only rewrites the config when the entry
	// exists). Delete therefore stays idempotent without matching on the error
	// text; 400 InvalidArgument for an unsupported target type and every other
	// failure is a real error the user must see.
	if err := r.client.RustClient.ResetNotificationTarget(
		ctx, state.TargetType.ValueString(),
		state.TargetName.ValueString(),
	); err != nil {
		resp.Diagnostics.AddError(
			"Error resetting notification target",
			"Could not reset notification target, unexpected error: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "deleted a notification target")
}

func (r *notificationTargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected the import ID to be <target_type>/<target_name>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target_type"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target_name"), parts[1])...)
}

func init() {
	registerResource(NewNotificationTargetResource)
}
