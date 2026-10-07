package provider

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildReplicationConfig_basic(t *testing.T) {
	plan := bucketReplicationResourceModel{
		Bucket:            types.StringValue("source-bucket"),
		Role:              types.StringValue("arn:minio:replication::id:src"),
		DestinationBucket: types.StringValue("arn:aws:s3:::dest"),
		Priority:          types.Int64Value(1),
		Status:            types.StringValue("Enabled"),
	}

	cfg := buildReplicationConfig(plan)

	if aws.ToString(cfg.Role) != "arn:minio:replication::id:src" {
		t.Errorf("expected role, got %s", aws.ToString(cfg.Role))
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Status != "Enabled" {
		t.Errorf("expected Enabled, got %s", cfg.Rules[0].Status)
	}
	if aws.ToString(cfg.Rules[0].Destination.Bucket) != "arn:aws:s3:::dest" {
		t.Errorf("unexpected dest: %s", aws.ToString(cfg.Rules[0].Destination.Bucket))
	}
}

func TestBuildReplicationConfig_deleteMarkerReplication(t *testing.T) {
	plan := bucketReplicationResourceModel{
		Bucket:                  types.StringValue("source"),
		Role:                    types.StringValue("arn:minio:replication::id:src"),
		DestinationBucket:       types.StringValue("arn:aws:s3:::dest"),
		Priority:                types.Int64Value(1),
		Status:                  types.StringValue("Enabled"),
		DeleteMarkerReplication: types.StringValue("Enabled"),
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
