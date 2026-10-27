// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &BatchJobsDataSource{}

type BatchJobsDataSource struct {
	client *AllClient
}

type BatchJobsDataSourceModel struct {
	JobType types.String `tfsdk:"job_type"`
	JobID   types.String `tfsdk:"job_id"`
	ID      types.String `tfsdk:"id"`
	Jobs    types.List   `tfsdk:"jobs"`
	Job     types.String `tfsdk:"job"`
	Status  types.String `tfsdk:"status"`
	Found   types.Bool   `tfsdk:"found"`
	JSON    types.String `tfsdk:"json"`
}

func NewBatchJobsDataSource() datasource.DataSource {
	return &BatchJobsDataSource{}
}

func (d *BatchJobsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_batch_jobs"
}

func (d *BatchJobsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	description := "Lists the MinIO-compatible batch jobs RustFS reports. RustFS implements the batch-job admin " +
		"surface without a job execution backend: `list-jobs` always answers with an empty list, and `describe-job` " +
		"and `status-job` always report that the requested job does not exist. Job documents are exposed as the raw " +
		"JSON the server returned (`jobs`, `job`, `status`) instead of an invented Terraform schema, because the " +
		"MinIO batch-job payload is large, version-dependent, and not owned by this provider. This data source is " +
		"read-only; job submission stays an `mc`/`rc` operation."
	resp.Schema = schema.Schema{
		Description:         "List RustFS batch jobs",
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"job_type": schema.StringAttribute{
				Optional: true,
				Description: "Only list jobs of this type. The server recognises `replicate`, `keyrotate` and " +
					"`expire`; leave unset to list every type.",
				Validators: []validator.String{
					stringvalidator.OneOf("replicate", "keyrotate", "expire"),
				},
			},
			"job_id": schema.StringAttribute{
				Optional: true,
				Description: "Describe and read the status of one job by id. When set, `job`, `status` and " +
					"`found` are populated. RustFS has no job execution backend, so a job id cannot exist and " +
					"`found` is false rather than an error.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier, derived from the query.",
			},
			"jobs": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Raw JSON document of every listed job, exactly as the server returned it.",
			},
			"job": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON job definition returned by `describe-job`; null unless `job_id` is set and the job exists.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON job status returned by `status-job`; null unless `job_id` is set and the job exists.",
			},
			"found": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the requested `job_id` exists; null when no `job_id` was given.",
			},
			"json": schema.StringAttribute{
				Computed:    true,
				Description: "Raw JSON `list-jobs` response as returned by the RustFS admin API.",
			},
		},
	}
}

func (d *BatchJobsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *AllClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *BatchJobsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config BatchJobsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobType := config.JobType.ValueString()
	listed, err := d.client.RustClient.ListBatchJobs(ctx, jobType)
	if err != nil {
		d.addCapabilityOrReadError(resp, err, "Error reading batch jobs")
		return
	}

	jobID := config.JobID.ValueString()

	config.ID = types.StringValue("batch-jobs")
	if jobType != "" {
		config.ID = types.StringValue("batch-jobs/" + jobType)
	}
	if jobID != "" {
		config.ID = types.StringValue(config.ID.ValueString() + "/" + jobID)
	}
	config.Jobs = batchJobDocumentsValue(listed.Jobs)
	if len(listed.Raw) > 0 {
		config.JSON = types.StringValue(string(listed.Raw))
	} else {
		config.JSON = types.StringValue("{}")
	}

	if jobID == "" {
		config.Job = types.StringNull()
		config.Status = types.StringNull()
		config.Found = types.BoolNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
		return
	}

	job, err := d.client.RustClient.DescribeBatchJob(ctx, jobID)
	if err != nil {
		if rustfs.IsNoSuchBatchJob(err) {
			config.Job = types.StringNull()
			config.Status = types.StringNull()
			config.Found = types.BoolValue(false)
			resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
			return
		}
		d.addCapabilityOrReadError(resp, err, "Error reading batch job")
		return
	}

	status, err := d.client.RustClient.StatusBatchJob(ctx, jobID)
	if err != nil {
		if rustfs.IsNoSuchBatchJob(err) {
			// describe-job returned the job, so keep that document instead of
			// reporting the job as missing. A server-side race or a partially
			// implemented engine is real information, not a clean "not found".
			config.Job = types.StringValue(string(job))
			config.Status = types.StringNull()
			config.Found = types.BoolValue(true)
			resp.Diagnostics.AddWarning(
				"Batch job status is no longer available",
				"describe-job returned job "+jobID+", but status-job reported that it does not exist. "+
					"The job document is kept in state and status is null; refresh to read the current state.",
			)
			resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
			return
		}
		d.addCapabilityOrReadError(resp, err, "Error reading batch job status")
		return
	}

	config.Job = types.StringValue(string(job))
	config.Status = types.StringValue(string(status))
	config.Found = types.BoolValue(true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func (d *BatchJobsDataSource) addCapabilityOrReadError(resp *datasource.ReadResponse, err error, summary string) {
	var unsupported *rustfs.UnsupportedFeatureError
	if errors.As(err, &unsupported) {
		resp.Diagnostics.AddError(
			"Batch jobs are not available on this RustFS server",
			unsupported.Error()+". Upgrade RustFS to a release that implements the route, or remove the "+
				"rustfs_batch_jobs data source from the configuration.",
		)
		return
	}
	resp.Diagnostics.AddError(summary, "Could not read batch job data: "+err.Error())
}

func batchJobDocumentsValue(documents []json.RawMessage) types.List {
	values := make([]attr.Value, 0, len(documents))
	for _, document := range documents {
		values = append(values, types.StringValue(string(document)))
	}
	return types.ListValueMust(types.StringType, values)
}

func init() {
	registerDataSource(NewBatchJobsDataSource)
}
