// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
)

// policyAssociationRequest is the JSON body accepted by the RustFS
// builtin-policy attach/detach endpoints. Exactly one of User or Group must
// be set, together with at least one policy name.
type policyAssociationRequest struct {
	User     string   `json:"user,omitempty"`
	Group    string   `json:"group,omitempty"`
	Policies []string `json:"policies"`
}

// policyAssociation posts a single attach or detach request to the additive
// idp/builtin/policy/{attach,detach} endpoints. Those endpoints append to, or
// remove from, the target's existing policy list instead of replacing it, so
// unlike PUT set-user-or-group-policy (which carries the single primary policy
// on rustfs_user) they never touch a policy the caller did not name.
func (c *RustfsAdmin) policyAssociation(ctx context.Context, relPath string, req policyAssociationRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	reqData := RequestData{
		Method:  "POST",
		RelPath: relPath,
		Content: body,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}
