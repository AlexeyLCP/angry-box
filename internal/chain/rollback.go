package chain

import (
	"context"
	"log"

	xraycfg "github.com/alexeylcp/lucx-core/internal/backend/xray/config"
)

// rollbackExecuted restores config.json from backup on each server that was modified.
// Called when a batch fails after previous batches succeeded.
func rollbackExecuted(ctx context.Context, executed []ExecutedBatch, sshFactory SSHFactory) {
	if len(executed) == 0 {
		return
	}
	log.Printf("[ROLLBACK] restoring %d server(s) from backup", len(executed))
	for i := len(executed) - 1; i >= 0; i-- {
		eb := executed[i]
		log.Printf("[ROLLBACK]   server %s <- %s", eb.ServerID, eb.BackupPath)
		client, err := sshFactory(eb.ServerID)
		if err != nil {
			log.Printf("[ROLLBACK]   ERROR: cannot connect to %s: %v", eb.ServerID, err)
			continue
		}
		mgr := xraycfg.NewManager(client)
		if err := mgr.Rollback(ctx, eb.BackupPath); err != nil {
			log.Printf("[ROLLBACK]   ERROR: restore failed on %s: %v", eb.ServerID, err)
		} else {
			log.Printf("[ROLLBACK]   server %s restored OK", eb.ServerID)
		}
		client.Close()
	}
}
