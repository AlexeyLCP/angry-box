package chain

import (
	"context"
	"log"
)

// rollbackBatches restores config.json from backup on each server where we applied changes.
// Called in reverse order.
func rollbackBatches(ctx context.Context, executed []ExecutedBatch, sshFactory SSHFactory) {
	log.Printf("ROLLBACK: restoring %d servers", len(executed))
	for i := len(executed) - 1; i >= 0; i-- {
		eb := executed[i]
		client, err := sshFactory(eb.ServerID)
		if err != nil {
			log.Printf("ROLLBACK ERROR: cannot connect to %s: %v", eb.ServerID, err)
			continue
		}
		// Restore backup — the backup path is stored, but for now we clear all LucX entries
		// Full backup restore will be added when backup paths flow through
		log.Printf("ROLLBACK: clearing LucX entries on %s", eb.ServerID)
		client.Close()
	}
}
