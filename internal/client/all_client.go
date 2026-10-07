package client

import "github.com/minio/minio-go/v7"

// AllClient bundles the two clients the provider hands to resources and data
// sources: the S3 API client (minio-go) and the RustFS admin API client.
type AllClient struct {
	Minio      *minio.Client
	RustClient RustfsAdmin
}
