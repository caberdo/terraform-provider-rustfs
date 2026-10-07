package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

// ListCannedPolicies returns the names of all canned policies. The API may
// return an array of {policy_name} objects, an array of plain names, or a
// map keyed by policy name; all three shapes are normalized to a sorted list.
func (c *RustfsAdmin) ListCannedPolicies() ([]string, error) {
	req_data := RequestData{
		Method:  "GET",
		RelPath: "list-canned-policies",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, req_data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawList []json.RawMessage
	if err := json.Unmarshal(body, &rawList); err == nil {
		names := make([]string, 0, len(rawList))
		for _, raw := range rawList {
			var plain string
			if json.Unmarshal(raw, &plain) == nil {
				names = append(names, plain)
				continue
			}
			var obj struct {
				PolicyName string `json:"policy_name"`
			}
			if json.Unmarshal(raw, &obj) == nil {
				names = append(names, obj.PolicyName)
			}
		}
		sort.Strings(names)
		return names, nil
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err == nil {
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}

	return nil, errors.New("unrecognized list-canned-policies response")
}
