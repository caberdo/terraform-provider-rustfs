// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import "context"

// AttachUserPolicy attaches a canned policy to a user, leaving any policies
// the user already holds untouched. RustFS stores a user's policies as a
// comma-separated list, so this appends to the existing list instead of
// replacing it (unlike PUT set-user-or-group-policy, which replaces the
// whole mapping and is used for the single primary policy on rustfs_user).
func (c *RustfsAdmin) AttachUserPolicy(ctx context.Context, user, policy string) error {
	return c.userPolicyAssociation(ctx, user, policy, "idp/builtin/policy/attach")
}

// DetachUserPolicy detaches a single canned policy from a user, leaving any
// other attached policies untouched. The RustFS admin API exposes no DELETE
// variant of set-user-or-group-policy, so removal goes through the dedicated
// POST idp/builtin/policy/detach endpoint.
func (c *RustfsAdmin) DetachUserPolicy(ctx context.Context, user, policy string) error {
	return c.userPolicyAssociation(ctx, user, policy, "idp/builtin/policy/detach")
}

func (c *RustfsAdmin) userPolicyAssociation(ctx context.Context, user, policy, relPath string) error {
	return c.policyAssociation(ctx, relPath, policyAssociationRequest{
		User:     user,
		Policies: []string{policy},
	})
}
