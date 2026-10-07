package replication_metrics

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

func TestReplicationMetricsDataSourceSchema(t *testing.T) {
	d := NewReplicationMetricsDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", resp.Diagnostics)
	}
	for _, attr := range []string{"bucket", "id", "replication_count", "completed_replication_size", "replica_count", "replica_size", "failed", "queued", "targets", "json"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("missing schema attribute %q", attr)
		}
	}

	targets, ok := resp.Schema.Attributes["targets"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("targets attribute is not a ListNestedAttribute")
	}
	for _, attr := range []string{"target", "replication_count", "completed_replication_size", "limit_in_bits", "current_bandwidth", "failed", "failed_replication_size", "failed_replication_count"} {
		if _, ok := targets.NestedObject.Attributes[attr]; !ok {
			t.Errorf("missing nested schema attribute %q", attr)
		}
	}

	failed, ok := resp.Schema.Attributes["failed"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("failed attribute is not a SingleNestedAttribute")
	}
	for _, attr := range []string{"last_minute", "last_hour", "totals"} {
		if _, ok := failed.Attributes[attr]; !ok {
			t.Errorf("missing nested schema attribute %q", attr)
		}
	}

	queued, ok := resp.Schema.Attributes["queued"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("queued attribute is not a SingleNestedAttribute")
	}
	for _, attr := range []string{"curr", "avg", "max", "peak"} {
		if _, ok := queued.Attributes[attr]; !ok {
			t.Errorf("missing nested schema attribute %q", attr)
		}
	}
}
