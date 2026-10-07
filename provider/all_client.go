package provider

import (
	"github.com/minio/minio-go/v7"
	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

type AllClient struct {
	Minio      *minio.Client
	RustClient client.RustfsAdmin
}
