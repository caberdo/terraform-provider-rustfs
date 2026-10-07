package module_switch

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/models"
)

var (
	_ resource.Resource                = &ModuleSwitchResource{}
	_ resource.ResourceWithImportState = &ModuleSwitchResource{}
)

type ModuleSwitchResource struct {
	client *client.AllClient
}

func NewModuleSwitchResource() resource.Resource {
	return &ModuleSwitchResource{}
}

func (r *ModuleSwitchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_module_switch"
}

func (r *ModuleSwitchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage RustFS feature module switches",
		MarkdownDescription: "Manage RustFS feature module switches (notify and audit modules).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Fixed identifier for the module switch set.",
			},
			"notify_enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the notify module is enabled.",
			},
			"audit_enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether the audit module is enabled.",
			},
		},
	}
}

func (r *ModuleSwitchResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*client.AllClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.AllClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *ModuleSwitchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan models.ModuleSwitchRessourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.client.RustClient.SetModuleSwitches(ModuleSwitchUpdateFromModel(plan))
	if err != nil {
		resp.Diagnostics.AddError("Error setting module switches", "Could not set module switches: "+err.Error())
		return
	}

	tflog.Trace(ctx, "created module switches")
	plan.ID = types.StringValue("module-switches")
	applyModuleSwitchState(&plan, state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ModuleSwitchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state models.ModuleSwitchRessourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	switches, err := r.client.RustClient.GetModuleSwitches()
	if err != nil {
		resp.Diagnostics.AddError("Error reading module switches", "Could not read module switches: "+err.Error())
		return
	}

	applyModuleSwitchState(&state, switches)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ModuleSwitchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan models.ModuleSwitchRessourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.client.RustClient.SetModuleSwitches(ModuleSwitchUpdateFromModel(plan))
	if err != nil {
		resp.Diagnostics.AddError("Error updating module switches", "Could not update module switches: "+err.Error())
		return
	}

	plan.ID = types.StringValue("module-switches")
	applyModuleSwitchState(&plan, state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the resource from state. The admin API exposes no reset
// endpoint for module switches, so the server switches are intentionally left
// unchanged on destroy.
func (r *ModuleSwitchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Trace(ctx, "module switches left unchanged on destroy (no DELETE endpoint)")
}

func (r *ModuleSwitchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func ModuleSwitchUpdateFromModel(model models.ModuleSwitchRessourceModel) client.ModuleSwitchUpdate {
	return client.ModuleSwitchUpdate{
		NotifyEnabled: model.NotifyEnabled.ValueBool(),
		AuditEnabled:  model.AuditEnabled.ValueBool(),
	}
}

func applyModuleSwitchState(model *models.ModuleSwitchRessourceModel, state *client.ModuleSwitchState) {
	model.NotifyEnabled = types.BoolValue(state.NotifyEnabled)
	model.AuditEnabled = types.BoolValue(state.AuditEnabled)
}
