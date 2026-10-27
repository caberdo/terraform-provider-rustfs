// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"io"
)

func (c *RustfsAdmin) ExportIam(ctx context.Context) ([]byte, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "export-iam",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	return io.ReadAll(resp.Body)
}

func (c *RustfsAdmin) ImportIam(ctx context.Context, data []byte) error {
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "import-iam",
		Content: data,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
