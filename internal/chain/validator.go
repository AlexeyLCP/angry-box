package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	xraycfg "github.com/alexeylcp/lucx-core/internal/backend/xray/config"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

type SSHFactory func(serverID string) (*ssh.Client, error)

func Validate(ctx context.Context, plan *Plan, sshFactory SSHFactory) error {
	for _, batch := range plan.Batches {
		client, err := sshFactory(batch.ServerID)
		if err != nil {
			return fmt.Errorf("server %s SSH: %w", batch.ServerID, err)
		}

		// Basic connectivity
		if _, err := client.Exec("echo ok"); err != nil {
			client.Close()
			return fmt.Errorf("server %s unreachable: %w", batch.ServerID, err)
		}

		// Backend status
		status, err := batch.Backend.Status(ctx, client)
		if err != nil {
			client.Close()
			return fmt.Errorf("server %s status: %w", batch.ServerID, err)
		}
		if !status.Running {
			client.Close()
			return fmt.Errorf("server %s: backend not running", batch.ServerID)
		}

		// Port conflict check: read current config and verify no non-lucx
		// inbounds use the ports we're about to claim.
		if err := checkPortConflicts(client, batch); err != nil {
			client.Close()
			return fmt.Errorf("server %s: %w", batch.ServerID, err)
		}

		client.Close()
	}
	return nil
}

// checkPortConflicts reads the current config and ensures the ports we plan
// to use are either free or already owned by LucX-managed inbounds.
func checkPortConflicts(client *ssh.Client, batch ServerBatch) error {
	// Collect all ports from new inbounds
	newPorts := make(map[int]string) // port -> tag
	for _, raw := range batch.Inbounds {
		var item struct {
			Port int    `json:"port"`
			Tag  string `json:"tag"`
		}
		if err := json.Unmarshal(raw, &item); err != nil || item.Port == 0 {
			continue
		}
		newPorts[item.Port] = item.Tag
	}
	if len(newPorts) == 0 {
		return nil
	}

	// Read current config
	content, err := client.ReadFile("/usr/local/etc/xray/config.json")
	if err != nil {
		// No config yet — all ports are free
		return nil
	}

	var current struct {
		Inbounds []struct {
			Port int    `json:"port"`
			Tag  string `json:"tag"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(content), &current); err != nil {
		return nil // Can't parse — proceed with caution
	}

	for _, existing := range current.Inbounds {
		if _, claimed := newPorts[existing.Port]; claimed {
			// LucX-managed inbounds are OK — we replace them during merge
			if strings.HasPrefix(existing.Tag, xraycfg.LucXTagPattern) {
				continue
			}
			return fmt.Errorf(
				"port %d is already in use by non-LucX inbound %q — remove it first or use a different port",
				existing.Port, existing.Tag,
			)
		}
	}

	return nil
}
