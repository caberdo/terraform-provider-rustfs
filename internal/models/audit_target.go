package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type AuditTargetRessourceModel struct {
	TargetType    types.String `tfsdk:"target_type"`
	TargetName    types.String `tfsdk:"target_name"`
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
}
