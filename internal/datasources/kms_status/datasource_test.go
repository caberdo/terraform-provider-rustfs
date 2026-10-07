package kms_status

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/weinmann-emt/terraform-provider-rustfs/internal/client"
)

func stringPtr(s string) *string { return &s }

func TestKmsStatusDataSourceSchema(t *testing.T) {
	d := NewKmsStatusDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.TODO(), datasource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"backend_type", "backend_status", "cache_enabled", "cache_stats",
		"default_key_id", "capabilities", "cluster_config",
	} {
		a, ok := attrs[name]
		if !ok {
			t.Errorf("expected %s attribute", name)
			continue
		}
		if !a.IsComputed() {
			t.Errorf("expected %s to be computed", name)
		}
		if a.GetDescription() == "" {
			t.Errorf("expected %s to have a description", name)
		}
	}
}

func TestKmsStatusDataSourceMetadata(t *testing.T) {
	d := NewKmsStatusDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.TODO(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_kms_status" {
		t.Errorf("expected rustfs_kms_status, got %s", resp.TypeName)
	}
}

func TestKmsStatusModelFromStatus_Full(t *testing.T) {
	status := &client.KmsStatus{
		BackendType:   "local",
		BackendStatus: "healthy",
		CacheEnabled:  true,
		CacheStats: &client.KmsCacheStats{
			HitCount:      1,
			MissCount:     2,
			EntryCount:    3,
			EvictionCount: 4,
		},
		DefaultKeyID: stringPtr("key-01"),
		Capabilities: map[string]bool{"encrypt": true},
		ClusterConfig: &client.KmsClusterConfig{
			Consistent: true,
			Nodes: []client.KmsClusterNode{
				{Host: "local", ConfigFingerprint: stringPtr("abc123")},
			},
		},
	}

	model, diags := kmsStatusModelFromStatus(context.Background(), status)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.BackendType.ValueString() != "local" {
		t.Errorf("expected backend_type local, got %s", model.BackendType.ValueString())
	}
	if model.BackendStatus.ValueString() != "healthy" {
		t.Errorf("expected backend_status healthy, got %s", model.BackendStatus.ValueString())
	}
	if !model.CacheEnabled.ValueBool() {
		t.Error("expected cache_enabled true")
	}
	if model.CacheStats == nil {
		t.Fatal("expected cache_stats")
	}
	if model.CacheStats.HitCount.ValueInt64() != 1 || model.CacheStats.MissCount.ValueInt64() != 2 ||
		model.CacheStats.EntryCount.ValueInt64() != 3 || model.CacheStats.EvictionCount.ValueInt64() != 4 {
		t.Errorf("unexpected cache_stats: %+v", model.CacheStats)
	}
	if model.DefaultKeyID.ValueString() != "key-01" {
		t.Errorf("expected key-01, got %s", model.DefaultKeyID.ValueString())
	}
	if model.Capabilities.IsNull() {
		t.Fatal("expected capabilities map")
	}
	expected := types.MapValueMust(types.BoolType, map[string]attr.Value{"encrypt": types.BoolValue(true)})
	if !model.Capabilities.Equal(expected) {
		t.Errorf("expected capabilities %s, got %s", expected, model.Capabilities)
	}
	if model.ClusterConfig == nil {
		t.Fatal("expected cluster_config")
	}
	if !model.ClusterConfig.Consistent.ValueBool() {
		t.Error("expected cluster_config consistent true")
	}
	if len(model.ClusterConfig.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(model.ClusterConfig.Nodes))
	}
	node := model.ClusterConfig.Nodes[0]
	if node.Host.ValueString() != "local" {
		t.Errorf("expected host local, got %s", node.Host.ValueString())
	}
	if node.ConfigFingerprint.ValueString() != "abc123" {
		t.Errorf("expected fingerprint abc123, got %s", node.ConfigFingerprint.ValueString())
	}
	if !node.Error.IsNull() {
		t.Errorf("expected null error, got %s", node.Error)
	}
}

func TestKmsStatusModelFromStatus_OptionalAbsent(t *testing.T) {
	status := &client.KmsStatus{
		BackendType:   "aws",
		BackendStatus: "error",
		CacheEnabled:  false,
	}

	model, diags := kmsStatusModelFromStatus(context.Background(), status)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if model.CacheStats != nil {
		t.Errorf("expected nil cache_stats, got %+v", model.CacheStats)
	}
	if !model.DefaultKeyID.IsNull() {
		t.Errorf("expected null default_key_id, got %s", model.DefaultKeyID)
	}
	if !model.Capabilities.IsNull() {
		t.Errorf("expected null capabilities, got %s", model.Capabilities)
	}
	if model.ClusterConfig != nil {
		t.Errorf("expected nil cluster_config, got %+v", model.ClusterConfig)
	}
}
