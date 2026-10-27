// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                   = &auditTargetResource{}
	_ resource.ResourceWithImportState    = &auditTargetResource{}
	_ resource.ResourceWithValidateConfig = &auditTargetResource{}
)

// NewAuditTargetResource is a helper function to simplify the provider implementation.
func NewAuditTargetResource() resource.Resource {
	return &auditTargetResource{}
}

// auditTargetResource is the resource implementation.
type auditTargetResource struct {
	client *AllClient
}

type auditTargetResourceModel struct {
	TargetType    types.String `tfsdk:"target_type"`
	TargetName    types.String `tfsdk:"target_name"`
	Enable        types.Bool   `tfsdk:"enable"`
	Endpoint      types.String `tfsdk:"endpoint"`
	AuthToken     types.String `tfsdk:"auth_token"`
	Comment       types.String `tfsdk:"comment"`
	QueueLimit    types.Int64  `tfsdk:"queue_limit"`
	QueueDir      types.String `tfsdk:"queue_dir"`
	ClientCert    types.String `tfsdk:"client_cert"`
	ClientKey     types.String `tfsdk:"client_key"`
	ClientCA      types.String `tfsdk:"client_ca"`
	SkipTLSVerify types.Bool   `tfsdk:"skip_tls_verify"`
	HealthState   types.String `tfsdk:"health_state"`
	HealthReason  types.String `tfsdk:"health_reason"`
	Status        types.String `tfsdk:"status"`

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
func (m *auditTargetResourceModel) configBlocks() targetConfigBlocks {
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
func (r *auditTargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_audit_target"
}

// Schema defines the schema for the resource.
func (r *auditTargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"target_type": schema.StringAttribute{
			Optional:    true,
			Computed:    true,
			Default:     stringdefault.StaticString("audit_webhook"),
			Description: "Audit target subsystem. Defaults to audit_webhook.",
			Validators: []validator.String{stringvalidator.OneOf(
				"audit_webhook",
				"audit_kafka",
				"audit_amqp",
				"audit_mqtt",
				"audit_mysql",
				"audit_nats",
				"audit_postgres",
				"audit_pulsar",
				"audit_redis",
			)},
		},
		"target_name": schema.StringAttribute{
			Required:      true,
			Description:   "Name of the audit target. Changing this forces recreation.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"enable": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(true),
			Description: "Whether the audit target is enabled.",
		},
		"endpoint": schema.StringAttribute{
			Optional:    true,
			Description: "Endpoint URL the audit target posts to (webhook).",
		},
		"auth_token": schema.StringAttribute{
			Optional:    true,
			Sensitive:   true,
			Description: "Optional bearer token for the audit target (webhook). Not refreshed from the server on read.",
		},
		"comment": schema.StringAttribute{
			Optional:    true,
			Description: "Optional comment describing the audit target.",
		},
		"queue_limit": schema.Int64Attribute{
			Optional:    true,
			Description: "Optional in-memory queue limit for the audit target.",
		},
		"queue_dir": schema.StringAttribute{
			Optional:    true,
			Description: "Optional absolute path of the on-disk queue directory for the audit target.",
		},
		"client_cert": schema.StringAttribute{
			Optional:    true,
			Sensitive:   true,
			Description: "Optional client certificate for mTLS to the audit target.",
		},
		"client_key": schema.StringAttribute{
			Optional:    true,
			Sensitive:   true,
			Description: "Optional client private key for mTLS to the audit target.",
		},
		"client_ca": schema.StringAttribute{
			Optional:    true,
			Sensitive:   true,
			Description: "Optional CA certificate used to verify the audit target.",
		},
		"skip_tls_verify": schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Default:     booldefault.StaticBool(false),
			Description: "Skip TLS certificate verification for the audit target.",
		},
		"health_state": schema.StringAttribute{
			Computed:    true,
			Description: "Runtime health state of the audit target (offline/online/...).",
		},
		"health_reason": schema.StringAttribute{
			Computed:    true,
			Description: "Runtime health reason for the audit target.",
		},
		"status": schema.StringAttribute{
			Computed:    true,
			Description: "Runtime status of the audit target.",
		},
	}

	for name, attr := range targetConfigBlockSchema() {
		attributes[name] = attr
	}

	resp.Schema = schema.Schema{
		Description:         "Manage RustFS audit-log targets (webhook, amqp, kafka, mqtt, nats, postgres, mysql, redis, pulsar)",
		MarkdownDescription: "Manage RustFS audit-log targets (webhook, amqp, kafka, mqtt, nats, postgres, mysql, redis, pulsar)",
		Attributes:          attributes,
	}
}

