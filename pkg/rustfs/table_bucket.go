// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// TableBucket is the S3 Tables catalog state RustFS reports for one bucket.
//
// The route lives outside the admin API base, so every call goes through
// DoDirectRequest. The field set mirrors a live GET payload (verified on
// RustFS 1.0.1, 1.0.2 and 1.0.3); server-only fields the provider does not
// model are intentionally absent.
type TableBucket struct {
	TableBucket         string `json:"table-bucket"`
	Enabled             bool   `json:"enabled"`
	CatalogType         string `json:"catalog-type"`
	Warehouse           string `json:"warehouse"`
	CatalogEntryPresent bool   `json:"catalog-entry-present"`
	CatalogURI          string `json:"catalog-uri"`
}

// tableBucket issues one request against /iceberg/v1/buckets/<bucket> and
// decodes the catalog state the server returns. DELETE answers 200 with the
// state on 1.0.2+, and both PUT and GET answer 200, so only an empty body is
// tolerated as "no state".
func (c *RustfsAdmin) tableBucket(ctx context.Context, method, bucket string) (TableBucket, error) {
	reqData := RequestData{
		Method:  method,
		RelPath: "iceberg/v1/buckets/" + url.PathEscape(bucket),
	}

	resp, err := c.DoDirectRequest(ctx, reqData)
	if err != nil {
		return TableBucket{}, err
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusNoContent {
		return TableBucket{}, nil
	}

	var tableBucket TableBucket
	if err := json.NewDecoder(resp.Body).Decode(&tableBucket); err != nil {
		return TableBucket{}, err
	}
	return tableBucket, nil
}

// EnableTableBucket turns an existing bucket into a table bucket by enabling
// the built-in Iceberg REST catalog on it (PUT /iceberg/v1/buckets/<bucket>).
func (c *RustfsAdmin) EnableTableBucket(ctx context.Context, bucket string) (TableBucket, error) {
	return c.tableBucket(ctx, http.MethodPut, bucket)
}

// GetTableBucket reads the catalog state of one bucket
// (GET /iceberg/v1/buckets/<bucket>).
func (c *RustfsAdmin) GetTableBucket(ctx context.Context, bucket string) (TableBucket, error) {
	return c.tableBucket(ctx, http.MethodGet, bucket)
}

// DisableTableBucket turns the catalog switch off again
// (DELETE /iceberg/v1/buckets/<bucket>). RustFS 1.0.1 registers the route but
// answers 501 NotImplemented, which is returned as an
// *UnsupportedFeatureError naming the route and the server version.
func (c *RustfsAdmin) DisableTableBucket(ctx context.Context, bucket string) (TableBucket, error) {
	tableBucket, err := c.tableBucket(ctx, http.MethodDelete, bucket)
	if err != nil {
		return TableBucket{}, c.featureError(ctx, err, FeatureTableBucket)
	}
	return tableBucket, nil
}

func init() {
	registerFeature(FeatureTableBucket, featureSpec{
		route: "DELETE /iceberg/v1/buckets/{bucket}",
		// Probed live: 1.0.1 answers 501 NotImplemented for the disable route,
		// while 1.0.2 and 1.0.3 disable the catalog. Enable and read work on
		// all three.
		minVersion: &Version{Major: 1, Minor: 0, Patch: 2, Raw: "1.0.2"},
	})
}
