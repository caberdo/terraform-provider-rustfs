// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// FeatureConfigHistory is the config snapshot list read by the
// rustfs_config_history data source (GET list-config-history-kv).
const FeatureConfigHistory Feature = "config history"

// FeatureConfigRestore is the rollback performed by the rustfs_config_restore
// resource (PUT restore-config-history-kv).
const FeatureConfigRestore Feature = "config restore"

// FeatureConfigHistoryClear is the history purge performed by the
// rustfs_config_history_clear resource (DELETE clear-config-history-kv).
const FeatureConfigHistoryClear Feature = "config history clear"

func init() {
	registerFeature(FeatureConfigHistory, featureSpec{
		route: "GET /rustfs/admin/v3/list-config-history-kv",
		// The route is registered and implemented in the tagged RustFS source
		// for 1.0.1 through 1.0.3, so there is no version floor to advertise.
	})
	registerFeature(FeatureConfigRestore, featureSpec{
		route: "PUT /rustfs/admin/v3/restore-config-history-kv",
	})
	registerFeature(FeatureConfigHistoryClear, featureSpec{
		route: "DELETE /rustfs/admin/v3/clear-config-history-kv",
	})
}

// ConfigKV is a single key/value pair of a sub-system config scope.
type ConfigKV struct {
	Key   string
	Value string
}