func (r *auditTargetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the two-path invariant the schema alone cannot
// express: when a typed block is set it must be the single matching block and
// the flat webhook attributes must be absent; when no block is set the target
// type must be audit_webhook (the legacy flat path).
func (r *auditTargetResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model auditTargetResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := model.validateConfig(); err != nil {
		resp.Diagnostics.AddError(
			"Invalid audit target configuration",
			err.Error(),
		)
	}
}

// validateConfig enforces the audit target's two-path configuration rules and
// returns a diagnostic-ready error when they are violated.
func (m *auditTargetResourceModel) validateConfig() error {
	// target_type is Optional+Computed with a default of "audit_webhook". An
	// unknown value cannot be resolved during config validation, so skip the
	// two-path comparison rather than normalizing "" to "audit_webhook" and
	// falsely rejecting a matching block.
	if m.TargetType.IsUnknown() {
		return nil
	}

	// target_type is Optional+Computed with a default of "audit_webhook", and
	// ValidateConfig runs before defaults are applied, so an omitted target_type
	// arrives as null/empty and must be normalized to the default before the
	// two-path comparison.
	targetType := m.TargetType.ValueString()
	if targetType == "" {
		targetType = "audit_webhook"
	}

	blocks := m.configBlocks()
	if len(blocks.setBlocks()) == 0 {
		// Legacy flat path: only audit_webhook may omit a typed block.
		if targetType != "audit_webhook" {
			return fmt.Errorf("target type %q requires the %s block", targetType, targetConfigBlockName(targetType))
		}
		return nil
	}

	// Typed path: exactly one block, matching target_type, and no flat attrs.
	if err := blocks.validate(targetType); err != nil {
		return err
	}
	if flatWebhookAttributesSet(m) {
		return errors.New("the flat webhook attributes cannot be combined with a configuration block")
	}
	return nil
}

// flatWebhookAttributesSet reports whether any of the legacy flat webhook
// attributes are set on the model.
func flatWebhookAttributesSet(m *auditTargetResourceModel) bool {
	for _, v := range []types.String{m.Endpoint, m.AuthToken, m.QueueDir, m.ClientCert, m.ClientKey, m.ClientCA} {
		if !v.IsNull() && !v.IsUnknown() {
			return true
		}
	}
	if !m.QueueLimit.IsNull() && !m.QueueLimit.IsUnknown() {
		return true
	}
	if !m.SkipTLSVerify.IsNull() && !m.SkipTLSVerify.IsUnknown() {
		return true
	}
	return false
}

// auditTargetKeyValues builds the key/value config body from the resource model.
//
// When no typed block is set it produces the legacy flat webhook output, with
// the trailing enable pair appended last. When a block is set it delegates to
// the shared per-type mapping.
func (r *auditTargetResource) auditTargetKeyValues(model *auditTargetResourceModel) ([]rustfs.TargetKeyValue, error) {
	if len(model.configBlocks().setBlocks()) > 0 {
		return model.configBlocks().keyValues(model.TargetType.ValueString(), model.Enable.ValueBool(), model.Comment.ValueString())
	}

	// Legacy path: the previous flat ordering, unchanged, plus the enable pair.
	var kvs []rustfs.TargetKeyValue
	add := func(key string, value string) {
		if value != "" {
			kvs = append(kvs, rustfs.TargetKeyValue{Key: key, Value: value})
		}
	}
	add("endpoint", model.Endpoint.ValueString())
	add("auth_token", model.AuthToken.ValueString())
	add("comment", model.Comment.ValueString())
	add("queue_dir", model.QueueDir.ValueString())
	add("client_cert", model.ClientCert.ValueString())
	add("client_key", model.ClientKey.ValueString())
	add("client_ca", model.ClientCA.ValueString())
	if !model.QueueLimit.IsNull() && !model.QueueLimit.IsUnknown() {
		kvs = append(kvs, rustfs.TargetKeyValue{Key: "queue_limit", Value: strconv.FormatInt(model.QueueLimit.ValueInt64(), 10)})
	}
	if !model.SkipTLSVerify.IsNull() && !model.SkipTLSVerify.IsUnknown() {
		kvs = append(kvs, rustfs.TargetKeyValue{Key: "skip_tls_verify", Value: strconv.FormatBool(model.SkipTLSVerify.ValueBool())})
	}
	enable := "off"
	if model.Enable.ValueBool() {
		enable = "on"
	}
	kvs = append(kvs, rustfs.TargetKeyValue{Key: "enable", Value: enable})
	return kvs, nil
}

// Create adds the audit target and sets the initial Terraform state.
func (r *auditTargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan auditTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := r.auditTargetKeyValues(&plan)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid audit target configuration",
			err.Error(),
		)
		return
	}

	if err := r.client.RustClient.SetAuditTarget(
		ctx, plan.TargetType.ValueString(),
		plan.TargetName.ValueString(),
		kvs,
	); err != nil {
		resp.Diagnostics.AddError(
			"Error creating audit target",
			"Could not create audit target, unexpected error: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "created an audit target")
	if err := r.refreshHealth(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading audit target",
			"Could not read audit target after create, unexpected error: "+err.Error(),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// refreshHealth lists the audit targets and populates the computed health
// metadata on the model when the target is present.
func (r *auditTargetResource) refreshHealth(ctx context.Context, model *auditTargetResourceModel) error {
	targets, err := r.client.RustClient.ListAuditTargets(ctx)
	if err != nil {
		return err
	}

	key := model.TargetName.ValueString()
	service := targetServiceName(model.TargetType.ValueString())
	for i := range targets {
		t := &targets[i]
		if t.AccountID == key && (service == "" || t.Service == service) {
			model.Status = types.StringValue(t.Status)
			model.HealthState = types.StringValue(t.HealthState)
			model.HealthReason = types.StringValue(t.HealthReason)
			return nil
		}
	}
	return fmt.Errorf("audit target %s not found in list", key)
}

// Read refreshes the Terraform state with the latest data. The admin list
// endpoint does not echo the target configuration (endpoint, auth token, ...),
// only identity and health metadata, so the config fields are preserved from
// the prior state and only the health/computed metadata is refreshed.
func (r *auditTargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state auditTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	targets, err := r.client.RustClient.ListAuditTargets(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing audit targets",
			"Could not list audit targets, unexpected error: "+err.Error(),
		)
		return
	}

	key := state.TargetName.ValueString()
	service := targetServiceName(state.TargetType.ValueString())
	var found *rustfs.AuditTarget
	for i := range targets {
		t := &targets[i]
		if t.AccountID == key && (service == "" || t.Service == service) {
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
	// endpoint, auth_token, comment and the other config fields are preserved
	// from state; the list response is not trusted to echo them back.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update edits the audit target and updates the Terraform state on success.
func (r *auditTargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan auditTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kvs, err := r.auditTargetKeyValues(&plan)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid audit target configuration",
			err.Error(),
		)
		return
	}

	if err := r.client.RustClient.SetAuditTarget(
		ctx, plan.TargetType.ValueString(),
		plan.TargetName.ValueString(),
		kvs,
	); err != nil {
		resp.Diagnostics.AddError(
			"Error updating audit target",
			"Could not update audit target, unexpected error: "+err.Error(),
		)
		return
	}
	if err := r.refreshHealth(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Error reading audit target",
			"Could not read audit target after update, unexpected error: "+err.Error(),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete resets the audit target to its default configuration and removes the
// Terraform state. The admin API exposes no plain delete for targets, so
// destroy goes through the reset endpoint.
func (r *auditTargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state auditTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.RustClient.ResetAuditTarget(
		ctx, state.TargetType.ValueString(),
		state.TargetName.ValueString(),
	); err != nil {
		resp.Diagnostics.AddError(
			"Error resetting audit target",
			"Could not reset audit target, unexpected error: "+err.Error(),
		)
	}
}

func (r *auditTargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
	registerResource(NewAuditTargetResource)
}
