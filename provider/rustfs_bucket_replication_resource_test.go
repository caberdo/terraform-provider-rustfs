package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketReplicationResourceSchema(t *testing.T) {
	r := NewBucketReplicationResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "role", "token", "checksum_algorithm", "content_md5",
		"expected_bucket_owner", "rule",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
	for _, legacy := range []string{"destination_bucket", "priority", "status", "delete_marker_replication", "delete_replication"} {
		if _, ok := attrs[legacy]; ok {
			t.Errorf("legacy top-level attribute %q must be removed", legacy)
		}
	}

	rule, ok := attrs["rule"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatal("rule attribute is not a ListNestedAttribute")
	}
	ruleAttrs := rule.NestedObject.Attributes
	for _, name := range []string{
		"id", "priority", "status", "filter", "delete_marker_replication",
		"existing_object_replication", "source_selection_criteria", "destination",
	} {
		if _, ok := ruleAttrs[name]; !ok {
			t.Errorf("expected rule.%s attribute", name)
		}
	}

	filter, ok := ruleAttrs["filter"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.filter is not a SingleNestedAttribute")
	}
	for _, name := range []string{"prefix", "tag", "and"} {
		if _, ok := filter.Attributes[name]; !ok {
			t.Errorf("expected rule.filter.%s attribute", name)
		}
	}

	dest, ok := ruleAttrs["destination"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.destination is not a SingleNestedAttribute")
	}
	for _, name := range []string{
		"bucket", "account", "storage_class", "access_control_translation",
		"encryption_configuration", "metrics", "replication_time",
	} {
		if _, ok := dest.Attributes[name]; !ok {
			t.Errorf("expected rule.destination.%s attribute", name)
		}
	}

	ssc, ok := ruleAttrs["source_selection_criteria"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.source_selection_criteria is not a SingleNestedAttribute")
	}
	for _, name := range []string{"replica_modifications", "sse_kms_encrypted_objects"} {
		if _, ok := ssc.Attributes[name]; !ok {
			t.Errorf("expected source_selection_criteria.%s attribute", name)
		}
	}
}

func TestBucketReplicationResourceMetadata(t *testing.T) {
	r := NewBucketReplicationResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_replication" {
		t.Errorf("expected rustfs_bucket_replication, got %s", resp.TypeName)
	}
}

func TestBuildReplicationConfig_basic(t *testing.T) {
	plan := bucketReplicationResourceModel{
		Bucket: types.StringValue("source-bucket"),
		Role:   types.StringValue("arn:minio:replication::id:src"),
		Rule: []bucketReplicationRuleModel{
			{
				Id:       types.StringValue("rule-1"),
				Priority: types.Int64Value(1),
				Status:   types.StringValue("Enabled"),
				Destination: &bucketReplicationDestinationModel{
					Bucket: types.StringValue("arn:aws:s3:::dest"),
				},
			},
		},
	}

	cfg := buildReplicationConfig(plan)

	if aws.ToString(cfg.Role) != "arn:minio:replication::id:src" {
		t.Errorf("expected role, got %s", aws.ToString(cfg.Role))
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}
	rule := cfg.Rules[0]
	if aws.ToString(rule.ID) != "rule-1" {
		t.Errorf("unexpected rule id: %s", aws.ToString(rule.ID))
	}
	if rule.Status != "Enabled" {
		t.Errorf("expected Enabled, got %s", rule.Status)
	}
	if rule.Priority == nil || *rule.Priority != 1 {
		t.Errorf("expected priority 1, got %v", rule.Priority)
	}
	if aws.ToString(rule.Destination.Bucket) != "arn:aws:s3:::dest" {
		t.Errorf("unexpected dest: %s", aws.ToString(rule.Destination.Bucket))
	}
}

func TestBuildReplicationConfig_multipleRules(t *testing.T) {
	plan := bucketReplicationResourceModel{
		Bucket: types.StringValue("source"),
		Role:   types.StringValue("arn:minio:replication::id:src"),
		Rule: []bucketReplicationRuleModel{
			{
				Status:      types.StringValue("Enabled"),
				Destination: &bucketReplicationDestinationModel{Bucket: types.StringValue("arn:aws:s3:::d1")},
			},
			{
				Status:      types.StringValue("Disabled"),
				Destination: &bucketReplicationDestinationModel{Bucket: types.StringValue("arn:aws:s3:::d2")},
			},
		},
	}

	cfg := buildReplicationConfig(plan)
	if len(cfg.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Rules))
	}
	if cfg.Rules[1].Status != "Disabled" {
		t.Errorf("expected Disabled, got %s", cfg.Rules[1].Status)
	}
}

