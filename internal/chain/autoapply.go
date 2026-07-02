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

import (
	"context"
	"log/slog"
	"sync"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// autoApplyContext bundles the immutable deps a background deploy needs.
type autoApplyContext struct {
	factory   ports.Factory
	connector ports.SSHConnector
	storePath string
}

var (
	autoApplyCtx    autoApplyContext
	backgroundTasks sync.WaitGroup
)

// InitAutoApply wires the factory + store path used by background deploys. Call
// once at startup (serveCmd). If connector is nil, the production SSH connector
// is used; tests inject a fake.
func InitAutoApply(f ports.Factory, connector ports.SSHConnector, storePath string) {
	autoApplyCtx = autoApplyContext{factory: f, connector: connector, storePath: storePath}
}

// ScheduleAutoApply fires-and-forgets a background SSH deploy to nodeID. It
// returns immediately; failures are logged + audited. No-op if autoApplyCtx is
// unset (InitAutoApply not called).
//
// Serialization is handled INSIDE ApplyMergedNode via withHostLock, so this
// background path and the explicit apply paths (CLI, web) share the same
// per-host mutex and cannot interleave their SSH backup->write->restart
// sequences on the same node (CTO-review C2).
func ScheduleAutoApply(nodeID, reason string) {
	if autoApplyCtx.factory == nil || nodeID == "" {
		return
	}
	backgroundTasks.Add(1)
	go func() {
		defer backgroundTasks.Done()
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