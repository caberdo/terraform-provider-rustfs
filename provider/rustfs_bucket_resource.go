// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &bucketResource{}
	_ resource.ResourceWithImportState = &bucketResource{}
)

// NewbucketResource is a helper function to simplify the provider implementation.
func NewBucketResource() resource.Resource {
	return &bucketResource{}
}

// bucketResource is the resource implementation.
type bucketResource struct {
	client *AllClient
}

type bucketResourceModel struct {
	Name                       types.String                    `tfsdk:"name"`
	ForceDestroy               types.Bool                      `tfsdk:"force_destroy"`
	ACL                        types.String                    `tfsdk:"acl"`
	BucketNamespace            types.String                    `tfsdk:"bucket_namespace"`
	ObjectOwnership            types.String                    `tfsdk:"object_ownership"`
	ObjectLockEnabledForBucket types.Bool                      `tfsdk:"object_lock_enabled_for_bucket"`
	GrantRead                  types.String                    `tfsdk:"grant_read"`
	GrantWrite                 types.String                    `tfsdk:"grant_write"`
	GrantReadACP               types.String                    `tfsdk:"grant_read_acp"`
	GrantWriteACP              types.String                    `tfsdk:"grant_write_acp"`
	GrantFullControl           types.String                    `tfsdk:"grant_full_control"`
	CreateBucketConfiguration  *createBucketConfigurationModel `tfsdk:"create_bucket_configuration"`
}

type createBucketConfigurationModel struct {
	LocationConstraint types.String `tfsdk:"location_constraint"`
	Tags               types.Map    `tfsdk:"tags"`
}

// Metadata returns the resource type name.
func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

// Schema defines the schema for the resource.
func (r *bucketResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manage S3 buckets in rustfs",
		MarkdownDescription: "Manage S3 buckets in rustfs",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the bucket",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "Delete every object, object version and incomplete multipart upload in the bucket before " +
					"destroying it. This attribute causes data loss: without it, RustFS refuses to delete a non-empty " +
					"bucket with BucketNotEmpty and the destroy fails.",
			},
			"acl": schema.StringAttribute{
				Optional: true,
				Description: "Canned ACL for the bucket (for example private, public-read, public-read-write or " +
					"authenticated-read). Applied at creation and on change; RustFS 1.0.x reports only the owner's " +
					"FULL_CONTROL grant back, so the provider cannot verify the value or detect an out-of-band ACL change.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.BucketCannedACL("").Values())...),
				},
			},
			"bucket_namespace": schema.StringAttribute{
				Optional:    true,
				Description: "Namespace the bucket is created in: account-regional or global.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.BucketNamespace("").Values())...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"object_ownership": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Object ownership setting for the bucket: BucketOwnerPreferred, ObjectWriter or BucketOwnerEnforced.",
				Validators: []validator.String{
					stringvalidator.OneOf(enumStrings(s3types.ObjectOwnership("").Values())...),
				},
			},
			"object_lock_enabled_for_bucket": schema.BoolAttribute{
				Optional:    true,
				Description: "Whether S3 Object Lock is enabled for the new bucket. This can only be set at creation time.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"grant_read": schema.StringAttribute{
				Optional: true,
				Description: "Grantee to whom the read permission on the bucket is granted. Applied at creation " +
					"and on change; RustFS 1.0.x does not report ACL grants back, so the value cannot be drift-checked.",
			},
			"grant_write": schema.StringAttribute{
				Optional: true,
				Description: "Grantee to whom the write permission on the bucket is granted. Applied at creation " +
					"and on change; RustFS 1.0.x does not report ACL grants back, so the value cannot be drift-checked.",
			},
			"grant_read_acp": schema.StringAttribute{
				Optional: true,
				Description: "Grantee to whom the read ACL permission on the bucket is granted. Applied at creation " +
					"and on change; RustFS 1.0.x does not report ACL grants back, so the value cannot be drift-checked.",
			},
			"grant_write_acp": schema.StringAttribute{
				Optional: true,
				Description: "Grantee to whom the write ACL permission on the bucket is granted. Applied at creation " +
					"and on change; RustFS 1.0.x does not report ACL grants back, so the value cannot be drift-checked.",
			},
			"grant_full_control": schema.StringAttribute{
				Optional: true,
				Description: "Grantee to whom the full control permission on the bucket is granted. Applied at creation " +
					"and on change; RustFS 1.0.x does not report ACL grants back, so the value cannot be drift-checked.",
			},
			"create_bucket_configuration": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Additional bucket creation options (region and tags).",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
				Attributes: map[string]schema.Attribute{
					"location_constraint": schema.StringAttribute{
						Optional:    true,
						Description: "Region in which the bucket is created (for example eu-west-1).",
						Validators: []validator.String{
							stringvalidator.OneOf(enumStrings(s3types.BucketLocationConstraint("").Values())...),
						},
					},
					"tags": schema.MapAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "Map of key/value tag pairs applied to the bucket at creation time.",
						PlanModifiers: []planmodifier.Map{
							mapplanmodifier.RequiresReplace(),
						},
					},
				},
			},
		},
	}
}

