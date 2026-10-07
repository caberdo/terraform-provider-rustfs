package client

import (
	"context"
	"encoding/json"
	"net/url"
)

func (c *RustfsAdmin) AddTier(config json.RawMessage) error {
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "tier",
		Content: []byte(config),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *RustfsAdmin) EditTier(name string, config json.RawMessage) error {
	reqData := RequestData{
		Method:  "POST",
		RelPath: "tier/" + name,
		Content: []byte(config),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *RustfsAdmin) RemoveTier(name string) error {
	reqData := RequestData{
		Method:  "DELETE",
		RelPath: "tier/" + name,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// TierStat holds per-tier usage statistics as reported by the tier-stats
// endpoint.
type TierStat struct {
	TotalSize   int64 `json:"total_size"`
	NumVersions int64 `json:"num_versions"`
	NumObjects  int64 `json:"num_objects"`
}

// TierStats returns per-tier usage statistics keyed by tier name. When no
// tiers are configured the server returns an empty map.
func (c *RustfsAdmin) TierStats() (map[string]TierStat, error) {
	// RustFS contract v2 returns an envelope; the flat tier-name => counters map
	// this client decodes is served by the documented legacy format.
	reqData := RequestData{
		Method:      "GET",
		RelPath:     "tier-stats",
		QueryValues: url.Values{"format": []string{"legacy"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var stats map[string]TierStat
	err = json.NewDecoder(resp.Body).Decode(&stats)
	return stats, err
}
