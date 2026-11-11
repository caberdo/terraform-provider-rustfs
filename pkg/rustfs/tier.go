// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const (
	// FeatureTierList is the tier listing read by the rustfs_tiers data source
	// (GET /rustfs/admin/v3/tier).
	FeatureTierList Feature = "tier listing"

	// FeatureTierDeletionCheck explains why a tier cannot be removed
	// (GET /rustfs/admin/v3/tier/{tier}/deletion-check). RustFS 1.0.5 added
	// the route; older servers answer 501 NotImplemented.
	FeatureTierDeletionCheck Feature = "tier deletion check"

	// FeatureTierCleanupRetry enqueues durable free-version cleanup for a tier
	// (POST /rustfs/admin/v3/tier/{tier}/cleanup-retry). RustFS 1.0.5 added the
	// route; older servers answer 501 NotImplemented.
	FeatureTierCleanupRetry Feature = "tier cleanup retry"
)

func init() {
	registerFeature(FeatureTierList, featureSpec{
		route: "GET /rustfs/admin/v3/tier",
		// The route is implemented in RustFS 1.0.1 through 1.0.3 and later.
	})
	registerFeature(FeatureTierDeletionCheck, featureSpec{
		route:      "GET /rustfs/admin/v3/tier/{tier}/deletion-check",
		minVersion: &Version{Major: 1, Minor: 0, Patch: 5, Raw: "1.0.5"},
	})
	registerFeature(FeatureTierCleanupRetry, featureSpec{
		route:      "POST /rustfs/admin/v3/tier/{tier}/cleanup-retry",
		minVersion: &Version{Major: 1, Minor: 0, Patch: 5, Raw: "1.0.5"},
	})
}

