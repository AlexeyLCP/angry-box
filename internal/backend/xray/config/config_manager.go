package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

// Manager orchestrates config.json operations for a single Xray server.
// Flow: Read → Backup → Merge → Atomic Write → Test → Restart → Verify.
type Manager struct {
	client *ssh.Client
}

// NewManager creates a config manager for the given SSH-connected server.
func NewManager(client *ssh.Client) *Manager {
	return &Manager{client: client}
}

// Read loads the current config.json from the server.
func (m *Manager) Read(ctx context.Context) (*backend.RawConfig, error) {
	content, err := m.client.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var raw backend.RawConfig
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &raw, nil
}

// Apply is the PRIMARY method. It safely applies LucX changes to the server's config.
//
// Flow:
// 1. Backup current config
// 2. Read current config
// 3. Merge: keep non-LucX entries, replace LucX entries
// 4. Atomic write (tmp → rename)
// 5. Test config validity (xray run -test)
// 6. Restart Xray
// 7. Verify ports are listening
//
// Returns the backup path for rollback.
func (m *Manager) Apply(
	ctx context.Context,
	tm *TagManager,
	inbounds, outbounds []json.RawMessage,
	rules []backend.RoutingRule,
	verifyPorts []int,
) (backupPath string, err error) {

	// 1. Backup
	backupPath, err = Backup(ctx, m.client)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}

	// 2. Read current
	cfg, err := m.Read(ctx)
	if err != nil {
		Restore(ctx, m.client, backupPath)
		return "", fmt.Errorf("read: %w", err)
	}

	// 3. Merge
	merged := Merge(cfg, inbounds, outbounds, rules)

	// 4. Atomic write
	if err := atomicWrite(ctx, m.client, merged); err != nil {
		Restore(ctx, m.client, backupPath)
		return backupPath, fmt.Errorf("write: %w", err)
	}

	// 5. Validate config
	if err := TestConfig(ctx, m.client); err != nil {
		Restore(ctx, m.client, backupPath)
		return backupPath, fmt.Errorf("test: %w", err)
	}

	// 6. Restart Xray
	if err := restartXray(ctx, m.client); err != nil {
		Restore(ctx, m.client, backupPath)
		return backupPath, fmt.Errorf("restart: %w", err)
	}

	// 7. Verify ports
	for _, port := range verifyPorts {
		if err := VerifyPort(ctx, m.client, port); err != nil {
			Restore(ctx, m.client, backupPath)
			return backupPath, fmt.Errorf("verify port %d: %w", port, err)
		}
	}

	return backupPath, nil
}

// Rollback restores a backup and restarts Xray.
func (m *Manager) Rollback(ctx context.Context, backupPath string) error {
	if err := Restore(ctx, m.client, backupPath); err != nil {
		return err
	}
	return restartXray(ctx, m.client)
}

// ClearLucX removes ALL LucX-managed entries from the config.
func (m *Manager) ClearLucX(ctx context.Context, tm *TagManager) error {
	_, err := m.Apply(ctx, tm, nil, nil, nil, nil)
	return err
}

// atomicWrite writes merged config to tmp file then renames.
func atomicWrite(ctx context.Context, client *ssh.Client, merged *MergeResult) error {
	cfg := map[string]interface{}{
		"inbounds":  merged.Inbounds,
		"outbounds": merged.Outbounds,
		"routing":   json.RawMessage(merged.Routing),
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := configPath + ".tmp"
	if err := client.WriteFile(tmpPath, string(data)); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if _, err := client.Exec(fmt.Sprintf("mv %s %s", tmpPath, configPath)); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	return nil
}

// restartXray restarts Xray and waits for it to come back up.
func restartXray(ctx context.Context, client *ssh.Client) error {
	client.Exec("systemctl restart xray 2>/dev/null || /etc/init.d/xray restart 2>/dev/null")
	time.Sleep(2 * time.Second)

	out, _ := client.Exec("systemctl is-active xray 2>/dev/null || echo unknown")
	out = strings.TrimSpace(out)
	if out != "active" {
		client.Exec("/etc/init.d/xray restart 2>/dev/null")
		time.Sleep(2 * time.Second)
	}
	return nil
}