// GetConfig returns the current KV settings of a config scope from GET get-config-kv.
//
// subSystem is a config scope: either a bare sub-system (e.g. "scanner") or a
// targeted sub-system (e.g. "notify_webhook:primary"). The server renders the
// scope as text lines in the form `scope key="value" key2="value2"`.
func (c *RustfsAdmin) GetConfig(ctx context.Context, subSystem string) ([]ConfigKV, error) {
	query := make(url.Values)
	query.Set("key", subSystem)
	reqData := RequestData{
		Method:      "GET",
		RelPath:     "get-config-kv",
		QueryValues: query,
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
	return parseConfigKVs(string(body))
}

// SetConfig sets the KV settings of a config scope via PUT set-config-kv.
//
// The request body is a plain text config directive in the same format the
// server renders: `scope key="value" key2="value2"`.
func (c *RustfsAdmin) SetConfig(ctx context.Context, subSystem string, kvs []ConfigKV) error {
	reqData := RequestData{
		Method:  "PUT",
		RelPath: "set-config-kv",
		Content: []byte(buildConfigDirective(subSystem, kvs)),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// DeleteConfig removes a config scope via DELETE del-config-kv. The whole
// sub-system or the named target is removed; keys that are not explicitly
// listed are also removed, so this resets the scope to the server defaults.
func (c *RustfsAdmin) DeleteConfig(ctx context.Context, subSystem string) error {
	reqData := RequestData{
		Method:  "DELETE",
		RelPath: "del-config-kv",
		Content: []byte(subSystem),
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	drainClose(resp)
	return nil
}

// ConfigHistoryEntry is one saved server-config snapshot from
// GET list-config-history-kv.
//
// RustFS writes a snapshot before every config mutation, including a restore,
// so the entry a mutation created is the way back to the state before it. Data
// is the snapshot rendered in the same directive text as get-config-kv with
// secret-like values replaced by the server; DataRedacted reports that the
// server did so. The list carries no actor: the route does not record who made
// the change.
type ConfigHistoryEntry struct {
	RestoreID    string `json:"RestoreID"`
	CreateTime   string `json:"CreateTime"`
	Data         string `json:"Data"`
	DataRedacted bool   `json:"DataRedacted"`
}

// ListConfigHistory reads GET list-config-history-kv.
//
// The server requires a count and keeps at most that many newest entries. It
// returns whole-server snapshots, not per-subsystem diffs, so every entry
// describes the same config scopes.
func (c *RustfsAdmin) ListConfigHistory(ctx context.Context, count int) ([]ConfigHistoryEntry, error) {
	if count < 1 {
		return nil, fmt.Errorf("config history count must be at least 1: %d", count)
	}

	query := make(url.Values)
	query.Set("count", strconv.Itoa(count))
	reqData := RequestData{
		Method:      "GET",
		RelPath:     "list-config-history-kv",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, c.featureError(ctx, err, FeatureConfigHistory)
	}
	defer drainClose(resp)

	var entries []ConfigHistoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode config history: %w", err)
	}
	return entries, nil
}

// RestoreConfigHistory replaces the live configuration with the snapshot named
// by restoreID via PUT restore-config-history-kv.
//
// The restore overwrites every config scope, not just the one that changed, so
// it can undo another operator's unrelated change. The server writes a new
// history entry for the current configuration before applying the snapshot,
// which is what makes a restore itself revertible. A missing or legacy entry
// is reported by the server; the provider does not fall back to another entry.
func (c *RustfsAdmin) RestoreConfigHistory(ctx context.Context, restoreID string) error {
	if restoreID == "" {
		return errors.New("restore id is required")
	}

	query := make(url.Values)
	query.Set("restoreId", restoreID)
	reqData := RequestData{
		Method:      "PUT",
		RelPath:     "restore-config-history-kv",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		// A missing entry is a semantic 404 (NoSuchKey); only a bare 404/501
		// means the route is not implemented.
		return c.featureErrorPreservingCode(ctx, err, FeatureConfigRestore)
	}
	drainClose(resp)
	return nil
}

// ClearConfigHistory deletes every stored config snapshot via
// DELETE clear-config-history-kv with restoreId=all.
//
// The route always clears the whole history: it takes no per-entry identifier
// and returns an empty 200 without a count, so the provider cannot report how
// many entries it removed. The operation is idempotent; clearing an already
// empty history succeeds. Clearing removes the rollback points the restore
// resource reaches, so a later restore can no longer roll back to them.
func (c *RustfsAdmin) ClearConfigHistory(ctx context.Context) error {
	query := make(url.Values)
	query.Set("restoreId", "all")
	reqData := RequestData{
		Method:      "DELETE",
		RelPath:     "clear-config-history-kv",
		QueryValues: query,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return c.featureError(ctx, err, FeatureConfigHistoryClear)
	}
	drainClose(resp)
	return nil
}

// buildConfigDirective renders a config scope and its key/value pairs in the
// text format the set-config-kv endpoint accepts and the server renders.
func buildConfigDirective(subSystem string, kvs []ConfigKV) string {
	parts := make([]string, 0, len(kvs)+1)
	parts = append(parts, subSystem)
	for _, kv := range kvs {
		parts = append(parts, kv.Key+`="`+escapeConfigValue(kv.Value)+`"`)
	}
	return strings.Join(parts, " ")
}

// escapeConfigValue mirrors the server-side escaping of config values.
func escapeConfigValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

// parseConfigKVs parses the text/plain body returned by get-config-kv into a
// flat list of key/value pairs. Comment lines (env override hints) are skipped.
func parseConfigKVs(text string) ([]ConfigKV, error) {
	var kvs []ConfigKV
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lineKVs, err := parseConfigLine(line)
		if err != nil {
			return nil, err
		}
		kvs = append(kvs, lineKVs...)
	}
	return kvs, nil
}

// parseConfigLine parses a single rendered config line of the form
// `scope key="value" key2="value2"` and returns its key/value pairs.
func parseConfigLine(line string) ([]ConfigKV, error) {
	tokens, err := tokenizeConfigLine(line)
	if err != nil {
		return nil, err
	}
	if len(tokens) < 2 {
		return nil, nil
	}
	kvs := make([]ConfigKV, 0, len(tokens)-1)
	for _, token := range tokens[1:] {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			return nil, fmt.Errorf("config assignment must use key=value syntax: %q", token)
		}
		kvs = append(kvs, ConfigKV{Key: key, Value: value})
	}
	return kvs, nil
}

// tokenizeConfigLine splits a config line into tokens, honoring the quoting and
// backslash escaping rules used by the server's config parser.
func tokenizeConfigLine(line string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	var quote byte
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				current.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case '"', '\'':
			quote = ch
		case ' ', '\t':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if escaped {
		current.WriteByte('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted config value")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}
