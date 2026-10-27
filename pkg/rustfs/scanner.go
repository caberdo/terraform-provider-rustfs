// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FeatureScannerStatus is the scanner runtime status read by the
// rustfs_scanner_status data source (GET /rustfs/admin/v3/scanner/status).
const FeatureScannerStatus Feature = "scanner status"

// FeatureScannerCycleStateReset is the break-glass cycle-state reset
// (POST /rustfs/admin/v3/scanner/cycle-state/reset).
const FeatureScannerCycleStateReset Feature = "scanner cycle-state reset"

// FeatureScannerUsageStateReset is the destructive usage-state rebuild
// (POST /rustfs/admin/v3/scanner/usage-state/reset).
const FeatureScannerUsageStateReset Feature = "scanner usage-state reset"

// ScannerStatus is the slice of GET /v3/scanner/status the provider surfaces.
//
// The status payload is large and grows between RustFS releases; RawJSON keeps
// the exact body so a newer server field stays available to an operator while
// the typed fields stay limited to what the provider documents.
type ScannerStatus struct {
	RawJSON string

	Enabled        bool
	DisabledReason *string
	FreshnessState string

	CurrentCycle                    uint64
	CurrentCycleActive              bool
	CurrentScanMode                 string
	LastCycleEndUnixSecs            uint64
	LastCycleResult                 string
	LastCycleDurationSeconds        float64
	LastCycleObjectsScanned         uint64
	LastCycleDirectoriesScanned     uint64
	CyclesCompleted                 uint64
	FailedCycles                    uint64
	SupersededCycles                uint64
	EffectiveCycleIntervalSeconds   uint64
	UsageBootstrapRebuildPending    bool
	UsagePublicationState           string
	UsageLastDurableSuccessUnixSecs uint64
	UsageDirtyPendingBuckets        uint64
	MaintenancePrimaryControl       string
	CycleRecoveryState              string
	CycleRecoveryReason             *string
	DataMovementPaused              bool
	PauseBacklogPhase               string
}

// ScannerUsageStateResetResult is the synchronous usage-state reset answer.
// The server holds the scanner leader lock while it publishes the
// bootstrap-pending marker and clears the stale usage slots.
type ScannerUsageStateResetResult struct {
	Status      string   `json:"status"`
	Mode        string   `json:"mode"`
	UsageState  string   `json:"usage_state"`
	LeaderEpoch uint64   `json:"leader_epoch"`
	NextCycle   uint64   `json:"next_cycle"`
	ResetPaths  []string `json:"reset_paths"`
	RawJSON     string   `json:"-"`
}

// ScannerCycleStateResetResult is the synchronous cycle-state reset answer.
type ScannerCycleStateResetResult struct {
	Status  string `json:"status"`
	Mode    string `json:"mode"`
	RawJSON string `json:"-"`
}

type scannerStatusEnvelope struct {
	Enabled        bool    `json:"enabled"`
	DisabledReason *string `json:"disabled_reason"`
	Freshness      struct {
		State string `json:"state"`
	} `json:"freshness"`
	Metrics struct {
		CurrentCycle                uint64            `json:"current_cycle"`
		CurrentCycleActive          bool              `json:"current_cycle_active"`
		CurrentScanMode             string            `json:"current_scan_mode"`
		LastCycleEndUnixSecs        uint64            `json:"last_cycle_end_unix_secs"`
		LastCycleResult             string            `json:"last_cycle_result"`
		LastCycleDurationSeconds    float64           `json:"last_cycle_duration_seconds"`
		LastCycleObjectsScanned     uint64            `json:"last_cycle_objects_scanned"`
		LastCycleDirectoriesScanned uint64            `json:"last_cycle_directories_scanned"`
		CyclesCompletedAt           []json.RawMessage `json:"cycles_completed_at"`
		FailedCycles                uint64            `json:"failed_cycles"`
		SupersededCycles            uint64            `json:"superseded_cycles"`
		UsageFreshness              struct {
			DirtyPendingBuckets        uint64 `json:"dirty_pending_buckets"`
			LastPublicationState       string `json:"last_publication_state"`
			LastDurableSuccessUnixSecs uint64 `json:"last_durable_success_unix_secs"`
		} `json:"usage_freshness"`
		MaintenanceControl struct {
			PrimaryControl string `json:"primary_control"`
		} `json:"maintenance_control"`
	} `json:"metrics"`
	CycleSchedule struct {
		EffectiveIntervalSeconds     uint64 `json:"effective_interval_seconds"`
		UsageBootstrapRebuildPending bool   `json:"usage_bootstrap_rebuild_pending"`
	} `json:"cycle_schedule"`
	CycleRecovery struct {
		State  string  `json:"state"`
		Reason *string `json:"reason"`
	} `json:"cycle_recovery"`
	DataMovementPause struct {
		Paused bool `json:"paused"`
	} `json:"data_movement_pause"`
	PauseBacklog struct {
		Phase string `json:"phase"`
	} `json:"pause_backlog"`
}

