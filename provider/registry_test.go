// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// goldenResourceTypeNames is every resource the provider must register. A
// dropped or misspelled registration fails TestRegisteredResourcesMatchGolden,
// which is the only place the type list is still written down.
var goldenResourceTypeNames = []string{
	"rustfs_audit_target",
	"rustfs_bucket",
	"rustfs_bucket_accelerate",
	"rustfs_bucket_cors",
	"rustfs_bucket_durability",
	"rustfs_bucket_encryption",
	"rustfs_bucket_lifecycle_configuration",
	"rustfs_bucket_logging",
	"rustfs_bucket_metadata_backup_import",
	"rustfs_bucket_notification",
	"rustfs_bucket_object_lock",
	"rustfs_bucket_on_demand_migration",
	"rustfs_bucket_policy",
	"rustfs_bucket_public_access_block",
	"rustfs_bucket_recovery",
	"rustfs_bucket_replication",
	"rustfs_bucket_request_payment",
	"rustfs_bucket_tags",
	"rustfs_bucket_versioning",
	"rustfs_bucket_website",
	"rustfs_config",
	"rustfs_config_history_clear",
	"rustfs_config_restore",
	"rustfs_group",
	"rustfs_group_policy_attachment",
	"rustfs_iam_backup_import",
	"rustfs_ilm_transition",
	"rustfs_integrity_job",
	"rustfs_kms_backup",
	"rustfs_kms_key",
	"rustfs_kms_rekey",
	"rustfs_ldap_policy_attachment",
	"rustfs_ldap_service_account",
	"rustfs_module_switch",
	"rustfs_notification_target",
	"rustfs_object_data_cache_flush",
	"rustfs_oidc_provider",
	"rustfs_policy",
	"rustfs_pool_decommission",
	"rustfs_quota",
	"rustfs_rebalance",
	"rustfs_remote_target",
	"rustfs_scanner_cycle_state_reset",
	"rustfs_scanner_usage_state_reset",
	"rustfs_serviceaccount",
	"rustfs_site_replication",
	"rustfs_site_replication_repair",
	"rustfs_site_replication_resync",
	"rustfs_table_bucket",
	"rustfs_tier",
	"rustfs_tls_certificate",
	"rustfs_token_revocation",
	"rustfs_user",
	"rustfs_user_policy_attachment",
}

// goldenDataSourceTypeNames is every data source the provider must register.
var goldenDataSourceTypeNames = []string{
	"rustfs_batch_jobs",
	"rustfs_bucket",
	"rustfs_bucket_metadata_backup",
	"rustfs_bucket_on_demand_migration_status",
	"rustfs_bucket_policy_status",
	"rustfs_bucket_usage",
	"rustfs_buckets",
	"rustfs_cluster_snapshot",
	"rustfs_config_history",
	"rustfs_extensions",
	"rustfs_groups",
	"rustfs_health_info",
	"rustfs_iam_backup",
	"rustfs_iam_policies",
	"rustfs_iam_policy",
	"rustfs_ilm_expiry_status",
	"rustfs_ilm_recovery_records",
	"rustfs_ilm_tier_stats",
	"rustfs_integrity_inventory",
	"rustfs_integrity_readiness",
	"rustfs_kms_backup_status",
	"rustfs_kms_config",
	"rustfs_kms_keys",
	"rustfs_kms_status",
	"rustfs_metrics",
	"rustfs_object_data_cache_stats",
	"rustfs_oidc_config",
	"rustfs_plugins",
	"rustfs_pools",
	"rustfs_quota",
	"rustfs_quota_check",
	"rustfs_quota_stats",
	"rustfs_replication_diff",
	"rustfs_replication_metrics",
	"rustfs_replication_mrf",
	"rustfs_runtime_capabilities",
	"rustfs_scanner_status",
	"rustfs_server_info",
	"rustfs_service_accounts",
	"rustfs_site_replication_status",
	"rustfs_storage_info",
	"rustfs_temporary_accounts",
	"rustfs_tiers",
	"rustfs_tls_certificate_state",
	"rustfs_user_mfa",
	"rustfs_users",
}

