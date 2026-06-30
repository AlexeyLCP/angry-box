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
	"log"
	"sync"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// autoApplyContext bundles the immutable deps a background deploy needs.
type autoApplyContext struct {
	factory   ports.Factory
	storePath string
}

var (
	autoApplyCtx    autoApplyContext
	hostLocksMu     sync.Mutex
	hostLocks       = map[string]*sync.Mutex{}
	backgroundTasks sync.WaitGroup
)

// InitAutoApply wires the factory + store path used by background deploys. Call
// once at startup (serveCmd).
func InitAutoApply(f ports.Factory, storePath string) {
	autoApplyCtx = autoApplyContext{factory: f, storePath: storePath}
}

// hostLock returns the per-host mutex, lazily created.
func hostLock(nodeID string) *sync.Mutex {
	hostLocksMu.Lock()
	defer hostLocksMu.Unlock()
	mu, ok := hostLocks[nodeID]
	if !ok {
		mu = &sync.Mutex{}
		hostLocks[nodeID] = mu
	}
	return mu
}

// ScheduleAutoApply fires-and-forgets a background SSH deploy to nodeID. It
// returns immediately; failures are logged + audited. No-op if autoApplyCtx is
// unset (InitAutoApply not called).
func ScheduleAutoApply(nodeID, reason string) {
	if autoApplyCtx.factory == nil || nodeID == "" {
		return
	}
	backgroundTasks.Add(1)
	go func() {
		defer backgroundTasks.Done()
		mu := hostLock(nodeID)
		mu.Lock()
		defer mu.Unlock()
		runAutoDeploy(nodeID, reason)
	}()
}

// runAutoDeploy loads the node, renders its merged config, and pushes it.
func runAutoDeploy(nodeID, reason string) {
	st := NewStore(autoApplyCtx.storePath)
	info, err := st.GetNodeInfo(nodeID)
	if err != nil {
		log.Printf("auto-apply: node %s not found: %v", nodeID, err)
		return
	}
	if !info.AutoApply {
		return // node-level switch off — skip
	}
	applier := NewApplier(autoApplyCtx.factory)
	_, _, err = applier.ApplyMergedNode(context.Background(), st, info)
	if err != nil {
		log.Printf("auto-apply: deploy %s (%s) failed: %v", nodeID, reason, err)
		WriteAudit(st, "deploy", "node", nodeID, AuditPayload{"mode": "auto", "reason": reason, "error": err.Error()}, "operator")
		return
	}
	log.Printf("auto-apply: deploy %s (%s) ok", nodeID, reason)
}

// WaitAutoApply blocks until all in-flight background deploys finish (for
// graceful shutdown / tests).
func WaitAutoApply() {
	backgroundTasks.Wait()
}