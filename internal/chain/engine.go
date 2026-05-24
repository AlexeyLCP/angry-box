package chain

import (
	"context"
	"fmt"

	"github.com/alexeylcp/lucx-core/internal/store"
)

type Engine struct {
	store      *store.Store
	sshFactory SSHFactory
}

func NewEngine(s *store.Store, sshFactory SSHFactory) *Engine {
	return &Engine{store: s, sshFactory: sshFactory}
}

func (e *Engine) Apply(ctx context.Context, chain *store.Chain) error {
	// 1. Build per-server batched plan
	plan, err := BuildPlan(chain)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	// 2. Pre-flight validation
	if err := Validate(ctx, plan, e.sshFactory); err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	// 3. Execute — one ApplyConfig per server (backup→write→restart→verify)
	executed, err := Execute(ctx, plan, e.sshFactory)
	if err != nil {
		e.store.UpdateChainStatus(chain.ID, "draft")
		return fmt.Errorf("execute: %w", err)
	}
	_ = executed // backup paths stored for future rollback

	// 4. Commit — chain is active
	if err := e.store.UpdateChainStatus(chain.ID, "active"); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
