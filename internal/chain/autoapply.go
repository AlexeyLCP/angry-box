package chain

// autoapply.go — background auto-apply (hybrid deploy mode), ported from
// VPN/orchestrator/app/services/auto_apply.py. Per-node mutex serializes
// deploys to the same host; schedule fires-and-forgets a goroutine that calls
// ApplyMergedNode. Failures are logged + audited, NOT surfaced to the HTTP
// caller (the pending change shows up on the Deploy Status page instead).
//
// The trigger gate is the per-resource auto_apply_on set (as in Python), NOT
// Profile.AutoApply (which is an intent/display flag). NodeInfo.AutoApply is the
// node-level master switch.
//
// Concurrency is bounded by a counting semaphore (autoApplyMaxConcurrent): a
// fleet with N pending nodes spawns at most autoApplyMaxConcurrent concurrent
// SSH deploys, not N. Without this cap, a 100-node fleet with all-pending
// changes would open 100 SSH connections at once (CTO-review resource budget §9
// "no concurrency cap on autoapply"). Each per-host deploy is still serialized
// by withHostLock inside ApplyMergedNode — the semaphore bounds the global
// fan-out, the host lock bounds per-host re-entrancy.

import (
	"context"
	"log/slog"
	"sync"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// autoApplyMaxConcurrent bounds the number of background SSH deploys that may
// run at the same time. 8 is a safe default for an orchestrator on a modest
// VPS (each SSH session is ~1-5MB; 8 sessions ≈ 8-40MB transient). Override via
// SetAutoApplyConcurrency before InitAutoApply if a larger fleet justifies it.
const autoApplyMaxConcurrent = 8

// autoApplyContext bundles the immutable deps a background deploy needs.
type autoApplyContext struct {
	factory   ports.Factory
	connector ports.SSHConnector
	storePath string
	// deploySem is a counting semaphore (buffered channel) that bounds the
	// number of concurrent background deploys. Allocated once at InitAutoApply.
	deploySem chan struct{}
}

var (
	autoApplyCtx    autoApplyContext
	backgroundTasks sync.WaitGroup
)

// InitAutoApply wires the factory + store path used by background deploys. Call
// once at startup (serveCmd). If connector is nil, the production SSH connector
// is used; tests inject a fake. If SetAutoApplyConcurrency was called first, the
// pre-sized semaphore is reused; otherwise a default-capacity one is allocated.
func InitAutoApply(f ports.Factory, connector ports.SSHConnector, storePath string) {
	sem := autoApplyCtx.deploySem
	if sem == nil {
		sem = make(chan struct{}, autoApplyMaxConcurrent)
	}
	autoApplyCtx = autoApplyContext{
		factory:   f,
		connector: connector,
		storePath: storePath,
		deploySem: sem,
	}
}

// SetAutoApplyConcurrency overrides the max-concurrent-background-deploys cap.
// Must be called BEFORE InitAutoApply (which allocates the semaphore) to take
// effect; calling after InitAutoApply is a no-op (the semaphore is already
// sized). Values <= 0 fall back to the default autoApplyMaxConcurrent. Exposed
// for tests/operators with a known fleet size (CTO-review §9).
func SetAutoApplyConcurrency(n int) {
	if n <= 0 || autoApplyCtx.deploySem != nil {
		// Already initialized — resizing a live channel is unsafe; ignore.
		return
	}
	// Stash the override so InitAutoApply (called next) sizes the semaphore to
	// n. We do this by pre-allocating here and letting InitAutoApply reuse it.
	autoApplyCtx.deploySem = make(chan struct{}, n)
}

// ScheduleAutoApply fires-and-forgets a background SSH deploy to nodeID. It
// returns immediately; failures are logged + audited. No-op if autoApplyCtx is
// unset (InitAutoApply not called).
//
// Serialization is handled INSIDE ApplyMergedNode via withHostLock, so this
// background path and the explicit apply paths (CLI, web) share the same
// per-host mutex and cannot interleave their SSH backup->write->restart
// sequences on the same node (CTO-review C2).
//
// The global concurrency cap (autoApplyMaxConcurrent) is enforced by acquiring
// a slot on autoApplyCtx.deploySem before the SSH deploy starts; the slot is
// released on completion so a backlog of pending nodes queues instead of
// fanning out to N simultaneous SSH connections.
func ScheduleAutoApply(nodeID, reason string) {
	if autoApplyCtx.factory == nil || nodeID == "" {
		return
	}
	backgroundTasks.Add(1)
	go func() {
		defer backgroundTasks.Done()
		// Acquire a concurrency slot. This blocks the goroutine (NOT the
		// scheduler) until a slot frees up — the per-host lock is acquired
		// AFTER the slot, so the semaphore is the outer bound.
		sem := autoApplyCtx.deploySem
		if sem != nil {
			sem <- struct{}{}
			defer func() { <-sem }()
		}
		runAutoDeploy(nodeID, reason)
	}()
}

// runAutoDeploy loads the node, renders its merged config, and pushes it.
func runAutoDeploy(nodeID, reason string) {
	st := NewStore(autoApplyCtx.storePath)
	info, err := st.GetNodeInfo(nodeID)
	if err != nil {
		slog.Warn("auto-apply: node not found",
			"node", nodeID, "err", err)
		return
	}
	if !info.AutoApply {
		return // node-level switch off — skip
	}
	slog.Info("auto-apply: starting background deploy",
		"node", nodeID, "reason", reason)
	applier := NewApplier(autoApplyCtx.factory, autoApplyCtx.connector)
	_, _, err = applier.ApplyMergedNode(context.Background(), st, info)
	if err != nil {
		slog.Warn("auto-apply: deploy failed",
			"node", nodeID, "reason", reason, "err", err)
		WriteAudit(st, "deploy", "node", nodeID, AuditPayload{"mode": "auto", "reason": reason, "error": err.Error()}, "operator")
		return
	}
	slog.Info("auto-apply: deploy ok",
		"node", nodeID, "reason", reason)
}

// WaitAutoApply blocks until all in-flight background deploys finish (for
// graceful shutdown / tests).
func WaitAutoApply() {
	backgroundTasks.Wait()
}

// AutoApplyMaxConcurrent returns the configured max-concurrent-background-deploys
// cap (the semaphore capacity). Exposed for tests/docs.
func AutoApplyMaxConcurrent() int {
	if autoApplyCtx.deploySem == nil {
		return 0
	}
	return cap(autoApplyCtx.deploySem)
}