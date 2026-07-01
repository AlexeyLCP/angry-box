package chain

// takeover_helpers.go — exported shims over the unexported deploy primitives so
// the internal/takeover package can drive pushConfig/createBackup/rollback
// without an import cycle (takeover needs chain + singbox, which cross-ref).
// These are production helpers (no build tag), used by the takeover executor.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// PushConfig is the exported wrapper around pushConfig: writes cfgContent to
// /etc/sing-box/config.json with the reliable deploy sequence (backup → cert →
// upload → check → restart → health-probe → rollback on failure). useSudo wraps
// privileged commands for non-root SSH users.
func PushConfig(client *sshclient.Client, cfgContent string, useSudo bool) (string, error) {
	return pushConfig(client, cfgContent, useSudo)
}

// CreateBackup is the exported wrapper around createBackup: copies file to a
// timestamped $HOME/sing-box-orch-backup-<ts>/ dir and returns the backup path.
// Preserved (never destroyed by rollback). Returns ("", nil) if file is absent.
func CreateBackup(client *sshclient.Client, file string) (string, error) {
	return createBackup(client, file)
}

// RecordDeploySuccess stamps info with the sha256 of cfgJSON + now and persists
// it via SaveNodeInfo. Best-effort (errors logged, not propagated).
func RecordDeploySuccess(store *Store, nodeID, cfgJSON string) {
	recordDeploySuccess(store, nodeID, cfgJSON)
}

// ProbeServiceUp is the exported wrapper around probeServiceUp: waits ~7s for
// the unit to become active, returns journalctl tail on failure.
func ProbeServiceUp(client *sshclient.Client, service string, useSudo bool) error {
	return probeServiceUp(client, service, useSudo)
}

// DisableService stops + disables a systemd unit WITHOUT deleting its unit file
// or config (contrast with Backend.Remove which deletes). Used by takeover to
// disable the old VPN while keeping it recoverable. useSudo via sudoBash.
func DisableService(client *sshclient.Client, service string, useSudo bool) error {
	cmd := "systemctl stop " + service + " && systemctl disable " + service
	if useSudo {
		cmd = fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	_, _, _, err := client.RunWithOutput(context.Background(), cmd, 30*time.Second)
	if err != nil {
		return fmt.Errorf("disable %s: %w", service, err)
	}
	return nil
}

// EnableService enables + starts a systemd unit (rollback path: re-enable the
// old VPN). useSudo via sudoBash.
func EnableService(client *sshclient.Client, service string, useSudo bool) error {
	cmd := "systemctl enable " + service + " && systemctl start " + service
	if useSudo {
		cmd = fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	_, _, _, err := client.RunWithOutput(context.Background(), cmd, 30*time.Second)
	if err != nil {
		return fmt.Errorf("enable %s: %w", service, err)
	}
	return nil
}

// IsServiceEnabled reports whether a systemd unit is enabled. useSudo via sudo.
func IsServiceEnabled(client *sshclient.Client, service string, useSudo bool) bool {
	cmd := "systemctl is-enabled " + service + " 2>/dev/null"
	if useSudo {
		cmd = "sudo " + cmd
	}
	out, _, _, _ := client.RunWithOutput(context.Background(), cmd, 15*time.Second)
	return strings.TrimSpace(out) == "enabled"
}

// RestoreFile copies a backed-up file back to its original path (rollback).
func RestoreFile(client *sshclient.Client, backupPath, destPath string, useSudo bool) error {
	if backupPath == "" {
		return fmt.Errorf("no backup path")
	}
	cmd := fmt.Sprintf("test -f %s && cp %s %s", backupPath, backupPath, destPath)
	if useSudo {
		cmd = fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	_, _, _, err := client.RunWithOutput(context.Background(), cmd, 30*time.Second)
	return err
}

// WriteTakeoverAudit records a takeover action in the audit log.
func WriteTakeoverAudit(store *Store, nodeID, fromType, status string, convertedInbounds int) {
	WriteAudit(store, "takeover", "node", nodeID,
		AuditPayload{"from": fromType, "to": "sing-box", "status": status, "converted_inbounds": convertedInbounds},
		"operator")
}

// NodeInfoForTakeover loads a NodeInfo by id, creating a minimal one if absent
// (so the takeover state can be persisted even for a host-only node).
func NodeInfoForTakeover(store *Store, host model.Host) *model.NodeInfo {
	info, err := store.GetNodeInfo(host.ID)
	if err != nil {
		info = &model.NodeInfo{}
	}
	info.Host = host
	return info
}