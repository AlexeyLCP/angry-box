package chain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

type ExecutedBatch struct {
	ServerID   string
	BackupPath string
}

// Execute applies each server batch via ApplyConfig: one write + one restart per server.
func Execute(ctx context.Context, plan *Plan, sshFactory SSHFactory) ([]ExecutedBatch, error) {
	var executed []ExecutedBatch

	type configApplier interface {
		ApplyConfig(context.Context, *ssh.Client, []json.RawMessage, []json.RawMessage, []backend.RoutingRule) error
	}

	for _, batch := range plan.Batches {
		client, err := sshFactory(batch.ServerID)
		if err != nil {
			rollbackBatches(ctx, executed, sshFactory)
			return nil, fmt.Errorf("server %s ssh: %w", batch.ServerID, err)
		}

		applier, ok := batch.Backend.(configApplier)
		if !ok {
			client.Close()
			rollbackBatches(ctx, executed, sshFactory)
			return nil, fmt.Errorf("server %s: backend does not support batch ApplyConfig", batch.ServerID)
		}

		if err := applier.ApplyConfig(ctx, client, batch.Inbounds, batch.Outbounds, batch.Routing); err != nil {
			client.Close()
			rollbackBatches(ctx, executed, sshFactory)
			return nil, fmt.Errorf("apply config on %s: %w", batch.ServerID, err)
		}

		client.Close()
		executed = append(executed, ExecutedBatch{ServerID: batch.ServerID})
	}

	return executed, nil
}
