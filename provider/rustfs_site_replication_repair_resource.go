// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                   = &SiteReplicationRepairResource{}
	_ resource.ResourceWithConfigure      = &SiteReplicationRepairResource{}
	_ resource.ResourceWithValidateConfig = &SiteReplicationRepairResource{}
	_ resource.ResourceWithImportState    = &SiteReplicationRepairResource{}
)

// expectedConfirmation is the value `confirm` has to repeat: the provider
// endpoint. The endpoint names the cluster the repair runs against, so a
// configuration copied to another environment fails instead of repairing the
// wrong deployment. It is empty only before Configure, when validation is
// deferred to Create.
func (r *SiteReplicationRepairResource) expectedConfirmation() string {
	if r.client == nil {
		return ""
	}
	return r.client.RustClient.Endpoint()
}

// SiteReplicationRepairResource drives one site-replication repair and records
// its outcome in state.
//
// Create plans the repair (dry-run), executes the plan, and stores the terminal
// operation. Read re-reads a persisted operation by id. Delete is a no-op:
// RustFS has no route that undoes a repair. Mirroring the provider's other
// one-shot server operations (rustfs_pool_decommission, rustfs_rebalance), this
// is a resource rather than a Terraform action so it works on every Terraform
// version the provider supports and can be driven by the acceptance harness.
type SiteReplicationRepairResource struct {
	client *AllClient
}

type SiteReplicationRepairResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Confirm     types.String `tfsdk:"confirm"`
	OperationID types.String `tfsdk:"operation_id"`
	Status      types.String `tfsdk:"status"`
	RetryEvents types.Int64  `tfsdk:"retry_events"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
	CompletedAt types.String `tfsdk:"completed_at"`
	Sites       types.List   `tfsdk:"sites"`
}

func NewSiteReplicationRepairResource() resource.Resource {
	return &SiteReplicationRepairResource{}
}

func (r *SiteReplicationRepairResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_replication_repair"
}

func (r *SiteReplicationRepairResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Repair a RustFS site-replication group",
		MarkdownDescription: "Repair a RustFS site-replication group.\n\n" +
			"Creating this resource reconciles every peer's site-replication state: the provider asks " +
			"RustFS for a dry-run plan, executes it, and stores the resulting operation. The route is " +
			"synchronous, so `status` is terminal when the apply finishes (`success` or `partial`). A " +
			"`partial` result reports which site and family still have failures in `sites`; re-create " +
			"the resource (for example with `terraform taint`) to retry.\n\n" +
			"**Repair mutates data.** It rewrites bucket targets and IAM items on the peers, so " +
			"`confirm` must repeat the provider `endpoint`. RustFS persists a bounded history of operations and " +
			"prunes the oldest successful ones; when the record for this resource has been pruned, " +
			"`Read` keeps the last observed state instead of forgetting the repair and running it " +
			"again.\n\n" +
			"Destroying this resource does nothing on the server: a repair cannot be undone. It only " +
			"forgets the operation record.\n\n" +
			"Import sets `operation_id` from the import ID; the configuration must still carry " +
			"`confirm` before the next plan.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				Description: "Canonical repair operation ID the server records. It matches `operation_id` unless " +
					"RustFS normalised the requested value.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"confirm": schema.StringAttribute{
				Required: true,
				Description: "Safety interlock: must repeat the provider `endpoint` without its scheme " +
					"(for example `127.0.0.1:9001`, even when `endpoint` is `https://127.0.0.1:9001`). " +
					"A repair reconciles every peer and can rewrite bucket targets and IAM items, so naming the " +
					"cluster makes the operation deliberate and stops a configuration copied to another " +
					"environment from repairing the wrong deployment.",
			},
			"operation_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Repair operation ID. Leave unset to let the provider generate a UUID, or set it to " +
					"choose the ID of the new repair. Changing it starts a new repair; import an existing " +
					"operation with `terraform import`. Set it explicitly when a failed apply may have " +
					"reached the server: the operation can keep running, and a later import by that ID " +
					"adopts it. If the server records the operation under a different ID, `id` reports " +
					"that canonical value while this attribute keeps the configured one.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"status": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Description: "Repair status reported by the server: `success`, `partial` or `running`. RustFS " +
					"reports `partial` when at least one task failed; the per-site detail is in `sites`.",
			},
			"retry_events": schema.Int64Attribute{
				Computed: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
				Description: "Number of retry-queue events the dry-run plan replays during the repair.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Time the operation was created, in RFC 3339, as reported by the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Time the operation was last updated, in RFC 3339, as reported by the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"completed_at": schema.StringAttribute{
				Computed:    true,
				Description: "Time the operation completed, in RFC 3339. Null while it is still running.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"sites": schema.ListNestedAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
				Description: "Per-site repair progress, one row per site and repair family, ordered by deployment ID " +
					"and family name. A site that reports no repair families appears once with an empty `family`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"deployment_id": schema.StringAttribute{
							Computed:    true,
							Description: "Deployment ID of the site.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Configured name of the site.",
						},
						"family": schema.StringAttribute{
							Computed:    true,
							Description: "Repair family (RustFS currently reports `replication`).",
						},
						"planned": schema.Int64Attribute{
							Computed:    true,
							Description: "Tasks planned for this site and family.",
						},
						"succeeded": schema.Int64Attribute{
							Computed:    true,
							Description: "Tasks that succeeded.",
						},
						"failed": schema.Int64Attribute{
							Computed:    true,
							Description: "Tasks that failed.",
						},
						"retry_events": schema.Int64Attribute{
							Computed:    true,
							Description: "Retry-queue events replayed for this site and family.",
						},
						"errors": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Errors reported for this site and family.",
						},
					},
				},
			},
		},
	}
}

