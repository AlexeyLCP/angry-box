package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
	"github.com/alexeylcp/lucx-core/internal/ws"
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

// ProgressFunc is called for each progress event during execution.
type ProgressFunc func(entry ws.LogEntry)

// Execute applies each server batch via ApplyConfig: one write + one restart per server.
// On failure, rolls back all previously applied batches using their backup paths.
func Execute(ctx context.Context, plan *Plan, sshFactory SSHFactory, progress ProgressFunc) ([]ExecutedBatch, error) {
	var executed []ExecutedBatch

	sendProgress := func(step, server, detail, status, errStr string) {
		if progress != nil {
			progress(ws.LogEntry{Step: step, Server: server, Detail: detail, Status: status, Error: errStr})
		}
	}

	for i, batch := range plan.Batches {
		sendProgress("ssh", batch.ServerID, "connecting", "", "")

		client, err := sshFactory(batch.ServerID)
		if err != nil {
			sendProgress("ssh", batch.ServerID, "", "error", err.Error())
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
		sendProgress("apply", batch.ServerID, fmt.Sprintf("batch %d/%d: %d inbounds, %d outbounds", i+1, len(plan.Batches), len(batch.Inbounds), len(batch.Outbounds)), "", "")

		backupPath, err := applier.ApplyConfig(ctx, client, batch.Inbounds, batch.Outbounds, batch.Routing)
		client.Close()
		if err != nil {
			log.Printf("[EXECUTOR] FAILED on server %s: %v", batch.ServerID, err)
			sendProgress("apply", batch.ServerID, "", "error", err.Error())
			rollbackExecuted(ctx, executed, sshFactory)
			return nil, fmt.Errorf("apply config on %s: %w", batch.ServerID, err)
		}

		log.Printf("[EXECUTOR] batch %d/%d OK — backup: %s", i+1, len(plan.Batches), backupPath)
		sendProgress("apply", batch.ServerID, "config applied, xray restarted", "ok", "")
		executed = append(executed, ExecutedBatch{ServerID: batch.ServerID, BackupPath: backupPath})
	}

	return executed, nil
}
