package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketNotificationResourceSchema(t *testing.T) {
	r := NewBucketNotificationResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "expected_bucket_owner", "skip_destination_validation",
		"event_bridge", "queue", "topic", "lambda",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
	if _, ok := attrs["skip_destination_validation"].(schema.BoolAttribute); !ok {
		t.Error("skip_destination_validation is not a BoolAttribute")
	}

	for _, target := range []string{"queue", "topic", "lambda"} {
		list, ok := attrs[target].(schema.ListNestedAttribute)
		if !ok {
			t.Fatalf("%s attribute is not a ListNestedAttribute", target)
		}
		for _, name := range []string{"id", "arn", "events", "filter"} {
			if _, ok := list.NestedObject.Attributes[name]; !ok {
				t.Errorf("expected %s.%s attribute", target, name)
			}
		}
		filter, ok := list.NestedObject.Attributes["filter"].(schema.SingleNestedAttribute)
		if !ok {
			t.Fatalf("%s.filter is not a SingleNestedAttribute", target)
		}
		for _, name := range []string{"prefix", "suffix"} {
			if _, ok := filter.Attributes[name]; !ok {
				t.Errorf("expected %s.filter.%s attribute", target, name)
			}
		}
	}

	eb, ok := attrs["event_bridge"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("event_bridge is not a SingleNestedAttribute")
	}
	if _, ok := eb.Attributes["event_bridge_enabled"]; !ok {
		t.Error("expected event_bridge.event_bridge_enabled attribute")
	}
}

func TestBucketNotificationResourceMetadata(t *testing.T) {
	r := NewBucketNotificationResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_notification" {
		t.Errorf("expected rustfs_bucket_notification, got %s", resp.TypeName)
	}
}

func TestBuildNotificationConfig_SingleQueue(t *testing.T) {
	eventsSet, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectCreated:*", "s3:ObjectRemoved:*"})
	plan := bucketNotificationResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Queue: []bucketNotificationTargetModel{
			{
				Id:     types.StringValue("queue-1"),
				Arn:    types.StringValue("arn:minio:sqs::PRIMARY:amqp"),
				Events: eventsSet,
				Filter: &bucketNotificationFilterModel{
					Prefix: types.StringValue("uploads/"),
					Suffix: types.StringValue(".jpg"),
				},
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
	if aws.ToString(q.Id) != "queue-1" {
		t.Errorf("unexpected queue id: %s", aws.ToString(q.Id))
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
		Queue: []bucketNotificationTargetModel{
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
		Queue: []bucketNotificationTargetModel{
			{Arn: types.StringValue("arn:minio:sqs::PRIMARY:q1"), Events: events1},
			{Arn: types.StringValue("arn:minio:sqs::PRIMARY:q2"), Events: events2},
		},
	}

	config := buildNotificationConfig(context.Background(), plan)
	if len(config.QueueConfigurations) != 2 {
		t.Fatalf("expected 2 queue configs, got %d", len(config.QueueConfigurations))
	}
}

func TestBuildNotificationConfig_TopicAndLambda(t *testing.T) {
	events, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectCreated:*"})
	plan := bucketNotificationResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Topic: []bucketNotificationTargetModel{
			{Arn: types.StringValue("arn:minio:sns::PRIMARY:topic"), Events: events},
		},
		Lambda: []bucketNotificationTargetModel{
			{Arn: types.StringValue("arn:aws:lambda:us-east-1:1:function:fn"), Events: events},
		},
	}

	config := buildNotificationConfig(context.Background(), plan)
	if len(config.TopicConfigurations) != 1 || aws.ToString(config.TopicConfigurations[0].TopicArn) != "arn:minio:sns::PRIMARY:topic" {
		t.Errorf("unexpected topic config: %+v", config.TopicConfigurations)
	}
	if len(config.LambdaFunctionConfigurations) != 1 ||
		aws.ToString(config.LambdaFunctionConfigurations[0].LambdaFunctionArn) != "arn:aws:lambda:us-east-1:1:function:fn" {
		t.Errorf("unexpected lambda config: %+v", config.LambdaFunctionConfigurations)
	}
}

func TestBuildNotificationConfig_EventBridge(t *testing.T) {
	plan := bucketNotificationResourceModel{
		Bucket:      types.StringValue("test-bucket"),
		EventBridge: &bucketNotificationEventBridgeModel{EventBridgeEnabled: types.BoolValue(true)},
	}

	config := buildNotificationConfig(context.Background(), plan)
	if config.EventBridgeConfiguration == nil {
		t.Fatal("expected event bridge configuration")
	}

	disabled := bucketNotificationResourceModel{
		Bucket:      types.StringValue("test-bucket"),
		EventBridge: &bucketNotificationEventBridgeModel{EventBridgeEnabled: types.BoolValue(false)},
	}
	if buildNotificationConfig(context.Background(), disabled).EventBridgeConfiguration != nil {
		t.Error("expected nil event bridge configuration when disabled")
	}
}

func TestBuildPutNotificationInput_requestFields(t *testing.T) {
	events, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"s3:ObjectCreated:*"})
	plan := bucketNotificationResourceModel{
		Bucket:                    types.StringValue("test-bucket"),
		ExpectedBucketOwner:       types.StringValue("123456789012"),
		SkipDestinationValidation: types.BoolValue(true),
		Queue: []bucketNotificationTargetModel{
			{Arn: types.StringValue("arn:minio:sqs::PRIMARY:q1"), Events: events},
		},
	}

	input := buildPutNotificationInput(context.Background(), plan)
	if aws.ToString(input.ExpectedBucketOwner) != "123456789012" {
		t.Errorf("unexpected expected bucket owner: %s", aws.ToString(input.ExpectedBucketOwner))
	}
	if !aws.ToBool(input.SkipDestinationValidation) {
		t.Error("expected skip destination validation true")
	}
	if input.NotificationConfiguration == nil || len(input.NotificationConfiguration.QueueConfigurations) != 1 {
		t.Error("expected one queue configuration")
	}
}

