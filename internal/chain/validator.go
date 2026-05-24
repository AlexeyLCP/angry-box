package chain

import (
	"context"
	"fmt"

	"github.com/alexeylcp/lucx-core/internal/ssh"
)

type SSHFactory func(serverID string) (*ssh.Client, error)

func Validate(ctx context.Context, plan *Plan, sshFactory SSHFactory) error {
	for _, batch := range plan.Batches {
		client, err := sshFactory(batch.ServerID)
		if err != nil {
			return fmt.Errorf("server %s SSH: %w", batch.ServerID, err)
		}
		if _, err := client.Exec("echo ok"); err != nil {
			client.Close()
			return fmt.Errorf("server %s unreachable: %w", batch.ServerID, err)
		}
		status, err := batch.Backend.Status(ctx, client)
		client.Close()
		if err != nil {
			return fmt.Errorf("server %s status: %w", batch.ServerID, err)
		}
		if !status.Running {
			return fmt.Errorf("server %s: backend not running", batch.ServerID)
		}
	}
	return nil
}
