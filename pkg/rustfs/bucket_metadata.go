// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"io"
)

func (c *RustfsAdmin) ExportBucketMetadata(ctx context.Context) ([]byte, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "export-bucket-metadata",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	return io.ReadAll(resp.Body)
}

func (c *RustfsAdmin) ImportBucketMetadata(ctx context.Context, data []byte) error {
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "import-bucket-metadata",
		Content: data,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
