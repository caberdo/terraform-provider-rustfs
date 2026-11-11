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

	// UsagePublication and HealObservation are the observation blocks RustFS
	// 1.0.5 added to the status payload. Both stay nil on 1.0.1 through 1.0.4,
	// which do not send them.
	UsagePublication *ScannerUsagePublication
	HealObservation  *ScannerHealObservation
}

// ScannerUsagePublication is the metrics.usage_publication block: one local
// usage-snapshot publication attempt, with its own scanner epoch and cycle even
// after metrics.current_cycle returns to zero.
//
// class and outcome stay strings because the server defines and extends them
// (`progress_only`, `observational`, `complete_pending`, `complete_converged`
// and `unknown` are the classes RustFS 1.0.5 documents). Fields the attempt
// could not confirm are nil, not zero: pending_bucket_count comes from a
// confirmed authoritative snapshot and is only zero for a confirmed converged
// one.
type ScannerUsagePublication struct {
	ScannerEpoch              *uint64
	Cycle                     *uint64
	Class                     string
	Outcome                   string
	PendingBucketCount        *uint64
	SnapshotUpdatedAtUnixSecs *uint64
	ObservedAtUnixSecs        *uint64
}

// ScannerHealTerminalCandidate is one retained terminal heal observation group.
type ScannerHealTerminalCandidate struct {
	Execution   string `json:"execution"`
	Disposition string `json:"disposition"`
	Scope       string `json:"scope"`
	ScanMode    string `json:"scan_mode"`
	Count       uint64 `json:"count"`
}

// ScannerHealUnknownCandidate is one retained unknown heal observation group.
type ScannerHealUnknownCandidate struct {
	Reason string `json:"reason"`
	Count  uint64 `json:"count"`
}

// ScannerHealObservation is the top-level heal_observation block: this node's
// bounded heal observations. It is observation only; nothing here starts,
// pauses or cancels a heal.
//
// The server groups candidates by their execution/disposition or unknown
// reason, so the two counts are the sum of the groups' count fields. They cover
// the local observation window, not a lifetime total and not a cluster
// backlog.
type ScannerHealObservation struct {
	RetainedPendingTasks   *uint64
	IntegrityState         string
	TerminalCandidates     []ScannerHealTerminalCandidate
	UnknownCandidates      []ScannerHealUnknownCandidate
	TerminalCandidateCount uint64
	UnknownCandidateCount  uint64
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

// scannerUsagePublicationBlock is the wire shape of metrics.usage_publication.
// Unknown keys are ignored, so a server that adds a field still decodes.
type scannerUsagePublicationBlock struct {
	ScannerEpoch              *uint64 `json:"scanner_epoch"`
	Cycle                     *uint64 `json:"cycle"`
	Class                     string  `json:"class"`
	Outcome                   string  `json:"outcome"`
	PendingBucketCount        *uint64 `json:"pending_bucket_count"`
	SnapshotUpdatedAtUnixSecs *uint64 `json:"snapshot_updated_at_unix_secs"`
	ObservedAtUnixSecs        *uint64 `json:"observed_at_unix_secs"`
}

// scannerHealObservationBlock is the wire shape of heal_observation. Unknown
// keys, including a future candidate field, are ignored.
type scannerHealObservationBlock struct {
	RetainedPendingTasks *uint64 `json:"retained_pending_tasks"`
	TerminalCandidates   []struct {
		Execution   string `json:"execution"`
		Disposition string `json:"disposition"`
		Scope       string `json:"scope"`
		ScanMode    string `json:"scan_mode"`
		Count       uint64 `json:"count"`
	} `json:"terminal_candidates"`
	UnknownCandidates []struct {
		Reason string `json:"reason"`
		Count  uint64 `json:"count"`
	} `json:"unknown_candidates"`
	IntegrityState string `json:"integrity_state"`
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

		// UsagePublication is a pointer so a 1.0.1 through 1.0.4 payload, which
		// omits the block, decodes to nil instead of a zero attempt.
		UsagePublication *scannerUsagePublicationBlock `json:"usage_publication"`
	} `json:"metrics"`
	// HealObservation is a pointer for the same reason: pre-1.0.5 servers omit
	// the block.
	HealObservation *scannerHealObservationBlock `json:"heal_observation"`
	CycleSchedule   struct {
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

	status := ScannerStatus{
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
	}

	if block := envelope.Metrics.UsagePublication; block != nil {
		status.UsagePublication = &ScannerUsagePublication{
			ScannerEpoch:              block.ScannerEpoch,
			Cycle:                     block.Cycle,
			Class:                     block.Class,
			Outcome:                   block.Outcome,
			PendingBucketCount:        block.PendingBucketCount,
			SnapshotUpdatedAtUnixSecs: block.SnapshotUpdatedAtUnixSecs,
			ObservedAtUnixSecs:        block.ObservedAtUnixSecs,
		}
	}

	if block := envelope.HealObservation; block != nil {
		observation := &ScannerHealObservation{
			RetainedPendingTasks: block.RetainedPendingTasks,
			IntegrityState:       block.IntegrityState,
			TerminalCandidates:   make([]ScannerHealTerminalCandidate, 0, len(block.TerminalCandidates)),
			UnknownCandidates:    make([]ScannerHealUnknownCandidate, 0, len(block.UnknownCandidates)),
		}
		for _, candidate := range block.TerminalCandidates {
			observation.TerminalCandidates = append(observation.TerminalCandidates, ScannerHealTerminalCandidate{
				Execution:   candidate.Execution,
				Disposition: candidate.Disposition,
				Scope:       candidate.Scope,
				ScanMode:    candidate.ScanMode,
				Count:       candidate.Count,
			})
			observation.TerminalCandidateCount += candidate.Count
		}
		for _, candidate := range block.UnknownCandidates {
			observation.UnknownCandidates = append(observation.UnknownCandidates, ScannerHealUnknownCandidate{
				Reason: candidate.Reason,
				Count:  candidate.Count,
			})
			observation.UnknownCandidateCount += candidate.Count
		}
		status.HealObservation = observation
	}

	return status, nil
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
