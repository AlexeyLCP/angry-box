package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

// ExecutedBatch records the result of applying a batch to one server.
type ExecutedBatch struct {
	ServerID   string
	BackupPath string
}

// configApplier is the interface backends must implement for batch ApplyConfig.
// Returns backupPath for rollback.
type configApplier interface {
	ApplyConfig(context.Context, *ssh.Client, []json.RawMessage, []json.RawMessage, []backend.RoutingRule) (string, error)
}

// Execute applies each server batch via ApplyConfig: one write + one restart per server.
// On failure, rolls back all previously applied batches using their backup paths.
func Execute(ctx context.Context, plan *Plan, sshFactory SSHFactory) ([]ExecutedBatch, error) {
	var executed []ExecutedBatch

	for i, batch := range plan.Batches {
		client, err := sshFactory(batch.ServerID)
		if err != nil {
			rollbackExecuted(ctx, executed, sshFactory)
			return nil, fmt.Errorf("server %s ssh: %w", batch.ServerID, err)
		}

		applier, ok := batch.Backend.(configApplier)
		if !ok {
			client.Close()
			rollbackExecuted(ctx, executed, sshFactory)
			return nil, fmt.Errorf("server %s: backend does not support batch ApplyConfig", batch.ServerID)
		}

		log.Printf("[EXECUTOR] applying batch %d/%d to server %s (in=%d out=%d rules=%d)",
			i+1, len(plan.Batches), batch.ServerID,
			len(batch.Inbounds), len(batch.Outbounds), len(batch.Routing))

		backupPath, err := applier.ApplyConfig(ctx, client, batch.Inbounds, batch.Outbounds, batch.Routing)
		client.Close()
		if err != nil {
			log.Printf("[EXECUTOR] FAILED on server %s: %v", batch.ServerID, err)
			rollbackExecuted(ctx, executed, sshFactory)
			return nil, fmt.Errorf("apply config on %s: %w", batch.ServerID, err)
		}

		log.Printf("[EXECUTOR] batch %d/%d OK — backup: %s", i+1, len(plan.Batches), backupPath)
		executed = append(executed, ExecutedBatch{ServerID: batch.ServerID, BackupPath: backupPath})
	}

	return executed, nil
}
