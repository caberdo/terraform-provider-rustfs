// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
)

type GroupInfo struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Members []string `json:"members"`
	Policy  string   `json:"policy"`
}

// Policies returns the canned policies attached to the group. The admin API
// stores and returns a group's policies as one comma-separated string, so an
// empty (or unset) policy field yields an empty slice.
func (g GroupInfo) Policies() []string {
	policies := []string{}
	for _, name := range strings.Split(g.Policy, ",") {
		if name = strings.TrimSpace(name); name != "" {
			policies = append(policies, name)
		}
	}
	return policies
}

type GroupAddRemove struct {
	Group    string   `json:"group"`
	Members  []string `json:"members"`
	IsRemove bool     `json:"isRemove"`
	Status   string   `json:"groupStatus"`
}

// ListGroups returns the names of all IAM groups. The API returns a plain
// JSON array of group name strings; a {"groups": [...]} wrapper is also
// accepted for forward compatibility. Names are sorted for deterministic
// output.
func (c *RustfsAdmin) ListGroups(ctx context.Context) ([]string, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "groups",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var names []string
	if err := json.Unmarshal(body, &names); err == nil {
		sort.Strings(names)
		return names, nil
	}

	var wrapped struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		sort.Strings(wrapped.Groups)
		return wrapped.Groups, nil
	}

	return nil, errors.New("unrecognized groups response")
}

func (c *RustfsAdmin) GetGroup(ctx context.Context, name string) (GroupInfo, error) {
	query := url.Values{}
	query.Set("group", name)
	reqData := RequestData{
		Method:      "GET",
		RelPath:     "group",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return GroupInfo{}, err
	}
	defer drainClose(resp)
	var info GroupInfo
	err = json.NewDecoder(resp.Body).Decode(&info)
	return info, err
}

func (c *RustfsAdmin) UpdateGroupMembers(ctx context.Context, req GroupAddRemove) error {
	if req.Members == nil {
		req.Members = []string{}
	}
	bytes, err := json.Marshal(req)
	if err != nil {
		return err
	}
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "update-group-members",
		Content: bytes,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

func (c *RustfsAdmin) DeleteGroup(ctx context.Context, name string) error {
	reqData := RequestData{
		Method:  "DELETE",
		RelPath: "group/" + name,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

func (c *RustfsAdmin) SetGroupStatus(ctx context.Context, name, status string) error {
	query := url.Values{}
	query.Set("group", name)
	query.Set("status", status)
	reqData := RequestData{
		Method:      "PUT",
		RelPath:     "set-group-status",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