func (r *bucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create creates the resource and sets the initial Terraform state.
func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketResourceModel
	diags := req.Plan.Get(ctx, &plan)

	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	exists, err := bucketExists(ctx, r.client.S3, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error checking bucket",
			"Could not check bucket existence: "+err.Error(),
		)
		return
	}
	if exists {
		resp.Diagnostics.AddError(
			"Error creating bucket",
			"Already existing",
		)
		return
	}

	input := &s3.CreateBucketInput{
		Bucket: aws.String(plan.Name.ValueString()),
	}
	if v := plan.ACL.ValueString(); v != "" {
		input.ACL = s3types.BucketCannedACL(v)
	}
	if v := plan.BucketNamespace.ValueString(); v != "" {
		input.BucketNamespace = s3types.BucketNamespace(v)
	}
	if v := plan.ObjectOwnership.ValueString(); v != "" {
		input.ObjectOwnership = s3types.ObjectOwnership(v)
	}
	if !plan.ObjectLockEnabledForBucket.IsNull() && !plan.ObjectLockEnabledForBucket.IsUnknown() {
		input.ObjectLockEnabledForBucket = aws.Bool(plan.ObjectLockEnabledForBucket.ValueBool())
	}
	if v := plan.GrantRead.ValueString(); v != "" {
		input.GrantRead = aws.String(v)
	}
	if v := plan.GrantWrite.ValueString(); v != "" {
		input.GrantWrite = aws.String(v)
	}
	if v := plan.GrantReadACP.ValueString(); v != "" {
		input.GrantReadACP = aws.String(v)
	}
	if v := plan.GrantWriteACP.ValueString(); v != "" {
		input.GrantWriteACP = aws.String(v)
	}
	if v := plan.GrantFullControl.ValueString(); v != "" {
		input.GrantFullControl = aws.String(v)
	}
	if cfg := plan.CreateBucketConfiguration; cfg != nil {
		cfgInput, cfgDiags := buildCreateBucketConfiguration(ctx, cfg)
		resp.Diagnostics.Append(cfgDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		input.CreateBucketConfiguration = cfgInput
	}

	_, err = r.client.S3.CreateBucket(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating bucket",
			"Could not create bucket: "+err.Error(),
		)
		return
	}
	tflog.Trace(ctx, "created a resource")
	resp.Diagnostics.Append(r.applyServerState(ctx, &plan, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func buildCreateBucketConfiguration(ctx context.Context, cfg *createBucketConfigurationModel) (*s3types.CreateBucketConfiguration, diag.Diagnostics) {
	out := &s3types.CreateBucketConfiguration{}
	var diags diag.Diagnostics
	if v := cfg.LocationConstraint.ValueString(); v != "" {
		out.LocationConstraint = s3types.BucketLocationConstraint(v)
	}
	if !cfg.Tags.IsNull() && !cfg.Tags.IsUnknown() {
		var tagMap map[string]string
		diags.Append(cfg.Tags.ElementsAs(ctx, &tagMap, false)...)
		if diags.HasError() {
			return out, diags
		}
		keys := make([]string, 0, len(tagMap))
		for k := range tagMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Tags = append(out.Tags, s3types.Tag{Key: aws.String(k), Value: aws.String(tagMap[k])})
		}
	}
	return out, diags
}

// Read refreshes the Terraform state with the latest data.
func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.S3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(state.Name.ValueString())}); err != nil {
		if isBucketNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading bucket",
			"Could not read bucket: "+err.Error(),
		)
		return
	}

	// The ACL is deliberately not read back. RustFS 1.0.x accepts the ACL on
	// CreateBucket and PutBucketAcl but GetBucketAcl always reports only the
	// owner's FULL_CONTROL grant, so mirroring it would turn every configured
	// ACL into permanent drift. The configured values stay in state.

	// force_destroy is not server state. An imported resource has no value for
	// it, so adopt the schema default instead of leaving a null that differs
	// from a configuration that does not set it.
	if state.ForceDestroy.IsNull() {
		state.ForceDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(r.applyServerState(ctx, &state, true)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// applyServerState mirrors server-side values for the mutable/computed
// attributes into the model. Create, Read and Update all use it so the applied
// state matches the state produced by an import. refreshCreateConfig must only
// be set on the Read path: refreshing create-only configuration during
// Create/Update would produce values that differ from the plan.
func (r *bucketResource) applyServerState(ctx context.Context, model *bucketResourceModel, refreshCreateConfig bool) diag.Diagnostics {
	var diags diag.Diagnostics

	// Object ownership is the only in-place mutable attribute; when the server
	// reports it, mirror it into state. On Read always mirror the server value.
	// On Create/Update only adopt the server value when the planned value is
	// unknown, otherwise keep the planned value so a normalized server value
	// cannot produce an inconsistent result after apply.
	if out, err := r.client.S3.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{
		Bucket: aws.String(model.Name.ValueString()),
	}); err == nil && out.OwnershipControls != nil && len(out.OwnershipControls.Rules) > 0 {
		if refreshCreateConfig || model.ObjectOwnership.IsUnknown() {
			model.ObjectOwnership = types.StringValue(string(out.OwnershipControls.Rules[0].ObjectOwnership))
		}
	} else if model.ObjectOwnership.IsUnknown() {
		model.ObjectOwnership = types.StringNull()
	}

	// Only refresh create-time configuration on Read, and only for fields the
	// user actually declared, so refresh never introduces values that are absent
	// from config.
	if refreshCreateConfig && model.CreateBucketConfiguration != nil {
		if !model.CreateBucketConfiguration.LocationConstraint.IsNull() {
			if out, err := r.client.S3.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
				Bucket: aws.String(model.Name.ValueString()),
			}); err == nil && out.LocationConstraint != "" {
				model.CreateBucketConfiguration.LocationConstraint = types.StringValue(string(out.LocationConstraint))
			}
		}
		if !model.CreateBucketConfiguration.Tags.IsNull() {
			if out, err := r.client.S3.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
				Bucket: aws.String(model.Name.ValueString()),
			}); err == nil && out.TagSet != nil {
				tagMap := make(map[string]string, len(out.TagSet))
				for _, t := range out.TagSet {
					tagMap[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
				tags, tagDiags := types.MapValueFrom(ctx, types.StringType, tagMap)
				diags.Append(tagDiags...)
				model.CreateBucketConfiguration.Tags = tags
			}
		}
	}

	return diags
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state bucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The ACL is applied in place rather than by replacing the bucket: a
	// replacement would delete every object for a setting RustFS accepts but
	// does not report back (see the schema description).
	if bucketACLChanged(state, plan) {
		if _, err := r.client.S3.PutBucketAcl(ctx, buildBucketACLInput(plan)); err != nil {
			resp.Diagnostics.AddError(
				"Error updating bucket ACL",
				"Could not update the bucket ACL: "+err.Error(),
			)
			return
		}
	}

	// Object ownership is the other in-place mutable setting.
	if v := plan.ObjectOwnership.ValueString(); v != "" {
		if _, err := r.client.S3.PutBucketOwnershipControls(ctx, &s3.PutBucketOwnershipControlsInput{
			Bucket: aws.String(plan.Name.ValueString()),
			OwnershipControls: &s3types.OwnershipControls{
				Rules: []s3types.OwnershipControlsRule{
					{ObjectOwnership: s3types.ObjectOwnership(v)},
				},
			},
		}); err != nil {
			resp.Diagnostics.AddError(
				"Error updating bucket object ownership",
				"Could not update object ownership: "+err.Error(),
			)
			return
		}
	}

	resp.Diagnostics.Append(r.applyServerState(ctx, &plan, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// bucketACLChanged reports whether any ACL attribute differs between state and
// plan, so Update only issues PutBucketAcl for a real change.
func bucketACLChanged(state, plan bucketResourceModel) bool {
	return state.ACL.ValueString() != plan.ACL.ValueString() ||
		state.GrantRead.ValueString() != plan.GrantRead.ValueString() ||
		state.GrantWrite.ValueString() != plan.GrantWrite.ValueString() ||
		state.GrantReadACP.ValueString() != plan.GrantReadACP.ValueString() ||
		state.GrantWriteACP.ValueString() != plan.GrantWriteACP.ValueString() ||
		state.GrantFullControl.ValueString() != plan.GrantFullControl.ValueString()
}

// buildBucketACLInput maps the planned ACL attributes onto PutBucketAcl.
func buildBucketACLInput(plan bucketResourceModel) *s3.PutBucketAclInput {
	input := &s3.PutBucketAclInput{Bucket: aws.String(plan.Name.ValueString())}
	if v := plan.ACL.ValueString(); v != "" {
		input.ACL = s3types.BucketCannedACL(v)
	}
	if v := plan.GrantRead.ValueString(); v != "" {
		input.GrantRead = aws.String(v)
	}
	if v := plan.GrantWrite.ValueString(); v != "" {
		input.GrantWrite = aws.String(v)
	}
	if v := plan.GrantReadACP.ValueString(); v != "" {
		input.GrantReadACP = aws.String(v)
	}
	if v := plan.GrantWriteACP.ValueString(); v != "" {
		input.GrantWriteACP = aws.String(v)
	}
	if v := plan.GrantFullControl.ValueString(); v != "" {
		input.GrantFullControl = aws.String(v)
	}
	return input
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data bucketResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	bucket := data.Name.ValueString()

	// The destructive switch is opt-in per bucket: without it a non-empty
	// bucket fails with BucketNotEmpty, exactly as it always has.
	if data.ForceDestroy.ValueBool() {
		if err := r.emptyBucket(ctx, bucket); err != nil {
			resp.Diagnostics.AddError(
				"Error emptying bucket before deletion",
				"Could not empty bucket "+bucket+" before deleting it: "+err.Error(),
			)
			return
		}
	}

	_, err := r.client.S3.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		if isBucketNotFound(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting bucket",
			"Could not delete bucket, unexpected error: "+err.Error(),
		)
	}
}

// deleteObjectsBatchSize is the DeleteObjects limit per request.
const deleteObjectsBatchSize = 1000

// emptyBucket removes everything that keeps DeleteBucket from succeeding:
// in-flight multipart uploads, current objects, and on a versioned bucket the
// object versions and delete markers the object pass leaves behind. Every step
// treats a vanished bucket as "already empty".
func (r *bucketResource) emptyBucket(ctx context.Context, bucket string) error {
	if err := r.abortMultipartUploads(ctx, bucket); err != nil {
		return err
	}
	if err := r.deleteCurrentObjects(ctx, bucket); err != nil {
		return err
	}
	versioned, err := r.bucketIsVersioned(ctx, bucket)
	if err != nil {
		return err
	}
	if !versioned {
		return nil
	}
	return r.deleteObjectVersions(ctx, bucket)
}

// abortMultipartUploads aborts every unfinished upload: a leftover upload
// blocks DeleteBucket just like an object does.
func (r *bucketResource) abortMultipartUploads(ctx context.Context, bucket string) error {
	paginator := s3.NewListMultipartUploadsPaginator(r.client.S3, &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if isBucketNotFound(err) {
				return nil
			}
			return err
		}
		for _, upload := range page.Uploads {
			_, err := r.client.S3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      upload.Key,
				UploadId: upload.UploadId,
			})
			if err != nil {
				if isBucketNotFound(err) {
					return nil
				}
				// Another operator may have aborted it already.
				if isNoSuchUpload(err) {
					continue
				}
				return err
			}
		}
	}
	return nil
}

