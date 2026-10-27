// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// PolicyCondition is the condition block of a policy statement: it maps a
// condition operator (for example "StringLike") to the condition keys it
// applies to and their values.
//
// The IAM policy grammar allows a condition value to be a string, a boolean, a
// number or a list of those; PolicyCondition decodes all four into a list of
// strings so the Terraform state stays stable across reads.
type PolicyCondition map[string]map[string][]string

// UnmarshalJSON normalizes every condition value to a list of strings.
func (c *PolicyCondition) UnmarshalJSON(data []byte) error {
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("condition: %w", err)
	}
	if raw == nil {
		*c = nil
		return nil
	}

	condition := make(PolicyCondition, len(raw))
	for operator, keys := range raw {
		values := make(map[string][]string, len(keys))
		for key, value := range keys {
			normalized, err := policyConditionValues(value)
			if err != nil {
				return fmt.Errorf("condition %q key %q: %w", operator, key, err)
			}
			values[key] = normalized
		}
		condition[operator] = values
	}
	*c = condition
	return nil
}

// policyConditionValues normalizes one condition value to a list of strings,
// accepting either a single scalar or a list of scalars.
func policyConditionValues(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
		values := make([]string, 0, len(items))
		for _, item := range items {
			value, err := policyConditionScalar(item)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	}

	value, err := policyConditionScalar(trimmed)
	if err != nil {
		return nil, err
	}
	return []string{value}, nil
}

// policyConditionScalar renders one string, boolean, number or null JSON
// condition value as a string. Numbers keep their original representation
// because the document is decoded with json.Number.
func policyConditionScalar(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", err
	}

	switch value := decoded.(type) {
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	case json.Number:
		return value.String(), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported condition value %s", string(raw))
	}
}

type PolicyStatement struct {
	Effect      string          `json:"Effect"`
	Sid         string          `json:"Sid,omitempty"`
	Action      []string        `json:"Action,omitempty"`
	NotAction   []string        `json:"NotAction,omitempty"`
	Resource    []string        `json:"Resource,omitempty"`
	NotResource []string        `json:"NotResource,omitempty"`
	Condition   PolicyCondition `json:"Condition,omitempty"`
}

type PolicyStatementSinle struct {
	Effect      string          `json:"Effect"`
	Sid         string          `json:"Sid"`
	Action      string          `json:"Action"`
	NotAction   string          `json:"NotAction"`
	Resource    []string        `json:"Resource"`
	NotResource []string        `json:"NotResource"`
	Condition   PolicyCondition `json:"Condition"`
}

type Policy struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
	Name      string
}

type statementReply struct {
	Statement []PolicyStatement `json:"Statement"`
}

type statementReplySingle struct {
	Statement []PolicyStatementSinle `json:"Statement"`
}

type policyReply struct {
	PolicyName string          `json:"policy_name"`
	Policy     json.RawMessage `json:"policy"`
}

type policyPost struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

func (c *RustfsAdmin) CreatePolicy(ctx context.Context, policy Policy) error {
	urlValues := make(url.Values)
	urlValues.Set("name", policy.Name)
	policy.Version = "2012-10-17" // only this is working
	post := policyPost{
		Version:   policy.Version,
		Statement: policy.Statement,
	}
	bytes, err := json.Marshal(post)
	if err != nil {
		return err
	}

	req_data := RequestData{
		Method:      "PUT",
		RelPath:     "add-canned-policy",
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

// parsePolicyField decodes the "policy" field of an info-canned-policy reply
// into statements.
//
// The RustFS API may return `policy` either as a JSON string containing the
// policy document, or as an inline JSON object, so the field is normalized to
// bytes first. A document whose Action is a single string (rather than a list)
// is accepted as well. The document comes from the server and is therefore
// untrusted: malformed input must produce an error, never a panic.
func parsePolicyField(policy json.RawMessage) ([]PolicyStatement, error) {
	policyBytes := []byte(policy)
	if len(policyBytes) > 0 && policyBytes[0] == '"' {
		var asString string
		if err := json.Unmarshal(policyBytes, &asString); err != nil {
			return nil, err
		}
		policyBytes = []byte(asString)
	}

	var statement statementReply
	errStatement := json.NewDecoder(bytes.NewReader(policyBytes)).Decode(&statement)
	if errStatement == nil {
		return statement.Statement, nil
	}

	var statementSingle statementReplySingle
	errSingle := json.NewDecoder(bytes.NewReader(policyBytes)).Decode(&statementSingle)
	if errSingle == nil {
		statements := make([]PolicyStatement, 0, len(statementSingle.Statement))
		for _, got := range statementSingle.Statement {
			statement := PolicyStatement{
				Effect:      got.Effect,
				Sid:         got.Sid,
				Resource:    got.Resource,
				NotResource: got.NotResource,
				Condition:   got.Condition,
			}
			if got.Action != "" {
				statement.Action = []string{got.Action}
			}
			if got.NotAction != "" {
				statement.NotAction = []string{got.NotAction}
			}
			statements = append(statements, statement)
		}
		return statements, nil
	}

	return nil, errors.Join(errStatement, errSingle)
}

func (c *RustfsAdmin) ReadPolicy(ctx context.Context, policy string) (Policy, error) {
	var instance policyReply
	var read Policy
	urlValues := make(url.Values)
	urlValues.Set("name", policy)
	req_data := RequestData{
		Method:      "GET",
		RelPath:     "info-canned-policy",
		QueryValues: urlValues,
	}

	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return Policy{}, err
	}
	defer drainClose(resp)

	err = json.NewDecoder(resp.Body).Decode(&instance)
	if err != nil {
		return Policy{}, err
	}
	read.Name = instance.PolicyName
	read.Version = "2012-10-17"

	statements, err := parsePolicyField(instance.Policy)
	if err != nil {
		return Policy{}, err
	}
	read.Statement = statements
	return read, nil
}

func (c *RustfsAdmin) DeletePolicy(ctx context.Context, policy string) error {

	urlValues := make(url.Values)
	urlValues.Set("name", policy)
	req_data := RequestData{
		Method:      "DELETE",
		RelPath:     "remove-canned-policy",
		QueryValues: urlValues,
	}

	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil

}