func (r *SiteReplicationRepairResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the confirmation interlock. Unknown values are left
// to Create, which repeats the check on the resolved plan.
func (r *SiteReplicationRepairResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config SiteReplicationRepairResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Confirm.IsNull() || config.Confirm.IsUnknown() {
		return
	}
	// The endpoint is only known once the provider has been configured. When it
	// is not (terraform validate without credentials), Create repeats the check.
	expected := r.expectedConfirmation()
	if expected == "" {
		return
	}
	if config.Confirm.ValueString() != expected {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Invalid site replication repair confirmation",
			fmt.Sprintf("confirm must repeat the provider endpoint %q; a repair reconciles every peer "+
				"and rewrites bucket targets and IAM items, so it has to be deliberate.", expected),
		)
	}
}

func (r *SiteReplicationRepairResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SiteReplicationRepairResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	expected := r.expectedConfirmation()
	if expected == "" {
		resp.Diagnostics.AddError(
			"Error repairing site replication",
			"The provider endpoint is unavailable, so the cluster being repaired cannot be confirmed.",
		)
		return
	}
	if plan.Confirm.IsNull() || plan.Confirm.IsUnknown() || plan.Confirm.ValueString() != expected {
		resp.Diagnostics.AddError(
			"Invalid site replication repair confirmation",
			fmt.Sprintf("confirm must repeat the provider endpoint %q.", expected),
		)
		return
	}

	requestedOperationID := plan.OperationID.ValueString()
	if requestedOperationID == "" {
		requestedOperationID = uuid.NewString()
	}

	preflight, err := r.client.RustClient.SiteReplicationRepairPreflight(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error planning site replication repair",
			"Could not plan site replication repair: "+err.Error(),
		)
		return
	}
	if preflight.PreflightToken == "" {
		resp.Diagnostics.AddError(
			"Error planning site replication repair",
			"The server returned a repair plan without a preflight token, so it cannot be executed.",
		)
		return
	}

	operation, err := r.client.RustClient.SiteReplicationRepairExecute(ctx, preflight.PreflightToken, requestedOperationID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error repairing site replication",
			"Could not repair site replication: "+err.Error(),
		)
		return
	}
	if operation.Status == "" {
		resp.Diagnostics.AddError(
			"Error repairing site replication",
			"The server accepted the repair but returned no operation status, so its outcome cannot be recorded.",
		)
		return
	}
	serverOperationID := repairServerOperationID(requestedOperationID, operation)
	tflog.Trace(ctx, "repaired site replication", map[string]any{
		"operation_id":        serverOperationID,
		"requested_operation": requestedOperationID,
		"status":              operation.Status,
	})

	// Keep the configured/generated value in operation_id: it is what the plan
	// promises and what RequiresReplace compares. The server's canonical ID goes
	// in id, so a normalised value cannot force a replacement that would run a
	// second repair.
	plan.ID = types.StringValue(serverOperationID)
	plan.OperationID = types.StringValue(requestedOperationID)
	plan.RetryEvents = types.Int64Value(preflight.RetryEvents)
	applySiteReplicationRepair(&plan, operation)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if operation.Status == "partial" {
		resp.Diagnostics.AddWarning(
			"Site replication repair completed with failures",
			"The repair operation "+serverOperationID+" finished with status `partial`; inspect `sites` for the "+
				"failed tasks and errors, then retry by replacing the resource.",
		)
	}
}