// GetScannerStatus reads GET /v3/scanner/status. The route is present on
// RustFS 1.0.1 and later; a server that registers it without implementing it
// answers 501, which becomes an *UnsupportedFeatureError naming the route and
// the server version.
func (c *RustfsAdmin) GetScannerStatus(ctx context.Context) (ScannerStatus, error) {
	reqData := RequestData{
		Method:  http.MethodGet,
		RelPath: "scanner/status",
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ScannerStatus{}, c.featureError(ctx, err, FeatureScannerStatus)
	}
	defer drainClose(resp)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ScannerStatus{}, err
	}

	var envelope scannerStatusEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ScannerStatus{}, fmt.Errorf("decode scanner status: %w", err)
	}

	return ScannerStatus{
		RawJSON:                         string(raw),
		Enabled:                         envelope.Enabled,
		DisabledReason:                  envelope.DisabledReason,
		FreshnessState:                  envelope.Freshness.State,
		CurrentCycle:                    envelope.Metrics.CurrentCycle,
		CurrentCycleActive:              envelope.Metrics.CurrentCycleActive,
		CurrentScanMode:                 envelope.Metrics.CurrentScanMode,
		LastCycleEndUnixSecs:            envelope.Metrics.LastCycleEndUnixSecs,
		LastCycleResult:                 envelope.Metrics.LastCycleResult,
		LastCycleDurationSeconds:        envelope.Metrics.LastCycleDurationSeconds,
		LastCycleObjectsScanned:         envelope.Metrics.LastCycleObjectsScanned,
		LastCycleDirectoriesScanned:     envelope.Metrics.LastCycleDirectoriesScanned,
		CyclesCompleted:                 uint64(len(envelope.Metrics.CyclesCompletedAt)),
		FailedCycles:                    envelope.Metrics.FailedCycles,
		SupersededCycles:                envelope.Metrics.SupersededCycles,
		EffectiveCycleIntervalSeconds:   envelope.CycleSchedule.EffectiveIntervalSeconds,
		UsageBootstrapRebuildPending:    envelope.CycleSchedule.UsageBootstrapRebuildPending,
		UsagePublicationState:           envelope.Metrics.UsageFreshness.LastPublicationState,
		UsageLastDurableSuccessUnixSecs: envelope.Metrics.UsageFreshness.LastDurableSuccessUnixSecs,
		UsageDirtyPendingBuckets:        envelope.Metrics.UsageFreshness.DirtyPendingBuckets,
		MaintenancePrimaryControl:       envelope.Metrics.MaintenanceControl.PrimaryControl,
		CycleRecoveryState:              envelope.CycleRecovery.State,
		CycleRecoveryReason:             envelope.CycleRecovery.Reason,
		DataMovementPaused:              envelope.DataMovementPause.Paused,
		PauseBacklogPhase:               envelope.PauseBacklog.Phase,
	}, nil
}

// ResetScannerCycleState runs POST /v3/scanner/cycle-state/reset with the only
// mode the server accepts, full-rescan. It clears cycle recovery bookkeeping and
// makes the next cycle scan the full namespace; it does not discard usage
// state.
func (c *RustfsAdmin) ResetScannerCycleState(ctx context.Context) (ScannerCycleStateResetResult, error) {
	content, err := json.Marshal(map[string]string{"mode": "full-rescan"})
	if err != nil {
		return ScannerCycleStateResetResult{}, err
	}

	reqData := RequestData{
		Method:  http.MethodPost,
		RelPath: "scanner/cycle-state/reset",
		Content: content,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ScannerCycleStateResetResult{}, c.featureError(ctx, err, FeatureScannerCycleStateReset)
	}
	defer drainClose(resp)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ScannerCycleStateResetResult{}, err
	}

	result := ScannerCycleStateResetResult{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ScannerCycleStateResetResult{}, fmt.Errorf("decode scanner cycle-state reset: %w", err)
	}
	result.RawJSON = string(raw)
	return result, nil
}

// ResetScannerUsageState runs the synchronous
// POST /v3/scanner/usage-state/reset with mode full-rebuild.
//
// The reset discards the durable usage baseline and publishes a
// bootstrap-pending marker; the next complete scanner cycle rebuilds usage.
// Quotas may briefly misreport while that happens. A running scanner leader
// can reject the synchronous reset with a busy-lock error; callers detect that
// with IsScannerLeaderLockBusy and retry, rather than using the server's async
// recovery-intent form.
func (c *RustfsAdmin) ResetScannerUsageState(ctx context.Context) (ScannerUsageStateResetResult, error) {
	content, err := json.Marshal(map[string]string{"mode": "full-rebuild"})
	if err != nil {
		return ScannerUsageStateResetResult{}, err
	}

	reqData := RequestData{
		Method:  http.MethodPost,
		RelPath: "scanner/usage-state/reset",
		Content: content,
	}

	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return ScannerUsageStateResetResult{}, c.featureError(ctx, err, FeatureScannerUsageStateReset)
	}
	defer drainClose(resp)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ScannerUsageStateResetResult{}, err
	}

	result := ScannerUsageStateResetResult{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ScannerUsageStateResetResult{}, fmt.Errorf("decode scanner usage-state reset: %w", err)
	}
	result.RawJSON = string(raw)
	return result, nil
}

// IsScannerLeaderLockBusy reports whether the scanner refused a reset because
// its leader still holds the state lock. The admin API has no structured code
// for this condition, so detection matches the server's exact wording.
func IsScannerLeaderLockBusy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "scanner leader lock is busy")
}

func init() {
	registerFeature(FeatureScannerStatus, featureSpec{
		route: "GET /rustfs/admin/v3/scanner/status",
		// The route is registered and implemented in the tagged RustFS source
		// for 1.0.1 through 1.0.3, so there is no version floor to advertise.
		// A build that does not implement it produces a capability error
		// naming the exact server version, and the acceptance precheck skips.
	})
	registerFeature(FeatureScannerCycleStateReset, featureSpec{
		route: "POST /rustfs/admin/v3/scanner/cycle-state/reset",
	})
	registerFeature(FeatureScannerUsageStateReset, featureSpec{
		route: "POST /rustfs/admin/v3/scanner/usage-state/reset",
	})
}
