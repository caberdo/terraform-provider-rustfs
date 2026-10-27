// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// liveGroupPolicyFixture creates a group and the named canned policies, then
// removes them again when the test ends.
func liveGroupPolicyFixture(t *testing.T, client *rustfs.RustfsAdmin, policies ...string) string {
	t.Helper()

	suffix := time.Now().UnixNano()
	group := fmt.Sprintf("tf-live-group-%d", suffix)

	if err := client.UpdateGroupMembers(context.Background(), rustfs.GroupAddRemove{
		Group:    group,
		Members:  []string{},
		IsRemove: false,
		Status:   "enabled",
	}); err != nil {
		t.Fatalf("failed to create group %s: %v", group, err)
	}

	for i := range policies {
		policies[i] = fmt.Sprintf("tf-live-policy-%d-%d", suffix, i)
		if err := client.CreatePolicy(context.Background(), rustfs.Policy{
			Name: policies[i],
			Statement: []rustfs.PolicyStatement{{
				Effect:   "Allow",
				Action:   []string{"s3:GetBucketLocation"},
				Resource: []string{"arn:aws:s3:::*"},
			}},
		}); err != nil {
			t.Fatalf("failed to create policy %s: %v", policies[i], err)
		}
		policy := policies[i]
		t.Cleanup(func() {
			if err := client.DeletePolicy(context.Background(), policy); err != nil {
				t.Errorf("failed to delete policy %s: %v", policy, err)
			}
		})
	}

	// Registered last so it runs first: a canned policy cannot be removed while
	// a group still holds it, and deleting the group drops its bindings.
	t.Cleanup(func() {
		if err := client.DeleteGroup(context.Background(), group); err != nil {
			t.Errorf("failed to delete group %s: %v", group, err)
		}
	})

	return group
}

func liveGroupPolicies(t *testing.T, client *rustfs.RustfsAdmin, group string) []string {
	t.Helper()

	info, err := client.GetGroup(context.Background(), group)
	if err != nil {
		t.Fatalf("failed to read group %s: %v", group, err)
	}
	return info.Policies()
}

// The group attach/detach endpoints are additive: neither may touch a policy
// the caller did not name.
func TestLiveGroupPolicyAttachmentIsAdditive(t *testing.T) {
	client := getClient(t)
	policies := make([]string, 2)
	group := liveGroupPolicyFixture(t, &client, policies...)
	policyA, policyB := policies[0], policies[1]

	if err := client.AttachGroupPolicy(context.Background(), group, policyA); err != nil {
		t.Fatalf("failed to attach %s: %v", policyA, err)
	}
	if err := client.AttachGroupPolicy(context.Background(), group, policyB); err != nil {
		t.Fatalf("failed to attach %s: %v", policyB, err)
	}

	attached := liveGroupPolicies(t, &client, group)
	for _, policy := range []string{policyA, policyB} {
		if !slices.Contains(attached, policy) {
			t.Fatalf("expected both policies attached, got %v", attached)
		}
	}

	if err := client.DetachGroupPolicy(context.Background(), group, policyA); err != nil {
		t.Fatalf("failed to detach %s: %v", policyA, err)
	}

	remaining := liveGroupPolicies(t, &client, group)
	if slices.Contains(remaining, policyA) {
		t.Errorf("expected %s to be detached, got %v", policyA, remaining)
	}
	if !slices.Contains(remaining, policyB) {
		t.Errorf("detaching %s must leave %s attached, got %v", policyA, policyB, remaining)
	}

	if err := client.DetachGroupPolicy(context.Background(), group, policyB); err != nil {
		t.Fatalf("failed to detach %s: %v", policyB, err)
	}
	if remaining := liveGroupPolicies(t, &client, group); len(remaining) != 0 {
		t.Errorf("expected no policies left, got %v", remaining)
	}
}

// The admin API serializes nothing itself, so two attachments applied in
// parallel must not race: the second one would otherwise rewrite the group's
// policy list without the first.
func TestLiveGroupPolicyAttachmentConcurrentAttach(t *testing.T) {
	client := getClient(t)
	policies := make([]string, 2)
	group := liveGroupPolicyFixture(t, &client, policies...)

	var wg sync.WaitGroup
	for _, policy := range policies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.AttachGroupPolicy(context.Background(), group, policy); err != nil {
				t.Errorf("failed to attach %s: %v", policy, err)
			}
		}()
	}
	wg.Wait()

	attached := liveGroupPolicies(t, &client, group)
	for _, policy := range policies {
		if !slices.Contains(attached, policy) {
			t.Errorf("concurrent attach dropped %s, group holds %v", policy, attached)
		}
	}
}
