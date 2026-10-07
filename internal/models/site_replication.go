package models

import "github.com/hashicorp/terraform-plugin-framework/types"

type SiteReplicationRessourceModel struct {
	Name          types.String `tfsdk:"name"`
	Endpoint      types.String `tfsdk:"endpoint"`
	AccessKey     types.String `tfsdk:"access_key"`
	SecretKey     types.String `tfsdk:"secret_key"`
	SkipTLSVerify types.Bool   `tfsdk:"skip_tls_verify"`
	CACertPEM     types.String `tfsdk:"ca_cert_pem"`
}