func (r *SiteReplicationRepairResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SiteReplicationRepairResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// id is the canonical ID the server reported, so prefer it; operation_id can
	// hold a configured value the server normalised.
	operationID := state.ID.ValueString()
	if operationID == "" {
		operationID = state.OperationID.ValueString()
	}
	if operationID == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	operation, err := r.client.RustClient.SiteReplicationRepairStatus(ctx, operationID)
	if err != nil {
		if rustfs.IsAPIErrorCode(err, "InvalidRequest") && strings.Contains(err.Error(), "repair operation was not found") {
			// RustFS keeps a bounded history and prunes the oldest successful
			// operations. Forgetting the resource here would make Terraform run
			// the repair again on the next apply, so the last observed outcome
			// stays in state.
			if state.Status.IsNull() || state.Status.IsUnknown() {
				resp.Diagnostics.AddError(
					"Error reading site replication repair",
					"The server does not hold a repair operation "+operationID+". Check operation_id or import "+
						"an operation the server still records.",
				)
				return
			}
			tflog.Trace(ctx, "site replication repair record was pruned; keeping the last observed state", map[string]any{
				"operation_id": operationID,
			})
			return
		}
		resp.Diagnostics.AddError(
			"Error reading site replication repair",
			"Could not read site replication repair: "+err.Error(),
		)
		return
	}

	if operation.Status == "" {
		resp.Diagnostics.AddError(
			"Error reading site replication repair",
			"The server returned a repair operation without a status, so its outcome cannot be recorded.",
		)
		return
	}
	serverOperationID := repairServerOperationID(operationID, operation)
	state.ID = types.StringValue(serverOperationID)
	if state.OperationID.ValueString() == "" {
		state.OperationID = types.StringValue(serverOperationID)
	}
	applySiteReplicationRepair(&state, operation)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SiteReplicationRepairResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SiteReplicationRepairResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// operation_id carries RequiresReplace, so changing it starts a new repair
	// and Terraform calls Create. confirm is the only attribute that can change
	// in place; it never reaches the server, but it still goes through the same
	// interlock so an imported resource cannot record a mismatched value.
	expected := r.expectedConfirmation()
	if expected == "" {
		resp.Diagnostics.AddError(
			"Error updating site replication repair",
			"The provider endpoint is unavailable, so the cluster being repaired cannot be confirmed.",
		)
		return
	}
	if plan.Confirm.IsNull() || plan.Confirm.IsUnknown() || plan.Confirm.ValueString() != expected {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Invalid site replication repair confirmation",
			fmt.Sprintf("confirm must repeat the provider endpoint %q.", expected),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is a no-op: RustFS has no route that undoes a repair, and the
// persisted operation is what `Read` reports. Destroying the resource only
// forgets which operation it started.
func (r *SiteReplicationRepairResource) Delete(ctx context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	tflog.Trace(ctx, "site replication repair cannot be undone; leaving the server operation in place")
}

func (r *SiteReplicationRepairResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("operation_id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// repairServerOperationID prefers the ID the server recorded, so lookups name
// an operation the server can find even if it normalised the requested value.
func repairServerOperationID(requested string, operation rustfs.SiteReplicationRepairOperation) string {
	if operation.OperationID != "" {
		return operation.OperationID
	}
	return requested
}

func applySiteReplicationRepair(model *SiteReplicationRepairResourceModel, operation rustfs.SiteReplicationRepairOperation) {
	model.Status = types.StringValue(operation.Status)
	model.CreatedAt = nullableRFC3339(operation.CreatedAt)
	model.UpdatedAt = nullableRFC3339(operation.UpdatedAt)
	model.CompletedAt = nullableRFC3339(operation.CompletedAt)
	model.Sites = siteReplicationRepairSitesValue(operation.Sites)
}

func nullableRFC3339(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func siteReplicationRepairSitesAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"deployment_id": types.StringType,
		"name":          types.StringType,
		"family":        types.StringType,
		"planned":       types.Int64Type,
		"succeeded":     types.Int64Type,
		"failed":        types.Int64Type,
		"retry_events":  types.Int64Type,
		"errors":        types.ListType{ElemType: types.StringType},
	}
}

// siteReplicationRepairSitesValue flattens the server's site -> family map into
// one row per family, ordered by deployment ID and family name so the state is
// stable across refreshes.
func siteReplicationRepairSitesValue(sites map[string]rustfs.SiteReplicationRepairSite) types.List {
	attrTypes := siteReplicationRepairSitesAttrTypes()

	deploymentIDs := make([]string, 0, len(sites))
	for deploymentID := range sites {
		deploymentIDs = append(deploymentIDs, deploymentID)
	}
	sort.Strings(deploymentIDs)

	rows := make([]attr.Value, 0, len(sites))
	for _, key := range deploymentIDs {
		site := sites[key]
		deploymentID := site.DeploymentID
		if deploymentID == "" {
			deploymentID = key
		}

		families := make([]string, 0, len(site.Families))
		for family := range site.Families {
			families = append(families, family)
		}
		sort.Strings(families)

		if len(families) == 0 {
			families = []string{""}
		}

		for _, family := range families {
			progress := site.Families[family]
			errors := make([]attr.Value, 0, len(progress.Errors))
			for _, message := range progress.Errors {
				errors = append(errors, types.StringValue(message))
			}
			rows = append(rows, types.ObjectValueMust(attrTypes, map[string]attr.Value{
				"deployment_id": types.StringValue(deploymentID),
				"name":          types.StringValue(site.Name),
				"family":        types.StringValue(family),
				"planned":       types.Int64Value(progress.Planned),
				"succeeded":     types.Int64Value(progress.Succeeded),
				"failed":        types.Int64Value(progress.Failed),
				"retry_events":  types.Int64Value(progress.RetryEvents),
				"errors":        types.ListValueMust(types.StringType, errors),
			}))
		}
	}

	return types.ListValueMust(types.ObjectType{AttrTypes: attrTypes}, rows)
}

func init() {
	registerResource(NewSiteReplicationRepairResource)
}