func TestFlattenNotificationFilter(t *testing.T) {
	filter := &s3types.NotificationConfigurationFilter{
		Key: &s3types.S3KeyFilter{
			FilterRules: []s3types.FilterRule{
				{Name: s3types.FilterRuleNamePrefix, Value: aws.String("logs/")},
				{Name: s3types.FilterRuleNameSuffix, Value: aws.String(".log")},
			},
		},
	}

	flat := flattenNotificationFilter(filter)
	if flat == nil || flat.Prefix.ValueString() != "logs/" || flat.Suffix.ValueString() != ".log" {
		t.Fatalf("unexpected flat filter: %+v", flat)
	}

	if flattenNotificationFilter(nil) != nil {
		t.Error("expected nil filter for nil input")
	}
}

func TestNormalizeNotificationUnknowns(t *testing.T) {
	state := bucketNotificationResourceModel{
		Queue: []bucketNotificationTargetModel{{Id: types.StringUnknown()}},
		Topic: []bucketNotificationTargetModel{{Id: types.StringValue("topic-1")}},
	}
	normalizeNotificationUnknowns(&state)

	if !state.Queue[0].Id.IsNull() {
		t.Errorf("unknown id should be nulled: %+v", state.Queue[0].Id)
	}
	if state.Topic[0].Id.ValueString() != "topic-1" {
		t.Errorf("known id must be preserved: %+v", state.Topic[0].Id)
	}
}

func TestFlattenNotificationTargets_EmptyReturnsNil(t *testing.T) {
	if got := flattenQueueTargets(nil); got != nil {
		t.Errorf("flattenQueueTargets(nil) = %#v, want nil", got)
	}
	if got := flattenQueueTargets([]s3types.QueueConfiguration{}); got != nil {
		t.Errorf("flattenQueueTargets(empty) = %#v, want nil", got)
	}
	if got := flattenTopicTargets(nil); got != nil {
		t.Errorf("flattenTopicTargets(nil) = %#v, want nil", got)
	}
	if got := flattenTopicTargets([]s3types.TopicConfiguration{}); got != nil {
		t.Errorf("flattenTopicTargets(empty) = %#v, want nil", got)
	}
	if got := flattenLambdaTargets(nil); got != nil {
		t.Errorf("flattenLambdaTargets(nil) = %#v, want nil", got)
	}
	if got := flattenLambdaTargets([]s3types.LambdaFunctionConfiguration{}); got != nil {
		t.Errorf("flattenLambdaTargets(empty) = %#v, want nil", got)
	}
}

func TestIsNotificationNotFound(t *testing.T) {
	if !isNotificationNotFound(&smithy.GenericAPIError{Code: "NoSuchConfiguration"}) {
		t.Error("NoSuchConfiguration must be treated as not found")
	}
	if !isNotificationNotFound(&smithy.GenericAPIError{Code: "NoSuchBucket"}) {
		t.Error("NoSuchBucket must be treated as not found")
	}
	if isNotificationNotFound(errors.New("access denied")) {
		t.Error("an unrelated error must not be treated as not found")
	}
}

func TestReconcileNotificationEventBridge(t *testing.T) {
	configured := &bucketNotificationResourceModel{
		EventBridge: &bucketNotificationEventBridgeModel{EventBridgeEnabled: types.BoolValue(false)},
	}
	reconcileEventBridge(configured, false)
	if configured.EventBridge == nil {
		t.Fatal("a configured event_bridge block must be preserved when the server reports none")
	}
	if configured.EventBridge.EventBridgeEnabled.ValueBool() {
		t.Error("preserved event_bridge block must be disabled")
	}

	serverEnabled := &bucketNotificationResourceModel{
		EventBridge: &bucketNotificationEventBridgeModel{EventBridgeEnabled: types.BoolValue(false)},
	}
	reconcileEventBridge(serverEnabled, true)
	if serverEnabled.EventBridge == nil || !serverEnabled.EventBridge.EventBridgeEnabled.ValueBool() {
		t.Error("server EventBridge config must read back as enabled")
	}

	omitted := &bucketNotificationResourceModel{}
	reconcileEventBridge(omitted, false)
	if omitted.EventBridge != nil {
		t.Error("an omitted event_bridge block must stay nil")
	}
}
