package provider

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildNotificationConfig_SingleQueue(t *testing.T) {
	eventsSet, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectCreated:*", "s3:ObjectRemoved:*"})
	plan := bucketNotificationResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Queue: []bucketNotificationQueueModel{
			{
				Arn:          types.StringValue("arn:minio:sqs::PRIMARY:amqp"),
				Events:       eventsSet,
				FilterPrefix: types.StringValue("uploads/"),
				FilterSuffix: types.StringValue(".jpg"),
			},
		},
	}

	config := buildNotificationConfig(context.Background(), plan)

	if len(config.QueueConfigurations) != 1 {
		t.Fatalf("expected 1 queue config, got %d", len(config.QueueConfigurations))
	}

	q := config.QueueConfigurations[0]
	if aws.ToString(q.QueueArn) != "arn:minio:sqs::PRIMARY:amqp" {
		t.Errorf("unexpected queue ARN: %s", aws.ToString(q.QueueArn))
	}
	if len(q.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(q.Events))
	}

	if q.Filter == nil {
		t.Fatal("expected non-nil filter")
	}

	rules := q.Filter.Key.FilterRules
	if len(rules) != 2 {
		t.Fatalf("expected 2 filter rules, got %d", len(rules))
	}

	foundPrefix, foundSuffix := false, false
	for _, r := range rules {
		if r.Name == s3types.FilterRuleNamePrefix && aws.ToString(r.Value) == "uploads/" {
			foundPrefix = true
		}
		if r.Name == s3types.FilterRuleNameSuffix && aws.ToString(r.Value) == ".jpg" {
			foundSuffix = true
		}
	}
	if !foundPrefix {
		t.Error("missing prefix filter rule")
	}
	if !foundSuffix {
		t.Error("missing suffix filter rule")
	}
}

func TestBuildNotificationConfig_NoFilter(t *testing.T) {
	eventsSet, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectCreated:*"})
	plan := bucketNotificationResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Queue: []bucketNotificationQueueModel{
			{
				Arn:    types.StringValue("arn:minio:sqs::PRIMARY:amqp"),
				Events: eventsSet,
			},
		},
	}

	config := buildNotificationConfig(context.Background(), plan)
	if len(config.QueueConfigurations) != 1 {
		t.Fatalf("expected 1 queue config, got %d", len(config.QueueConfigurations))
	}
	if config.QueueConfigurations[0].Filter != nil {
		t.Error("expected nil filter when no prefix/suffix")
	}
}

func TestBuildNotificationConfig_MultipleQueues(t *testing.T) {
	events1, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectCreated:*"})
	events2, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectRemoved:*"})
	plan := bucketNotificationResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Queue: []bucketNotificationQueueModel{
			{Arn: types.StringValue("arn:minio:sqs::PRIMARY:q1"), Events: events1},
			{Arn: types.StringValue("arn:minio:sqs::PRIMARY:q2"), Events: events2},
		},
	}

	config := buildNotificationConfig(context.Background(), plan)
	if len(config.QueueConfigurations) != 2 {
		t.Fatalf("expected 2 queue configs, got %d", len(config.QueueConfigurations))
	}
}
