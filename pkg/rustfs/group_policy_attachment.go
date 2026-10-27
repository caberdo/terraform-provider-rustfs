// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"sync"
)

// groupPolicyAssociationMu serializes the group attach/detach calls made by
// this process. RustFS implements both as a read-modify-write of the group's
// whole comma-separated policy list, so two attachments applied in parallel
// would otherwise race and drop one of the policies. Terraform applies
// resources concurrently, so the endpoints are serialized here instead.
var groupPolicyAssociationMu sync.Mutex

// AttachGroupPolicy attaches a canned policy to an IAM group, leaving every
// other policy the group holds attached. RustFS stores a group's policies as a
// comma-separated list and idp/builtin/policy/attach appends to it (unlike PUT
// set-user-or-group-policy, which replaces the whole mapping).
func (c *RustfsAdmin) AttachGroupPolicy(ctx context.Context, group, policy string) error {
	return c.groupPolicyAssociation(ctx, group, policy, "idp/builtin/policy/attach")
}

// DetachGroupPolicy detaches a single canned policy from an IAM group, leaving
// every other attached policy untouched. The admin API exposes no DELETE
// variant of set-user-or-group-policy, and an empty policyName there would drop
// the group's whole mapping, so removal goes through the dedicated additive
// POST idp/builtin/policy/detach endpoint.
func (c *RustfsAdmin) DetachGroupPolicy(ctx context.Context, group, policy string) error {
	return c.groupPolicyAssociation(ctx, group, policy, "idp/builtin/policy/detach")
}

func (c *RustfsAdmin) groupPolicyAssociation(ctx context.Context, group, policy, relPath string) error {
	groupPolicyAssociationMu.Lock()
	defer groupPolicyAssociationMu.Unlock()

	return c.policyAssociation(ctx, relPath, policyAssociationRequest{
		Group:    group,
		Policies: []string{policy},
	})
}
