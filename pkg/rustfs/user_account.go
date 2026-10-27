// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/url"
)

type UserAccount struct {
	SecretKey string `json:"secretKey"`
	Status    string `json:"status"`
	AccessKey string
	Policy    string   `json:"policyName"`
	Groups    []string `json:"memberOf"`
}

func (c *RustfsAdmin) CreateUserAccount(ctx context.Context, user UserAccount) error {

	user.Status = "enabled"
	urlValues := make(url.Values)
	urlValues.Set("accessKey", user.AccessKey)

	//#nosec G117 — AccessKey is a public identifier, not a secret
	bytes, err := json.Marshal(user)
	if err != nil {
		return err
	}

	req_data := RequestData{
		Method:      "PUT",
		RelPath:     "add-user",
		Content:     bytes,
		QueryValues: urlValues,
	}

	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)

	if user.Policy != "" {
		return c.AttachPolicyToUser(ctx, user.AccessKey, user.Policy)
	}
	return err
}

func (c *RustfsAdmin) ReadUserAccount(ctx context.Context, name string) (UserAccount, error) {
	var instance UserAccount
	urlValues := make(url.Values)
	urlValues.Set("accessKey", name)
	req_data := RequestData{
		Method:      "GET",
		RelPath:     "user-info",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return instance, err
	}
	defer drainClose(resp)
	err = json.NewDecoder(resp.Body).Decode(&instance)
	instance.AccessKey = name
	return instance, err

}

func (c *RustfsAdmin) DeleteUserAccount(ctx context.Context, account UserAccount) error {
	urlValues := make(url.Values)
	urlValues.Set("accessKey", account.AccessKey)
	req_data := RequestData{
		Method:      "DELETE",
		RelPath:     "remove-user",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// AttachPolicyToUser attaches a policy to an existing user. It is the policy
// follow-up of CreateUserAccount; no Terraform resource calls it directly
// (rustfs_user_policy_attachment owns its own attach/detach pair).
func (c *RustfsAdmin) AttachPolicyToUser(ctx context.Context, user string, policy string) error {
	urlValues := make(url.Values)
	urlValues.Set("userOrGroup", user)
	urlValues.Set("policyName", policy)
	urlValues.Set("isGroup", "false")
	req_data := RequestData{
		Method:      "PUT",
		RelPath:     "set-user-or-group-policy",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// SetUserSecretKey rotates the user's secret key in place.
func (c *RustfsAdmin) SetUserSecretKey(ctx context.Context, accessKey, secretKey string) error {
	urlValues := make(url.Values)
	urlValues.Set("accessKey", accessKey)
	body := struct {
		SecretKey string `json:"secret_key"`
	}{SecretKey: secretKey}
	bytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req_data := RequestData{
		Method:      "PUT",
		RelPath:     "set-user-secret-key",
		Content:     bytes,
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// SetUserStatus enables or disables an existing user.
func (c *RustfsAdmin) SetUserStatus(ctx context.Context, accessKey, status string) error {
	urlValues := make(url.Values)
	urlValues.Set("accessKey", accessKey)
	urlValues.Set("status", status)
	req_data := RequestData{
		Method:      "PUT",
		RelPath:     "set-user-status",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}
