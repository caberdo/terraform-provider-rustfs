// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/url"
)

type ServiceAccount struct {
	AccessKey     string `json:"accessKey"`
	SecretKey     string `json:"secretKey"`
	Description   string `json:"description"`
	Expiration    string `json:"expiration,omitempty"`
	Expiry        bool   `json:"expiry"`
	Name          string `json:"name"`
	ImpliedPolicy bool   `json:"impliedPolicy"`
	Policy        string `json:"policy,omitempty"`
	TargetUser    string `json:"targetUser,omitempty"`
	ParentUser    string `json:"parentUser,omitempty"`
	// AccountStatus is reported by info-service-account and
	// temporary-account-info, for example "on" or "off". It is not part of the
	// create/update request.
	AccountStatus string `json:"accountStatus,omitempty"`
}

type ServiceAccountUpdate struct {
	NewAccessKey   string `json:"newAccessKey"`
	NewSecretKey   string `json:"newSecretKey"`
	NewDescription string `json:"newDescription"`
	NewExpiration  string `json:"newExpiration,omitempty"`
	NewName        string `json:"newName"`
}

type serviceAccountCredentails struct {
	AccessKey  string `json:"accessKey"`
	SecretKey  string `json:"secretKey"`
	Expiration string `json:"expiration"`
}

type ServiceAccountReply struct {
	Credentials serviceAccountCredentails `json:"credentials"`
}

// FeatureServiceAccountList is the IAM service-account listing
// (GET /rustfs/admin/v3/list-service-accounts).
const FeatureServiceAccountList Feature = "service account listing"

// serviceAccountListEnvelope is the wire shape of list-service-accounts: the
// accounts are wrapped in an object, not returned as a bare array.
type serviceAccountListEnvelope struct {
	Accounts []ServiceAccount `json:"accounts"`
}

// ListServiceAccounts returns every IAM service account the server knows.
//
// The route exists in RustFS 1.0.1 through 1.0.3 and in latest, so it needs no
// version floor. A server that does not implement it answers 501, which
// featureError turns into an UnsupportedFeatureError naming the route.
//
// SecretKey is cleared on every returned account regardless of the payload: a
// conforming server never sends one, and a server regression must not be able
// to push key material into Terraform state.
func (c *RustfsAdmin) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	return c.ListServiceAccountsFor(ctx, "")
}

// ListServiceAccountsFor lists the IAM service accounts of one user. An empty
// user asks for the calling user's accounts; a different user is a cross-user
// listing that RustFS authorizes with the ListServiceAccounts admin action.
//
// The route exists in RustFS 1.0.1 through 1.0.3 and in latest, so it needs no
// version floor. A server that does not implement it answers 501, which
// featureError turns into an UnsupportedFeatureError naming the route.
//
// SecretKey is cleared on every returned account regardless of the payload: a
// conforming server never sends one, and a server regression must not be able
// to push key material into Terraform state.
func (c *RustfsAdmin) ListServiceAccountsFor(ctx context.Context, user string) ([]ServiceAccount, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "list-service-accounts",
	}
	if user != "" {
		reqData.QueryValues = map[string][]string{"user": {user}}
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, FeatureServiceAccountList)
	}
	defer drainClose(resp)

	var body serviceAccountListEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	for i := range body.Accounts {
		body.Accounts[i].SecretKey = ""
	}
	return body.Accounts, nil
}

func init() {
	registerFeature(FeatureServiceAccountList, featureSpec{
		route: "GET /rustfs/admin/v3/list-service-accounts",
	})
}

func (c *RustfsAdmin) CreateServiceAccount(ctx context.Context, account ServiceAccount) error {
	normalizeServiceAccount(&account)
	//#nosec G117 — AccessKey is a public identifier, not a secret
	bytes, err := json.Marshal(account)
	if err != nil {
		return err
	}
	req_data := RequestData{
		Method:  "PUT",
		RelPath: "add-service-accounts",
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	var is ServiceAccountReply
	err = json.NewDecoder(resp.Body).Decode(&is)
	return err
}

// ReadServiceAccount reads one service account. A cancelled refresh stops the
// request.
func (c *RustfsAdmin) ReadServiceAccount(ctx context.Context, name string) (ServiceAccount, error) {
	var instance ServiceAccount
	urlValues := make(url.Values)
	urlValues.Set("accessKey", name)
	req_data := RequestData{
		Method:      "GET",
		RelPath:     "info-service-account",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return instance, err
	}
	defer drainClose(resp)
	err = json.NewDecoder(resp.Body).Decode(&instance)
	return instance, err
}

func (c *RustfsAdmin) UpdateServiceAccount(ctx context.Context, account ServiceAccount) error {
	normalizeServiceAccount(&account)
	updateRequest := createUpdate(account)
	urlValues := make(url.Values)
	urlValues.Set("accessKey", account.AccessKey)
	bytes, err := json.Marshal(updateRequest)
	if err != nil {
		return err
	}
	req_data := RequestData{
		Method:      "POST",
		RelPath:     "update-service-account",
		QueryValues: urlValues,
		Content:     bytes,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

func (c *RustfsAdmin) DeleteServiceAccount(ctx context.Context, account ServiceAccount) error {
	normalizeServiceAccount(&account)
	urlValues := make(url.Values)
	urlValues.Set("accessKey", account.AccessKey)
	req_data := RequestData{
		Method:      "DELETE",
		RelPath:     "delete-service-accounts",
		QueryValues: urlValues,
	}
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

func normalizeServiceAccount(account *ServiceAccount) {
	// Set some defaults
	if account.Expiration == "" {
		account.Expiration = "9999-01-01T00:00:00Z"
	}
	if account.Policy == "" {
		account.ImpliedPolicy = true
	}
}

func createUpdate(account ServiceAccount) ServiceAccountUpdate {
	return ServiceAccountUpdate{
		NewAccessKey:   account.AccessKey,
		NewSecretKey:   account.SecretKey,
		NewDescription: account.Description,
		NewExpiration:  account.Expiration,
		NewName:        account.Name,
	}
}
