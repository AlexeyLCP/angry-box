package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

// Manager orchestrates config.json operations for a single Xray server.
// Flow: Read → Backup → Merge → Atomic Write → Test → Restart → Verify.
type Manager struct {
	client *ssh.Client
	log    *log.Logger
}

// NewManager creates a config manager for the given SSH-connected server.
func NewManager(client *ssh.Client) *Manager {
	return &Manager{
		client: client,
		log:    log.Default(),
	}
}

// NewManagerWithLogger creates a config manager with a custom logger.
func NewManagerWithLogger(client *ssh.Client, logger *log.Logger) *Manager {
	return &Manager{client: client, log: logger}
}

// host returns a short identifier for log messages.
func (m *Manager) host() string { return m.client.Host() }

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
	h := m.host()

	// 1. Backup
	m.log.Printf("[%s] STEP 1/7: backing up config.json", h)
	backupPath, err = Backup(ctx, m.client)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	m.log.Printf("[%s]   backup saved: %s", h, backupPath)

	// 2. Read current
	m.log.Printf("[%s] STEP 2/7: reading current config", h)
	cfg, err := m.Read(ctx)
	if err != nil {
		Restore(ctx, m.client, backupPath)
		return "", fmt.Errorf("read: %w", err)
	}
	m.log.Printf("[%s]   current inbounds=%d outbounds=%d", h, len(cfg.Inbounds), len(cfg.Outbounds))

	// 3. Merge
	m.log.Printf("[%s] STEP 3/7: merging LucX entries (in=%d out=%d rules=%d)", h, len(inbounds), len(outbounds), len(rules))
	merged := Merge(cfg, inbounds, outbounds, rules)
	m.log.Printf("[%s]   result inbounds=%d outbounds=%d", h, len(merged.Inbounds), len(merged.Outbounds))

	// 4. Atomic write
	m.log.Printf("[%s] STEP 4/7: atomic write config", h)
	if err := atomicWrite(ctx, m.client, merged); err != nil {
		Restore(ctx, m.client, backupPath)
		return backupPath, fmt.Errorf("write: %w", err)
	}

	// 5. Validate config
	m.log.Printf("[%s] STEP 5/7: testing config (xray run -test)", h)
	if err := TestConfig(ctx, m.client); err != nil {
		m.log.Printf("[%s]   CONFIG TEST FAILED — restoring backup", h)
		Restore(ctx, m.client, backupPath)
		return backupPath, fmt.Errorf("test: %w", err)
	}
	m.log.Printf("[%s]   config valid", h)

	// 6. Restart Xray
	m.log.Printf("[%s] STEP 6/7: restarting Xray", h)
	if err := restartXray(ctx, m.client); err != nil {
		m.log.Printf("[%s]   RESTART FAILED — restoring backup", h)
		Restore(ctx, m.client, backupPath)
		return backupPath, fmt.Errorf("restart: %w", err)
	}
	m.log.Printf("[%s]   Xray restarted", h)

	// 7. Verify ports
	if len(verifyPorts) > 0 {
		m.log.Printf("[%s] STEP 7/7: verifying ports %v", h, verifyPorts)
		for _, port := range verifyPorts {
			if err := VerifyPort(ctx, m.client, port); err != nil {
				m.log.Printf("[%s]   PORT %d NOT LISTENING — restoring backup", h, port)
				Restore(ctx, m.client, backupPath)
				return backupPath, fmt.Errorf("verify port %d: %w", port, err)
			}
			m.log.Printf("[%s]   port %d listening", h, port)
		}
	} else {
		m.log.Printf("[%s] STEP 7/7: no ports to verify", h)
	}

	m.log.Printf("[%s] APPLY COMPLETE — all steps passed", h)

	// Clean old backups, keep last N (best-effort, don't fail apply)
	if err := CleanOldBackups(ctx, m.client, DefaultBackupRetention); err != nil {
		m.log.Printf("[%s]   warning: backup cleanup failed: %v", h, err)
	}

	return backupPath, nil
}

// Rollback restores a backup, restarts Xray, and cleans old backups.
func (m *Manager) Rollback(ctx context.Context, backupPath string) error {
	m.log.Printf("[%s] ROLLBACK: restoring %s", m.host(), backupPath)
	if err := Restore(ctx, m.client, backupPath); err != nil {
		return err
	}
	if err := restartXray(ctx, m.client); err != nil {
		return err
	}
	// Clean old backups after rollback
	if err := CleanOldBackups(ctx, m.client, DefaultBackupRetention); err != nil {
		m.log.Printf("[%s]   warning: backup cleanup after rollback failed: %v", m.host(), err)
	}
	return nil
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
