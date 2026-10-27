// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                   = &IntegrityJobResource{}
	_ resource.ResourceWithConfigure      = &IntegrityJobResource{}
	_ resource.ResourceWithImportState    = &IntegrityJobResource{}
	_ resource.ResourceWithValidateConfig = &IntegrityJobResource{}
)

// The byte-sized limits are int64 so their values survive a 32-bit build:
// 5 GiB does not fit in an int on arm or 386, and fmt.Sprintf would then
// refuse the constant at compile time.
const (
	integrityJobDefaultBytesPerSecond int64 = 8 * 1024 * 1024
	integrityJobMinBytesPerSecond     int64 = 64 * 1024
	integrityJobMaxBytesPerSecond     int64 = 1024 * 1024 * 1024
	integrityJobDefaultMaxObjectBytes int64 = 5 * 1024 * 1024 * 1024
	integrityJobMaxObjectBytes        int64 = 5 * 1024 * 1024 * 1024
	integrityJobMaxItems                    = 64
)

// IntegrityJobResource starts one bounded data-integrity job and records the
// durable job it returns.
//
// RustFS creates a job in state "paused" and only runs it after an explicit
// resume through the control route; Create issues both calls. Every
// configurable attribute carries RequiresReplace, so changing one starts a new
// job. Destroy sends cancel and never rolls back bytes already verified or
// migrated.
type IntegrityJobResource struct {
	client *AllClient
}

type IntegrityJobResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Bucket            types.String `tfsdk:"bucket"`
	Mode              types.String `tfsdk:"mode"`
	Items             types.List   `tfsdk:"items"`
	BytesPerSecond    types.Int64  `tfsdk:"bytes_per_second"`
	MaxObjectBytes    types.Int64  `tfsdk:"max_object_bytes"`
	Confirm           types.String `tfsdk:"confirm"`
	State             types.String `tfsdk:"state"`
	Revision          types.Int64  `tfsdk:"revision"`
	BucketIncarnation types.String `tfsdk:"bucket_incarnation"`
	Results           types.List   `tfsdk:"results"`
	LastError         types.String `tfsdk:"last_error"`
}

type IntegrityJobItemModel struct {
	Key            types.String `tfsdk:"key"`
	VersionID      types.String `tfsdk:"version_id"`
	ExpectedSHA256 types.String `tfsdk:"expected_sha256"`
	TargetKey      types.String `tfsdk:"target_key"`
}

var integrityJobItemAttrTypes = map[string]attr.Type{
	"key":             types.StringType,
	"version_id":      types.StringType,
	"expected_sha256": types.StringType,
	"target_key":      types.StringType,
}

var integrityJobResultAttrTypes = map[string]attr.Type{
	"state":              types.StringType,
	"source_fingerprint": types.StringType,
	"expected_sha256":    types.StringType,
	"evidence_source":    types.StringType,
	"source_size":        types.Int64Type,
	"target_version_id":  types.StringType,
	"detail":             types.StringType,
}

func NewIntegrityJobResource() resource.Resource {
	return &IntegrityJobResource{}
}

func (r *IntegrityJobResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_integrity_job"
}

