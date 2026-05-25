package chain

import (
	"context"
	"fmt"

	"github.com/alexeylcp/lucx-core/internal/store"
	"github.com/alexeylcp/lucx-core/internal/ws"
)

type Engine struct {
	store      *store.Store
	sshFactory SSHFactory
	wsHub      *ws.Hub
}

func NewEngine(s *store.Store, sshFactory SSHFactory) *Engine {
	return &Engine{store: s, sshFactory: sshFactory}
}

func NewEngineWithHub(s *store.Store, sshFactory SSHFactory, hub *ws.Hub) *Engine {
	return &Engine{store: s, sshFactory: sshFactory, wsHub: hub}
}

// Apply deploys a chain. If hub is set, progress is broadcast.
func (e *Engine) Apply(ctx context.Context, chain *store.Chain) error {
	chainID := chain.ID
	broadcast := func(entry ws.LogEntry) {
		if e.wsHub != nil {
			e.wsHub.Broadcast(chainID, entry)
		}
	}

	broadcast(ws.LogEntry{Step: "plan", Status: "started", Detail: "building server batches"})

	plan, err := BuildPlan(chain, e.store.GetServer)
	if err != nil {
		broadcast(ws.LogEntry{Step: "plan", Status: "error", Error: err.Error()})
		return fmt.Errorf("plan: %w", err)
	}
	broadcast(ws.LogEntry{Step: "plan", Status: "ok", Detail: fmt.Sprintf("%d server batches", len(plan.Batches))})

	broadcast(ws.LogEntry{Step: "validate", Status: "started", Detail: "checking server connectivity"})
	if err := Validate(ctx, plan, e.sshFactory); err != nil {
		broadcast(ws.LogEntry{Step: "validate", Status: "error", Error: err.Error()})
		return fmt.Errorf("validate: %w", err)
	}
	broadcast(ws.LogEntry{Step: "validate", Status: "ok", Detail: "all servers reachable"})

	broadcast(ws.LogEntry{Step: "execute", Status: "started", Detail: fmt.Sprintf("applying to %d servers", len(plan.Batches))})
	executed, err := Execute(ctx, plan, e.sshFactory, func(entry ws.LogEntry) {
		broadcast(entry)
	})
	if err != nil {
		e.store.UpdateChainStatus(chain.ID, "draft")
		broadcast(ws.LogEntry{Step: "execute", Status: "error", Error: err.Error()})
		return fmt.Errorf("execute: %w", err)
	}
	_ = executed
	broadcast(ws.LogEntry{Step: "execute", Status: "ok", Detail: "all batches applied"})

	if err := e.store.UpdateChainStatus(chain.ID, "active"); err != nil {
		broadcast(ws.LogEntry{Step: "commit", Status: "error", Error: err.Error()})
		return fmt.Errorf("commit: %w", err)
	}
	broadcast(ws.LogEntry{Step: "commit", Status: "ok", Detail: "chain active"})
	broadcast(ws.LogEntry{Step: "complete", Status: "active"})

	return nil
}