func TestRegisteredResourcesMatchGolden(t *testing.T) {
	got := resourceTypeNames(t, registeredResources())
	if !slices.Equal(got, goldenResourceTypeNames) {
		t.Fatalf("registered resources = %v, want %v", got, goldenResourceTypeNames)
	}
}

func TestRegisteredDataSourcesMatchGolden(t *testing.T) {
	got := dataSourceTypeNames(t, registeredDataSources())
	if !slices.Equal(got, goldenDataSourceTypeNames) {
		t.Fatalf("registered data sources = %v, want %v", got, goldenDataSourceTypeNames)
	}
}

func TestProviderResourcesUseRegistry(t *testing.T) {
	p := New("test")()

	resourceNames := resourceTypeNames(t, p.Resources(context.Background()))
	if !slices.Equal(resourceNames, goldenResourceTypeNames) {
		t.Errorf("Provider.Resources() = %v, want %v", resourceNames, goldenResourceTypeNames)
	}

	dataSourceNames := dataSourceTypeNames(t, p.DataSources(context.Background()))
	if !slices.Equal(dataSourceNames, goldenDataSourceTypeNames) {
		t.Errorf("Provider.DataSources() = %v, want %v", dataSourceNames, goldenDataSourceTypeNames)
	}
}

func TestRegistryOrderIsStable(t *testing.T) {
	first := resourceTypeNames(t, registeredResources())
	second := resourceTypeNames(t, registeredResources())
	if !slices.Equal(first, second) {
		t.Errorf("registeredResources() order changed between calls: %v then %v", first, second)
	}

	first = dataSourceTypeNames(t, registeredDataSources())
	second = dataSourceTypeNames(t, registeredDataSources())
	if !slices.Equal(first, second) {
		t.Errorf("registeredDataSources() order changed between calls: %v then %v", first, second)
	}
}

func TestRegisteredTypesAreUnique(t *testing.T) {
	assertUnique(t, "resource", resourceTypeNames(t, registeredResources()))
	assertUnique(t, "data source", dataSourceTypeNames(t, registeredDataSources()))
}

func resourceTypeNames(t *testing.T, factories []func() resource.Resource) []string {
	t.Helper()

	names := make([]string, 0, len(factories))
	for _, factory := range factories {
		var resp resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		names = append(names, resp.TypeName)
	}
	return names
}

func dataSourceTypeNames(t *testing.T, factories []func() datasource.DataSource) []string {
	t.Helper()

	names := make([]string, 0, len(factories))
	for _, factory := range factories {
		var resp datasource.MetadataResponse
		factory().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		names = append(names, resp.TypeName)
	}
	return names
}

func assertUnique(t *testing.T, kind string, names []string) {
	t.Helper()

	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			t.Errorf("%s %q is registered more than once", kind, name)
		}
		seen[name] = struct{}{}
	}
}

func TestRegistrySnapshotMatchesRegistry(t *testing.T) {
	snapshot := RegistrySnapshot()

	var resources, dataSources []string
	for _, entry := range snapshot {
		switch entry.Kind {
		case RegistryKindResource:
			resources = append(resources, entry.TypeName)
		case RegistryKindDataSource:
			dataSources = append(dataSources, entry.TypeName)
		default:
			t.Errorf("%s has an unknown kind %q", entry.TypeName, entry.Kind)
		}

		if !strings.HasPrefix(entry.File, "provider/") || !strings.HasSuffix(entry.File, ".go") {
			t.Errorf("%s resolves to %q, want a provider/*.go path", entry.TypeName, entry.File)
			continue
		}
		if _, err := os.Stat(filepath.Join("..", entry.File)); err != nil {
			t.Errorf("%s resolves to %q, which does not exist: %v", entry.TypeName, entry.File, err)
		}
	}

	if !slices.Equal(resources, goldenResourceTypeNames) {
		t.Errorf("snapshot resources = %v, want %v", resources, goldenResourceTypeNames)
	}
	if !slices.Equal(dataSources, goldenDataSourceTypeNames) {
		t.Errorf("snapshot data sources = %v, want %v", dataSources, goldenDataSourceTypeNames)
	}

	if !slices.IsSortedFunc(snapshot, func(a, b RegistryEntry) int {
		return strings.Compare(a.TypeName, b.TypeName)
	}) {
		t.Error("snapshot is not sorted by type name")
	}
}