// deleteCurrentObjects deletes the listed objects of an unversioned listing.
func (r *bucketResource) deleteCurrentObjects(ctx context.Context, bucket string) error {
	paginator := s3.NewListObjectsV2Paginator(r.client.S3, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if isBucketNotFound(err) {
				return nil
			}
			return err
		}
		ids := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
		for _, object := range page.Contents {
			ids = append(ids, s3types.ObjectIdentifier{Key: object.Key})
		}
		if err := r.deleteObjects(ctx, bucket, ids); err != nil {
			return err
		}
	}
	return nil
}

// deleteObjectVersions deletes every version and delete marker. On a versioned
// bucket the object pass alone leaves delete markers behind, so this pass is
// what actually empties it.
func (r *bucketResource) deleteObjectVersions(ctx context.Context, bucket string) error {
	paginator := s3.NewListObjectVersionsPaginator(r.client.S3, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if isBucketNotFound(err) {
				return nil
			}
			return err
		}
		ids := make([]s3types.ObjectIdentifier, 0, len(page.Versions)+len(page.DeleteMarkers))
		for _, version := range page.Versions {
			ids = append(ids, s3types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
		}
		for _, marker := range page.DeleteMarkers {
			ids = append(ids, s3types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if err := r.deleteObjects(ctx, bucket, ids); err != nil {
			return err
		}
	}
	return nil
}

// deleteObjects deletes identifiers in DeleteObjects-sized batches. A per-object
// error is reported: silently ignoring it would leave the bucket non-empty and
// make the following DeleteBucket fail without saying why.
func (r *bucketResource) deleteObjects(ctx context.Context, bucket string, ids []s3types.ObjectIdentifier) error {
	for start := 0; start < len(ids); start += deleteObjectsBatchSize {
		end := start + deleteObjectsBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		out, err := r.client.S3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: ids[start:end],
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			if isBucketNotFound(err) {
				return nil
			}
			return err
		}
		if len(out.Errors) > 0 {
			first := out.Errors[0]
			return fmt.Errorf("%d object(s) could not be deleted, first: %s: %s",
				len(out.Errors), aws.ToString(first.Key), aws.ToString(first.Message))
		}
	}
	return nil
}

func (r *bucketResource) bucketIsVersioned(ctx context.Context, bucket string) (bool, error) {
	out, err := r.client.S3.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		if isBucketNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return out.Status != "", nil
}

func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func bucketExists(ctx context.Context, client *s3.Client, bucket string) (bool, error) {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return true, nil
	}
	if isBucketNotFound(err) {
		return false, nil
	}
	return false, err
}

func isBucketNotFound(err error) bool {
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var noSuchBucket *s3types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchBucket", "404":
			return true
		}
	}
	if rustfs.IsAPIErrorCode(err, "NotFound", "NoSuchBucket", "404") {
		return true
	}
	return false
}

// isNoSuchUpload reports the structured code for an already-aborted multipart
// upload. It stays narrow on purpose: a blanket "not found" match would make a
// failed abort look successful.
func isNoSuchUpload(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
		return true
	}
	if rustfs.IsAPIErrorCode(err, "NoSuchUpload") {
		return true
	}
	return strings.Contains(err.Error(), "NoSuchUpload")
}

func init() {
	registerResource(NewBucketResource)
}