func (r *IntegrityJobResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	description := "Start a bounded RustFS data-integrity job for explicit object versions " +
		"(POST /rustfs/admin/v3/integrity/{bucket}/jobs) and keep the durable job the server returns.\n\n" +
		"### This resource reads object data\n\n" +
		"An `audit` job reads the listed object versions and verifies their integrity evidence; a `migrate` " +
		"job reads them and writes a verified copy under `target_key`. The read is bounded by the item list " +
		"(1 to 64 versions) and `max_object_bytes`, but 64 versions at the limit is heavy I/O. `confirm` must " +
		"repeat the bucket name so a job cannot be started against the wrong bucket by accident.\n\n" +
		"### Lifecycle\n\n" +
		"RustFS creates a job paused and runs it only after a resume. `Create` creates the job and resumes it " +
		"in one apply; `Read` reports the current `state`, `revision` and per-item `results`. Every configurable " +
		"attribute carries `RequiresReplace`, so changing one starts a **new** job. One integrity worker runs " +
		"per cluster, so a second resume can be refused with `SlowDown` while another job is active.\n\n" +
		"`Delete` sends `cancel` through the control route. Cancelling is idempotent and does not roll back " +
		"bytes already verified or migrated; a terminal job is returned unchanged. There is no route that " +
		"deletes a job record, so destroy only stops the work and forgets the record.\n\n" +
		"### Readiness and requirements\n\n" +
		"`migrate` requires `RUSTFS_SHARD_INTEGRITY_WRITE` and `RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED` on every " +
		"node; without them the apply fails before a job is created. Read the " +
		"`rustfs_integrity_readiness` data source to inspect those switches. The routes are implemented in " +
		"RustFS 1.0.1 through 1.0.3; a server that does not implement them produces a version-aware diagnostic.\n\n" +
		"### Import\n\n" +
		"Import with `<bucket>/<job_id>`, because the status route is bucket-scoped."

	resp.Schema = schema.Schema{
		Description:         "Start a bounded RustFS data-integrity job",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Durable job id of the integrity job.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Bucket that holds the listed object versions. Changing it starts a new job.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"mode": schema.StringAttribute{
				Required: true,
				Description: "`audit` reads and verifies the listed versions; `migrate` also writes a verified copy " +
					"under each item's `target_key`. Changing it starts a new job.",
				Validators: []validator.String{
					stringvalidator.OneOf(rustfs.IntegrityModeAudit, rustfs.IntegrityModeMigrate),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"items": schema.ListNestedAttribute{
				Required: true,
				Description: "Explicit source versions the job covers. RustFS accepts 1 to 64 items; the job never " +
					"scans a prefix or the rest of the bucket. Changing the list starts a new job.",
				Validators: []validator.List{
					listvalidator.SizeBetween(1, integrityJobMaxItems),
				},
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							Required:    true,
							Description: "Object key of the source version.",
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
						"version_id": schema.StringAttribute{
							Optional: true,
							Description: "S3 version selector. Omit it for the current version, or use the literal " +
								"`null` for the null version slot.",
						},
						"expected_sha256": schema.StringAttribute{
							Optional: true,
							Description: "Canonical Base64 SHA-256 supplied by an independent source. When set, the " +
								"job compares the object against this value instead of trusting the stored checksum.",
						},
						"target_key": schema.StringAttribute{
							Optional: true,
							Description: "Destination key for `migrate`, distinct from every source and every other " +
								"target. It must be omitted for `audit`.",
						},
					},
				},
			},
			"bytes_per_second": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(integrityJobDefaultBytesPerSecond),
				Description: fmt.Sprintf("Bounds each source read, destination PUT and verification stream in bytes "+
					"per second. RustFS accepts %d to %d; the default is %d (8 MiB/s). Changing it starts a new job.",
					integrityJobMinBytesPerSecond, integrityJobMaxBytesPerSecond, integrityJobDefaultBytesPerSecond),
				Validators: []validator.Int64{
					int64validator.Between(integrityJobMinBytesPerSecond, integrityJobMaxBytesPerSecond),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"max_object_bytes": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(integrityJobDefaultMaxObjectBytes),
				Description: fmt.Sprintf("Largest object the job stages. RustFS accepts 1 to %d bytes; the default "+
					"is the maximum. Changing it starts a new job.", integrityJobMaxObjectBytes),
				Validators: []validator.Int64{
					int64validator.Between(1, integrityJobMaxObjectBytes),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"confirm": schema.StringAttribute{
				Required: true,
				Description: "Must repeat `bucket`. This is the cost guard: a job reads every listed version, so " +
					"starting one against the wrong bucket is expensive. Changing it starts a new job.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"state": schema.StringAttribute{
				Computed: true,
				Description: "Job state reported by the server: `paused`, `running`, `pause_requested`, " +
					"`cancel_requested`, `cancelled`, `complete` or `failed`.",
			},
			"revision": schema.Int64Attribute{
				Computed:    true,
				Description: "Monotonic job revision reported by the server.",
			},
			"bucket_incarnation": schema.StringAttribute{
				Computed: true,
				Description: "Incarnation UUID of the bucket the job was created against. A job is fenced to that " +
					"generation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"results": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Per-item outcome, in the same order as `items`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"state": schema.StringAttribute{
							Computed: true,
							Description: "Item state: `pending`, `prepared`, `verified`, `migrated`, `unsupported`, " +
								"`unavailable`, `mismatch`, `stale` or `conflict`.",
						},
						"source_fingerprint": schema.StringAttribute{
							Computed:    true,
							Description: "Fingerprint of the source metadata the item was verified against.",
						},
						"expected_sha256": schema.StringAttribute{
							Computed:    true,
							Description: "Expected SHA-256 recorded for the item, when the job had one.",
						},
						"evidence_source": schema.StringAttribute{
							Computed:    true,
							Description: "Where the verification evidence came from.",
						},
						"source_size": schema.Int64Attribute{
							Computed:    true,
							Description: "Source object size in bytes, once observed.",
						},
						"target_version_id": schema.StringAttribute{
							Computed:    true,
							Description: "Version id the migrated copy received.",
						},
						"detail": schema.StringAttribute{
							Computed:    true,
							Description: "Human-readable detail for a non-success item.",
						},
					},
				},
			},
			"last_error": schema.StringAttribute{
				Computed:    true,
				Description: "Why the job failed, when the server reports one.",
			},
		},
	}
}

