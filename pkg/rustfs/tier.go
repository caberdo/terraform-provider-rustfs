package rustfs

import (
	"context"
	"encoding/json"
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
	defer drainClose(resp)
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
	defer drainClose(resp)
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
	defer drainClose(resp)
	return nil
}

// TierStat holds a tier's total size, version count and object count.
type TierStat struct {
	TotalSize   int64 `json:"totalSize"`
	NumVersions int64 `json:"numVersions"`
	NumObjects  int64 `json:"numObjects"`
}

// tierStatsEnvelope is the version 2 body of GET tier-stats (the server
// default). Version 1 was a bare map of tier name to the answering process's
// rolling counters and is only returned under ?format=legacy; v2 separates the
// cluster-wide stored inventory from the rolling 24-hour transition activity.
type tierStatsEnvelope struct {
	ContractVersion int            `json:"contractVersion"`
	Tiers           []tierInfoBody `json:"tiers"`
}

type tierInfoBody struct {
	Name string `json:"name"`
	// Inventory is absent whenever the scanner has not accounted the tier yet
	// ("not accounted", never zero).
	Inventory          *TierStat `json:"inventory"`
	TransitionsLast24h TierStat  `json:"transitionsLast24h"`
}

// TierStats returns per-tier usage statistics keyed by tier name. It surfaces
// the cluster-wide stored inventory and falls back to the rolling 24-hour
// transition counters when the scanner has not accounted the tier yet. When no
// tiers are configured the server returns an empty list.
func (c *RustfsAdmin) TierStats() (map[string]TierStat, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "tier-stats",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	var body tierStatsEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	stats := make(map[string]TierStat, len(body.Tiers))
	for _, tier := range body.Tiers {
		if tier.Inventory != nil {
			stats[tier.Name] = *tier.Inventory
			continue
		}
		stats[tier.Name] = tier.TransitionsLast24h
	}
	return stats, nil
}