func TestBuildReplicationConfig_deleteMarkerReplication(t *testing.T) {
	plan := bucketReplicationResourceModel{
		Bucket: types.StringValue("source"),
		Role:   types.StringValue("arn:minio:replication::id:src"),
		Rule: []bucketReplicationRuleModel{
			{
				Status: types.StringValue("Enabled"),
				DeleteMarkerReplication: &bucketReplicationStatusModel{
					Status: types.StringValue("Enabled"),
				},
				Destination: &bucketReplicationDestinationModel{Bucket: types.StringValue("arn:aws:s3:::dest")},
			},
		},
	}

	cfg := buildReplicationConfig(plan)
	rule := cfg.Rules[0]
	if rule.DeleteMarkerReplication == nil {
		t.Fatal("expected delete marker replication to be set")
	}
	if rule.DeleteMarkerReplication.Status != "Enabled" {
		t.Errorf("expected Enabled, got %s", rule.DeleteMarkerReplication.Status)
	}
}

func TestBuildReplicationConfig_full(t *testing.T) {
	plan := bucketReplicationResourceModel{
		Bucket: types.StringValue("source"),
		Role:   types.StringValue("arn:minio:replication::id:src"),
		Rule: []bucketReplicationRuleModel{
			{
				Id:       types.StringValue("full"),
				Status:   types.StringValue("Enabled"),
				Priority: types.Int64Value(10),
				Filter: &bucketReplicationFilterModel{
					And: &bucketReplicationAndOperatorModel{
						Prefix: types.StringValue("logs/"),
						Tags: []bucketReplicationTagModel{
							{Key: types.StringValue("env"), Value: types.StringValue("prod")},
						},
					},
				},
				ExistingObjectReplication: &bucketReplicationStatusModel{Status: types.StringValue("Enabled")},
				SourceSelectionCriteria: &bucketReplicationSourceSelectionModel{
					ReplicaModifications:   &bucketReplicationStatusModel{Status: types.StringValue("Enabled")},
					SseKmsEncryptedObjects: &bucketReplicationStatusModel{Status: types.StringValue("Enabled")},
				},
				Destination: &bucketReplicationDestinationModel{
					Bucket:                   types.StringValue("arn:aws:s3:::dest"),
					Account:                  types.StringValue("123456789012"),
					StorageClass:             types.StringValue("STANDARD"),
					AccessControlTranslation: &bucketReplicationAccessControlTranslationModel{Owner: types.StringValue("Destination")},
					EncryptionConfiguration:  &bucketReplicationEncryptionConfigurationModel{ReplicaKmsKeyId: types.StringValue("arn:aws:kms:key")},
					Metrics: &bucketReplicationMetricsModel{
						Status:         types.StringValue("Enabled"),
						EventThreshold: &bucketReplicationTimeValueModel{Minutes: types.Int64Value(15)},
					},
					ReplicationTime: &bucketReplicationTimeModel{
						Status: types.StringValue("Enabled"),
						Time:   &bucketReplicationTimeValueModel{Minutes: types.Int64Value(15)},
					},
				},
			},
		},
	}

	rule := buildReplicationConfig(plan).Rules[0]

	if rule.Filter == nil || rule.Filter.And == nil {
		t.Fatal("expected filter.and")
	}
	if len(rule.Filter.And.Tags) != 1 || aws.ToString(rule.Filter.And.Tags[0].Key) != "env" {
		t.Errorf("unexpected and tags: %v", rule.Filter.And.Tags)
	}
	if rule.ExistingObjectReplication == nil || rule.ExistingObjectReplication.Status != "Enabled" {
		t.Error("expected existing_object_replication Enabled")
	}
	if rule.SourceSelectionCriteria.ReplicaModifications.Status != "Enabled" {
		t.Error("expected replica_modifications Enabled")
	}
	if rule.SourceSelectionCriteria.SseKmsEncryptedObjects.Status != "Enabled" {
		t.Error("expected sse_kms_encrypted_objects Enabled")
	}
	if rule.Destination.StorageClass != "STANDARD" {
		t.Errorf("unexpected storage class %q", rule.Destination.StorageClass)
	}
	if rule.Destination.AccessControlTranslation.Owner != "Destination" {
		t.Error("expected owner Destination")
	}
	if aws.ToString(rule.Destination.EncryptionConfiguration.ReplicaKmsKeyID) != "arn:aws:kms:key" {
		t.Error("unexpected replica kms key")
	}
	if rule.Destination.Metrics.EventThreshold == nil || *rule.Destination.Metrics.EventThreshold.Minutes != 15 {
		t.Error("expected metrics event threshold 15")
	}
	if rule.Destination.ReplicationTime.Time == nil || *rule.Destination.ReplicationTime.Time.Minutes != 15 {
		t.Error("expected replication time 15")
	}
}