func (c *RustfsAdmin) AddTier(ctx context.Context, config json.RawMessage) error {
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "tier",
		Content: []byte(config),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

func (c *RustfsAdmin) EditTier(ctx context.Context, name string, config json.RawMessage) error {
	reqData := RequestData{
		Method:  "POST",
		RelPath: "tier/" + name,
		Content: []byte(config),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

// RemoveTier deletes a tier's registry record (DELETE /rustfs/admin/v3/tier/{tier}).
//
// force sends ?force=true, which RustFS 1.0.5 makes skip driver construction:
// the record is removed even when the backend endpoint is unroutable or private
// and the server therefore cannot probe it. It never deletes remote data. A
// tier the server still considers in use is refused with
// XMinioAdminTierBackendNotEmpty or XMinioAdminTierBackendInUse; callers can
// explain that refusal through CheckTierDeletion.
func (c *RustfsAdmin) RemoveTier(ctx context.Context, name string, force bool) error {
	reqData := RequestData{
		Method:  http.MethodDelete,
		RelPath: "tier/" + name,
	}
	if force {
		reqData.QueryValues = url.Values{"force": []string{"true"}}
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}

// TierDeletionReason is one blocker the server reports for a tier removal. The
// server sends a kind plus kind-specific detail; the provider pins the kind
// only, because RustFS adds kinds and fields as deletion proof grows and a
// typed field that stops matching must not turn the route into a decode
// failure. The known kinds are SCREAMING_SNAKE_CASE, for example
// LIFECYCLE_REFERENCE, LIVE_TRANSITIONED_VERSION, CLEANUP_PENDING and
// TRANSITION_PENDING.
type TierDeletionReason struct {
	Kind string `json:"kind"`
}

// TierDeletionCheck is the server's read-only verdict on whether a tier can be
// removed (GET /rustfs/admin/v3/tier/{tier}/deletion-check).
//
// ProofComplete reports whether the scan that produced the verdict finished;
// ScanStatus is "complete", "stopped_after_blocker" or "incomplete". When the
// proof is incomplete the reasons list is a lower bound, not a complete list.
// Remote is the backend's own section and stays raw JSON: the provider does not
// pin a shape RustFS has not committed to.
type TierDeletionCheck struct {
	TierName      string               `json:"tier_name"`
	ProofComplete bool                 `json:"proof_complete"`
	ScanStatus    string               `json:"scan_status"`
	Reasons       []TierDeletionReason `json:"reasons"`
	Remote        json.RawMessage      `json:"remote"`
}

// CheckTierDeletion reports why RustFS would refuse to remove a tier and which
// blockers still exist. The route is read-only: it scans tier metadata but
// starts no cleanup and changes nothing. RustFS 1.0.5 added it, so an older
// server answers with an *UnsupportedFeatureError naming the route.
func (c *RustfsAdmin) CheckTierDeletion(ctx context.Context, name string) (TierDeletionCheck, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "tier/" + name + "/deletion-check",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return TierDeletionCheck{}, c.featureErrorPreservingCode(ctx, err, FeatureTierDeletionCheck)
	}
	defer drainClose(resp)

	var check TierDeletionCheck
	if err := json.NewDecoder(resp.Body).Decode(&check); err != nil {
		return TierDeletionCheck{}, err
	}
	return check, nil
}

// TierCleanupRetry is the server's answer to a cleanup-retry request
// (POST /rustfs/admin/v3/tier/{tier}/cleanup-retry). Status stays a string
// because the accepted answer is not the only possible value; Enqueued and
// Failed count the durable free-version cleanup tasks the server queued and
// dropped.
type TierCleanupRetry struct {
	Status   string `json:"status"`
	Enqueued int    `json:"enqueued"`
	Failed   int    `json:"failed"`
}

// RetryTierCleanup asks RustFS to enqueue durable free-version cleanup for a
// tier that is blocked on CLEANUP_PENDING entries
// (POST /rustfs/admin/v3/tier/{tier}/cleanup-retry). The route mutates: it
// starts work, so the provider never calls it implicitly. RustFS answers 202.
// RustFS 1.0.5 added the route, so an older server answers with an
// *UnsupportedFeatureError naming the route.
func (c *RustfsAdmin) RetryTierCleanup(ctx context.Context, name string) (TierCleanupRetry, error) {
	reqData := RequestData{
		Method:  http.MethodPost,
		RelPath: "tier/" + name + "/cleanup-retry",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return TierCleanupRetry{}, c.featureErrorPreservingCode(ctx, err, FeatureTierCleanupRetry)
	}
	defer drainClose(resp)

	var retry TierCleanupRetry
	if err := json.NewDecoder(resp.Body).Decode(&retry); err != nil {
		return TierCleanupRetry{}, err
	}
	return retry, nil
}

// TierExists reports whether the server holds a tier by that name. RustFS
// answers GET tier/<name> with 200 for a configured tier and 500 TierNotFound
// for one it does not have (probed on 1.0.1 and latest), so the answer is the
// server's own verdict on that exact name rather than something inferred from a
// list that may be incomplete. The name is matched exactly: the route is
// case-sensitive.
//
// Any other failure, including a server that does not implement the route, is
// returned as an error so the caller can decide whether it can verify at all.
func (c *RustfsAdmin) TierExists(ctx context.Context, name string) (bool, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "tier/" + name,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		if IsAPIErrorCode(err, "TierNotFound", "NoSuchTier") {
			return false, nil
		}
		return false, err
	}
	defer drainClose(resp)
	return true, nil
}

// TierInfo identifies a configured tier, the backend type the server reports
// for it and the backend's own configuration.
//
// The listing is the only route that returns a tier's backend configuration,
// and the server redacts the credentials it knows about before answering. The
// provider redacts them again on its side: a server that stops redacting one
// key must not turn this listing into a secret leak.
type TierInfo struct {
	Name     string
	TierType string
	// Config is the backend object (the value nested under TierType) as
	// compact JSON, with every known credential key replaced by REDACTED. It
	// is empty when the server entry cannot be encoded.
	Config string
}

// tierSecretKeys names every backend field that carries credential material.
// The names cover the backends RustFS supports (s3 and compatible stores,
// Azure, GCS, Alibaba Cloud, Tencent Cloud, Huawei Cloud, R2) plus the AWS role
// fields, which are token material too.
var tierSecretKeys = map[string]struct{}{
	"secret_key":                       {},
	"secretKey":                        {},
	"client_secret":                    {},
	"clientSecret":                     {},
	"sas_token":                        {},
	"sasToken":                         {},
	"account_key":                      {},
	"accountKey":                       {},
	"session_token":                    {},
	"sessionToken":                     {},
	"aws_role_web_identity_token":      {},
	"aws_role_web_identity_token_file": {},
}

// redactedTierValue is what a credential field is replaced with.
const redactedTierValue = "REDACTED"

// redactTierSecrets encodes a tier backend as compact JSON with every
// credential value replaced by REDACTED. It walks maps and arrays, so a
// credential nested in a sub-object is caught too. It returns an empty string
// when the value cannot be encoded, which the caller treats as "no configuration
// read" instead of failing the whole listing.
func redactTierSecrets(backend any) string {
	encoded, err := json.Marshal(redactTierValue(backend))
	if err != nil {
		return ""
	}
	return string(encoded)
}

// redactTierValue rewrites credential values in place. Only non-empty strings
// are replaced: a null or empty credential stays as the server reported it, so
// "not configured" is not disguised as "redacted".
func redactTierValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, entry := range typed {
			typed[key] = redactTierValue(entry)
			if _, secret := tierSecretKeys[key]; !secret {
				continue
			}
			if text, ok := typed[key].(string); ok && text != "" {
				typed[key] = redactedTierValue
			}
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = redactTierValue(typed[i])
		}
		return typed
	default:
		return value
	}
}

// ListTiers returns every configured tier with the backend type and redacted
// backend configuration the server reports for it. GET tier answers a JSON
// array whose entries nest the backend under the type name
// ({"type":"rustfs","rustfs":{"name":"COLD",...}}); the tier name is the
// backend's own name field, because the enclosing object omits it. The result
// is sorted by name so Terraform state is deterministic.
func (c *RustfsAdmin) ListTiers(ctx context.Context) ([]TierInfo, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "tier",
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureErrorPreservingCode(ctx, err, FeatureTierList)
	}
	defer drainClose(resp)

	var entries []map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}

	tiers := make([]TierInfo, 0, len(entries))
	for _, entry := range entries {
		var tierType string
		if raw, ok := entry["type"]; ok {
			if err := json.Unmarshal(raw, &tierType); err != nil {
				continue
			}
		}

		var name string
		var config string
		if raw, ok := entry[tierType]; ok {
			var backend map[string]any
			if err := json.Unmarshal(raw, &backend); err != nil {
				continue
			}
			name, _ = backend["name"].(string)
			config = redactTierSecrets(backend)
		}
		// An entry the server cannot name is not usable: the caller matches
		// tiers by name and would never find it. Skip it instead of failing
		// the whole listing for one malformed entry.
		if name == "" {
			continue
		}
		tiers = append(tiers, TierInfo{Name: name, TierType: tierType, Config: config})
	}
	slices.SortFunc(tiers, func(a, b TierInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	return tiers, nil
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
func (c *RustfsAdmin) TierStats(ctx context.Context) (map[string]TierStat, error) {
	reqData := RequestData{
		Method:  "GET",
		RelPath: "tier-stats",
	}
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