func (r *IntegrityJobResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *IntegrityJobResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config IntegrityJobResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Bucket.IsUnknown() && !config.Confirm.IsUnknown() &&
		config.Confirm.ValueString() != config.Bucket.ValueString() {
		resp.Diagnostics.AddAttributeError(
			path.Root("confirm"),
			"Invalid integrity job configuration",
			"`confirm` must repeat the bucket name ("+config.Bucket.ValueString()+"); a job reads every listed "+
				"version, so the guard prevents starting one against the wrong bucket.",
		)
	}
	if config.Items.IsNull() || config.Items.IsUnknown() || config.Mode.IsUnknown() {
		return
	}

	var items []IntegrityJobItemModel
	resp.Diagnostics.Append(config.Items.ElementsAs(ctx, &items, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	migrate := config.Mode.ValueString() == rustfs.IntegrityModeMigrate
	for index, item := range items {
		if item.TargetKey.IsUnknown() {
			continue
		}
		switch {
		case migrate && item.TargetKey.IsNull():
			resp.Diagnostics.AddAttributeError(
				path.Root("items").AtListIndex(index).AtName("target_key"),
				"Invalid integrity job configuration",
				"A `migrate` item needs `target_key`: the destination key for the verified copy.",
			)
		case !migrate && !item.TargetKey.IsNull():
			resp.Diagnostics.AddAttributeError(
				path.Root("items").AtListIndex(index).AtName("target_key"),
				"Invalid integrity job configuration",
				"`target_key` is only valid for `mode = \"migrate\"`; an `audit` item must omit it.",
			)
		}
	}
}

func (r *IntegrityJobResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan IntegrityJobResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mode := plan.Mode.ValueString()
	if mode == rustfs.IntegrityModeMigrate {
		readiness, err := r.client.RustClient.IntegrityReadiness(ctx)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error reading data-integrity readiness",
				"Could not check whether protected writes are activated before creating the job: "+err.Error(),
			)
			return
		}
		if !readiness.NewWritesEnabledHere {
			resp.Diagnostics.AddError(
				"Data-integrity writes are not activated",
				"`mode = \"migrate\"` needs `new_writes_enabled_here` from GET /rustfs/admin/v3/integrity/readiness. "+
					"The server reports write_requested="+strconv.FormatBool(readiness.WriteRequested)+
					", fleet_operator_attested="+strconv.FormatBool(readiness.FleetOperatorAttested)+
					", fleet_capability_verified="+strconv.FormatBool(readiness.FleetCapabilityVerified)+
					". Set RUSTFS_SHARD_INTEGRITY_WRITE=true and RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED=true on "+
					"every node before starting a migration job.",
			)
			return
		}
	}

	var items []IntegrityJobItemModel
	resp.Diagnostics.Append(plan.Items.ElementsAs(ctx, &items, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	request := rustfs.IntegrityJobRequest{
		Mode:           mode,
		Items:          make([]rustfs.IntegrityItemRequest, 0, len(items)),
		BytesPerSecond: plan.BytesPerSecond.ValueInt64(),
		MaxObjectBytes: plan.MaxObjectBytes.ValueInt64(),
	}
	for _, item := range items {
		request.Items = append(request.Items, rustfs.IntegrityItemRequest{
			Key:            item.Key.ValueString(),
			VersionID:      item.VersionID.ValueStringPointer(),
			ExpectedSHA256: item.ExpectedSHA256.ValueStringPointer(),
			TargetKey:      item.TargetKey.ValueStringPointer(),
		})
	}

	bucket := plan.Bucket.ValueString()
	job, err := r.client.RustClient.CreateIntegrityJob(ctx, bucket, request)
	if err != nil {
		addIntegrityJobError(&resp.Diagnostics, "Error creating integrity job", "create a job for bucket "+bucket, err)
		return
	}
	if job.ID == "" {
		resp.Diagnostics.AddError(
			"Error creating integrity job",
			"RustFS accepted the job without an id, so no state can be tracked. Inspect the bucket's integrity "+
				"jobs before retrying.",
		)
		return
	}

	// The server creates the job paused and only runs it after a resume. If the
	// resume is refused (another worker is active, or the job changed), cancel
	// the paused job so a failed apply does not leave a stuck record.
	job, err = r.client.RustClient.ControlIntegrityJob(ctx, bucket, job.ID, rustfs.IntegrityControlResume)
	if err != nil {
		resumeErr := err
		var cancelErr error
		_, cancelErr = r.client.RustClient.ControlIntegrityJob(ctx, bucket, job.ID, rustfs.IntegrityControlCancel)
		detail := "Created job " + job.ID + " but RustFS refused to resume it: " + resumeErr.Error() + "."
		if cancelErr == nil {
			detail += " The paused job was cancelled."
		} else {
			detail += " Cancelling the paused job also failed: " + cancelErr.Error() + "."
		}
		if rustfs.IsAPIErrorCode(resumeErr, "SlowDown") {
			detail += " Retry once no other integrity worker is active."
		} else {
			detail += " Inspect the job's state before retrying."
		}
		resp.Diagnostics.AddError("Error starting integrity job", detail)
		return
	}

	applyIntegrityJobToModel(job, &plan)
	tflog.Trace(ctx, "started a data-integrity job", map[string]any{"job_id": job.ID})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *IntegrityJobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state IntegrityJobResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID := state.ID.ValueString()
	bucket := state.Bucket.ValueString()
	if jobID == "" || bucket == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	job, err := r.client.RustClient.IntegrityJob(ctx, bucket, jobID)
	if err != nil {
		// A job the server no longer holds is NoSuchKey on an implemented route;
		// PreconditionFailed means the bucket generation changed and the record no
		// longer applies. A missing route is a different failure and must not drop
		// the record.
		if integrityJobStale(err) {
			tflog.Debug(ctx, "integrity job is gone; removing it from state", map[string]any{"job_id": jobID})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading integrity job",
			"Could not read job "+jobID+" in bucket "+bucket+": "+err.Error(),
		)
		return
	}

	applyIntegrityJobToModel(job, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *IntegrityJobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every configurable attribute carries RequiresReplace, so Terraform never
	// calls Update for a real change. Copy the plan through so a framework
	// caller that does call it still gets a consistent state.
	var plan IntegrityJobResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *IntegrityJobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state IntegrityJobResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID := state.ID.ValueString()
	bucket := state.Bucket.ValueString()
	if jobID == "" || bucket == "" {
		return
	}

	_, err := r.client.RustClient.ControlIntegrityJob(ctx, bucket, jobID, rustfs.IntegrityControlCancel)
	if err != nil {
		// A stale generation or a concurrent revision change both answer
		// PreconditionFailed. Re-read once: only the former means this record no
		// longer names a cancellable job. A concurrent change gets one retry.
		if rustfs.IsAPIErrorCode(err, "PreconditionFailed") {
			if _, readErr := r.client.RustClient.IntegrityJob(ctx, bucket, jobID); integrityJobStale(readErr) {
				tflog.Debug(ctx, "integrity job no longer applies; nothing to cancel", map[string]any{"job_id": jobID})
				return
			}
			if _, err = r.client.RustClient.ControlIntegrityJob(ctx, bucket, jobID, rustfs.IntegrityControlCancel); err == nil {
				tflog.Trace(ctx, "cancelled a data-integrity job after a concurrent change", map[string]any{"job_id": jobID})
				return
			}
		}
		if integrityJobStale(err) {
			tflog.Debug(ctx, "integrity job is already gone", map[string]any{"job_id": jobID})
			return
		}
		resp.Diagnostics.AddError(
			"Error cancelling integrity job",
			"Could not cancel job "+jobID+" in bucket "+bucket+": "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "cancelled a data-integrity job", map[string]any{"job_id": jobID})
}

func (r *IntegrityJobResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected the import ID to be <bucket>/<job_id>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func addIntegrityJobError(diags *diag.Diagnostics, summary, action string, err error) {
	if rustfs.IsUnsupportedFeature(err) {
		diags.AddError(
			"Data-integrity jobs are not available on this RustFS server",
			err.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
				"rustfs_integrity_job resource from the configuration.",
		)
		return
	}
	diags.AddError(summary, "Could not "+action+": "+err.Error())
}

func applyIntegrityJobToModel(job rustfs.IntegrityJob, model *IntegrityJobResourceModel) {
	model.ID = types.StringValue(job.ID)
	model.Bucket = types.StringValue(job.Bucket)
	// confirm is defined as the bucket name, so a refresh (including import)
	// restores it without inventing a value.
	model.Confirm = types.StringValue(job.Bucket)
	if job.Request.Mode != "" {
		// The request is immutable and part of the job record, so it is also
		// what makes an imported job converge with its configuration.
		model.Mode = types.StringValue(job.Request.Mode)
		model.Items = integrityJobItemsValue(job.Request.Items)
		model.BytesPerSecond = types.Int64Value(job.Request.BytesPerSecond)
		model.MaxObjectBytes = types.Int64Value(job.Request.MaxObjectBytes)
	}
	model.State = types.StringValue(job.State)
	model.Revision = types.Int64Value(job.Revision)
	model.BucketIncarnation = types.StringValue(job.BucketIncarnation)
	model.Results = integrityJobResultsValue(job.Results)
	if job.LastError == nil {
		model.LastError = types.StringNull()
	} else {
		model.LastError = types.StringValue(*job.LastError)
	}
}

func integrityJobItemsValue(items []rustfs.IntegrityItemRequest) types.List {
	values := make([]attr.Value, 0, len(items))
	for _, item := range items {
		values = append(values, types.ObjectValueMust(integrityJobItemAttrTypes, map[string]attr.Value{
			"key":             types.StringValue(item.Key),
			"version_id":      stringPointerValue(item.VersionID),
			"expected_sha256": stringPointerValue(item.ExpectedSHA256),
			"target_key":      stringPointerValue(item.TargetKey),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: integrityJobItemAttrTypes}, values)
}

// integrityJobStale reports whether the server says this resource no longer
// names a job it can act on: the record is gone, or the bucket generation
// changed.
func integrityJobStale(err error) bool {
	return rustfs.IsAPIErrorCode(err, "NoSuchKey", "PreconditionFailed")
}

func integrityJobResultsValue(results []rustfs.IntegrityItemResult) types.List {
	values := make([]attr.Value, 0, len(results))
	for _, result := range results {
		values = append(values, types.ObjectValueMust(integrityJobResultAttrTypes, map[string]attr.Value{
			"state":              types.StringValue(result.State),
			"source_fingerprint": stringPointerValue(result.SourceFingerprint),
			"expected_sha256":    stringPointerValue(result.ExpectedSHA256),
			"evidence_source":    stringPointerValue(result.EvidenceSource),
			"source_size":        int64PointerValue(result.SourceSize),
			"target_version_id":  stringPointerValue(result.TargetVersionID),
			"detail":             stringPointerValue(result.Detail),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: integrityJobResultAttrTypes}, values)
}

func stringPointerValue(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func int64PointerValue(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

func init() {
	registerResource(NewIntegrityJobResource)
}