func TestFlattenReplicationRules_roundTrip(t *testing.T) {
	cfg := buildReplicationConfig(bucketReplicationResourceModel{
		Bucket: types.StringValue("source"),
		Role:   types.StringValue("arn:minio:replication::id:src"),
		Rule: []bucketReplicationRuleModel{
			{
				Id:       types.StringValue("r1"),
				Priority: types.Int64Value(2),
				Status:   types.StringValue("Enabled"),
				Filter:   &bucketReplicationFilterModel{Prefix: types.StringValue("logs/")},
				Destination: &bucketReplicationDestinationModel{
					Bucket: types.StringValue("arn:aws:s3:::dest"),
				},
			},
		},
	})

	rules := flattenReplicationRules(cfg.Rules)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Id.ValueString() != "r1" || rules[0].Priority.ValueInt64() != 2 {
		t.Errorf("unexpected rule identity: %+v", rules[0])
	}
	if rules[0].Filter == nil || rules[0].Filter.Prefix.ValueString() != "logs/" {
		t.Errorf("unexpected filter: %+v", rules[0].Filter)
	}
	if rules[0].Destination.Bucket.ValueString() != "arn:aws:s3:::dest" {
		t.Errorf("unexpected destination: %+v", rules[0].Destination)
	}
}

func TestNormalizeReplicationUnknowns(t *testing.T) {
	state := bucketReplicationResourceModel{
		Rule: []bucketReplicationRuleModel{
			{Id: types.StringUnknown(), Priority: types.Int64Unknown()},
			{Id: types.StringValue("known"), Priority: types.Int64Value(3)},
		},
	}
	normalizeReplicationUnknowns(&state)

	if !state.Rule[0].Id.IsNull() || !state.Rule[0].Priority.IsNull() {
		t.Errorf("unknowns should be nulled: %+v", state.Rule[0])
	}
	if state.Rule[1].Id.ValueString() != "known" || state.Rule[1].Priority.ValueInt64() != 3 {
		t.Errorf("known values must be preserved: %+v", state.Rule[1])
	}
}

func TestIsReplicationNotFound(t *testing.T) {
	if isReplicationNotFound(errors.New("unrelated error containing 404")) {
		t.Error("a bare 404 in an unrelated error must not be treated as not found")
	}
	if isReplicationNotFound(errors.New("access denied")) {
		t.Error("an unrelated error must not be treated as not found")
	}
	if !isReplicationNotFound(&smithy.GenericAPIError{Code: "ReplicationConfigurationNotFoundError"}) {
		t.Error("typed ReplicationConfigurationNotFoundError must be treated as not found")
	}
	if !isReplicationNotFound(&smithy.GenericAPIError{Code: "NoSuchBucket"}) {
		t.Error("typed NoSuchBucket must be treated as not found")
	}
	if !isReplicationNotFound(errors.New(`{"Code":"ReplicationConfigurationNotFoundError"}`)) {
		t.Error("substring ReplicationConfigurationNotFoundError must be treated as not found")
	}
}

func TestBucketReplicationResourceSchemaMinutesRequired(t *testing.T) {
	r := NewBucketReplicationResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	rule, ok := resp.Schema.GetAttributes()["rule"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatal("rule is not a ListNestedAttribute")
	}
	dest, ok := rule.NestedObject.Attributes["destination"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.destination is not a SingleNestedAttribute")
	}

	replicationTime, ok := dest.Attributes["replication_time"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.destination.replication_time is not a SingleNestedAttribute")
	}
	timeBlock, ok := replicationTime.Attributes["time"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.destination.replication_time.time is not a SingleNestedAttribute")
	}
	timeMinutes, ok := timeBlock.Attributes["minutes"].(schema.Int64Attribute)
	if !ok {
		t.Fatal("rule.destination.replication_time.time.minutes is not an Int64Attribute")
	}
	if !timeMinutes.Required {
		t.Error("rule.destination.replication_time.time.minutes must be Required")
	}

	metrics, ok := dest.Attributes["metrics"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.destination.metrics is not a SingleNestedAttribute")
	}
	eventThreshold, ok := metrics.Attributes["event_threshold"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.destination.metrics.event_threshold is not a SingleNestedAttribute")
	}
	thresholdMinutes, ok := eventThreshold.Attributes["minutes"].(schema.Int64Attribute)
	if !ok {
		t.Fatal("rule.destination.metrics.event_threshold.minutes is not an Int64Attribute")
	}
	if !thresholdMinutes.Required {
		t.Error("rule.destination.metrics.event_threshold.minutes must be Required")
	}
}
