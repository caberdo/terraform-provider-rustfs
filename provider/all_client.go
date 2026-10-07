package provider

import "github.com/weinmann-emt/terraform-provider-rustfs/internal/client"

// AllClient is kept as an alias of client.AllClient so the flat provider
// package keeps compiling while resources and data sources move into their own
// packages under internal/.
type AllClient = client.AllClient
