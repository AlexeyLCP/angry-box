package chain

// applier_push.go — the SSH I/O layer of the deploy pipeline, split out of
// applier.go (AGENTS.md #4: config generation and SSH I/O must not be mixed in
// one file). This file owns the remote-touching deploy sequence only:
//
//	createBackup / performRollback / cleanupBackups   — remote file ops
//	pushConfig / pushConfigLocked                     — the full deploy sequence
//	probeServiceUp                                    — is-active health probe
//	ensureCertForTLSInbounds                          — best-effort self-signed cert
//
// Pure config generation (build*Inbound/Outbound, RenderClientConfig, etc.)
// stays in applier.go / merged_config.go / clientconfig.go. The split is
// mechanical: no logic changed, only the file boundary (CTO-review §4 finding:
// applier.go mixed pure-config-gen + SSH I/O — violation of layering).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// createBackup makes a timestamped backup of the current config under $HOME
// (writable without sudo) and returns the backup path. Uses cp — the backup is
// PRESERVED (never destroyed by rollback) so a second recovery attempt is
// always possible. Returns ("", nil) when there is no existing config (first
// deploy); callers must tolerate that (rollback becomes a no-op restore).
func createBackup(client ports.SSHClient, file string) (string, error) {
	// Name the backup after the source file's basename so multiple files backed
	// up in the same second (a multi-file AWG push: awg0.conf + awg-exit-n1.conf
	// + ...) don't all collide into one "config.json.bak" and clobber each other.
	// For the sing-box path (/etc/sing-box/config.json → "config.json.bak") this
	// is identical to the old hardcoded behavior. For AWG confs each gets its own
	// "<basename>.bak" inside the timestamped dir.
	bakName := filepath.Base(file) + ".bak"
	cmd := `set -e
HOME_DIR="${HOME:-/tmp}"
BAK_DIR="$HOME_DIR/sing-box-orch-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BAK_DIR"
if [ -f "` + file + `" ]; then
	cp -p "` + file + `" "$BAK_DIR/` + bakName + `"
	echo "$BAK_DIR/` + bakName + `"
else
	# No prior config — record an empty backup path so the caller knows rollback
	# is unavailable, but still return the marker dir for consistency.
	echo "$BAK_DIR/` + bakName + `"
fi`
	out, err := client.Run(cmd)
	return strings.TrimSpace(out), err
}

// performRollback restores the backup via cp (NOT mv — the backup is preserved
// for a second attempt), then restarts the service. If the backup file does not
// exist (first deploy with no prior config) this is a no-op restore.
func performRollback(client ports.SSHClient, file, backupPath, serviceName string, useSudo bool) error {
	if backupPath == "" {
		return fmt.Errorf("no backup path provided")
	}
	cmd := fmt.Sprintf(`test -f %s && cp %s %s; systemctl restart %s; sleep 2; systemctl is-active --quiet %s || true`,
		backupPath, backupPath, file, serviceName, serviceName)
	if useSudo {
		cmd = fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	_, err := client.Run(cmd)
	if err != nil {
		slog.Error("deploy: rollback FAILED",
			"file", file, "backup", backupPath, "service", serviceName, "err", err)
		return fmt.Errorf("rollback failed: %w", err)
	}
	slog.Warn("deploy: rollback applied (restored previous config)",
		"file", file, "backup", backupPath, "service", serviceName)
	return nil
}

// cleanupBackups keeps only the last 5 backups.
func cleanupBackups(client ports.SSHClient, file string) {
	client.Run(fmt.Sprintf(`ls -t %s.bak.* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true`, file))
}

// pushConfig writes the config to the remote host with the reliable deploy
// sequence: backup (cp, before write) → self-signed cert (if TLS inbounds) →
// upload via stdin cat (no heredoc) → sing-box check (stdout+stderr captured) →
// rollback on check-fail (cp, backup preserved) → systemctl restart → real
// health-probe (is-active with retry, journalctl on failure) → rollback on
// inactive. useSudo wraps privileged commands for non-root SSH users. Returns
// a human-readable result string and an error on failure.
//
// nodeID drives the per-host serialization: the whole backup→write→restart→
// rollback sequence runs under withHostLock(nodeID), so concurrent applies
// (CLI, web, background auto-apply, takeover) targeting the same node cannot
// interleave and corrupt the rollback chain (CTO-review C2). This is the
// SINGLE serialization chokepoint — callers must NOT wrap pushConfig in another
// withHostLock(nodeID) (sync.Mutex is not reentrant → deadlock). An empty
// nodeID skips locking (only acceptable for throwaway test hosts).
//
// ctx is propagated to every SSH RunWithOutput/UploadText call so a cancelled
// UI deploy cancels the in-flight SSH commands instead of waiting out the
// timeout (CTO-review §8: context.Context was not threaded into the SSH push).
func pushConfig(ctx context.Context, client ports.SSHClient, nodeID, cfgContent string, useSudo bool) (string, error) {
	if nodeID == "" {
		return pushConfigLocked(ctx, client, cfgContent, useSudo)
	}
	type pushResult struct {
		out string
		err error
	}
	r := withHostLock(nodeID, func() pushResult {
		out, err := pushConfigLocked(ctx, client, cfgContent, useSudo)
		return pushResult{out: out, err: err}
	})
	return r.out, r.err
}

// pushConfigLocked performs the actual deploy sequence. The caller is
// responsible for holding the per-host lock (via pushConfig/withHostLock).
func pushConfigLocked(ctx context.Context, client ports.SSHClient, cfgContent string, useSudo bool) (string, error) {

	// sudo wraps a single command; sudoBash wraps a pipeline.
	sudo := func(cmd string) string {
		if useSudo {
			return "sudo " + cmd
		}
		return cmd
	}
	sudoB := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}

	var js json.RawMessage
	if err := json.Unmarshal([]byte(cfgContent), &js); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	configFile := "/etc/sing-box/config.json"

	// 1. Backup existing config (best-effort, never blocks the deploy).
	backupPath, backupErr := createBackup(client, configFile)
	if backupErr != nil {
		log.Printf("pushConfig: backup warning for %s: %v", configFile, backupErr)
	}

	// 2. Ensure self-signed TLS cert exists when the config references TLS-based
	// inbounds (TUIC/Hysteria2/VLESS/Trojan). Generated via openssl; best-effort.
	ensureCertForTLSInbounds(ctx, client, cfgContent)

	// 3. Upload via stdin cat. When useSudo, the target (/etc/sing-box/config.json)
	// is root-owned, so we write to $HOME first and sudo cp into place (UploadText
	// itself can't sudo the cat, and the path isn't writable as lcp).
	if useSudo {
		tmp := "/tmp/angry-config-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".json"
		if err := client.UploadText(ctx, cfgContent, tmp, 0o644); err != nil {
			return "", fmt.Errorf("write config (tmp): %w", err)
		}
		if _, _, _, err := client.RunWithOutput(ctx,
			sudoB("cp "+tmp+" "+configFile+" && chmod 644 "+configFile+" && rm -f "+tmp), 30*time.Second); err != nil {
			return "", fmt.Errorf("write config: %w", err)
		}
	} else {
		if err := client.UploadText(ctx, cfgContent, configFile, 0o644); err != nil {
			return "", fmt.Errorf("write config: %w", err)
		}
	}

	// 4. sing-box check — capture BOTH streams so the operator sees the real
	// validation error instead of an opaque "exit status 1".
	checkCmd := sudo(fmt.Sprintf("/usr/local/bin/sing-box check -c %s", configFile))
	_, stderr, exit, err := client.RunWithOutput(ctx, checkCmd, 60*time.Second)
	if err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo)
			if rbErr != nil {
				// Rollback failed → node left in a broken state → not retry-able.
				return "", fmt.Errorf("check failed (exit %d): %s | AND rollback failed: %v: %w", exit, stderr, rbErr, ErrRollbackFailed)
			}
			// Rollback succeeded → node still running old config → retry-able.
			return "", fmt.Errorf("rolled back — check failed (exit %d): %s: %w", exit, stderr, ErrDeployFailed)
		}
		// No backup (first deploy) → nothing to roll back to → deploy-failed.
		return "", fmt.Errorf("check failed (exit %d, no backup): %s: %w", exit, stderr, ErrDeployFailed)
	}

	// 5. Restart. No 2>&1 (that would swallow the useful stderr into stdout,
	// which Run discards on error). Keep stderr separate for the error path.
	if _, _, _, err := client.RunWithOutput(ctx, sudoB("systemctl restart sing-box"), 60*time.Second); err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo)
			if rbErr != nil {
				return "", fmt.Errorf("restart failed: %v | AND rollback failed: %w", err, ErrRollbackFailed)
			}
			return "", fmt.Errorf("rolled back — restart failed: %v: %w", err, ErrDeployFailed)
		}
		return "", fmt.Errorf("restart failed (no backup): %v: %w", err, ErrDeployFailed)
	}

	// 6. Real health-probe: is-active with a short retry (handles the brief
	// "activating" window), and capture journalctl on failure for diagnosis.
	if err := probeServiceUp(ctx, client, "sing-box", useSudo); err != nil {
		slog.Error("deploy: service not active after restart — rolling back",
			"err", err)
		if backupPath != "" {
			if rbErr := performRollback(client, configFile, backupPath, "sing-box", useSudo); rbErr != nil {
				slog.Error("deploy: health-probe rollback also failed",
					"file", configFile, "backup", backupPath, "err", rbErr)
				return "", fmt.Errorf("service not active after restart: %v | AND rollback failed: %v: %w", err, rbErr, ErrRollbackFailed)
			}
		}
		return "", fmt.Errorf("service not active after restart: %v: %w", err, ErrDeployFailed)
	}

	// 7. Cleanup old backups.
	cleanupBackups(client, configFile)

	return "success", nil
}

// probeServiceUp waits up to ~7s for the unit to become active. On failure it
// returns the last 30 journalctl lines so the operator sees why sing-box didn't
// start (the old implementation reported success as long as `systemctl restart`
// returned 0, which is NOT the same as the service staying up).
func probeServiceUp(ctx context.Context, client ports.SSHClient, service string, useSudo bool) error {
	sudoB := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	check := sudoB("sleep 3 && systemctl is-active --quiet " + service + " && echo UP || echo DOWN")
	for attempt := 0; attempt < 3; attempt++ {
		out, _, _, _ := client.RunWithOutput(ctx, check, 30*time.Second)
		if strings.TrimSpace(out) == "UP" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	journal, _, _, _ := client.RunWithOutput(ctx,
		sudoB("journalctl -u "+service+" -n 30 --no-pager 2>/dev/null"), 30*time.Second)
	tail := strings.TrimSpace(journal)
	if len(tail) > 1200 {
		tail = tail[len(tail)-1200:]
	}
	return fmt.Errorf("service not active; journal:\n%s", tail)
}

// ensureCertForTLSInbounds generates a self-signed cert (best-effort) when the
// config has TLS-based inbounds that reference /etc/sing-box/cert.pem. This
// replaces the old writeTUICCert/base64 path, which only covered TUIC and used
// a hardcoded CN. Here we cover all TLS inbounds and use the host's address.
func ensureCertForTLSInbounds(ctx context.Context, client ports.SSHClient, cfgContent string) {
	needsCert := strings.Contains(cfgContent, `"type":"tuic"`) ||
		strings.Contains(cfgContent, `"type": "tuic"`) ||
		strings.Contains(cfgContent, `"type":"hysteria2"`) ||
		strings.Contains(cfgContent, `"type": "hysteria2"`) ||
		strings.Contains(cfgContent, `"certificate_path":"/etc/sing-box/cert.pem"`) ||
		strings.Contains(cfgContent, `"certificate_path": "/etc/sing-box/cert.pem"`)
	if !needsCert {
		return
	}
	certCmd := `test -f /etc/sing-box/cert.pem || (which openssl >/dev/null 2>&1 && \
openssl req -x509 -newkey rsa:2048 -keyout /etc/sing-box/key.pem \
-out /etc/sing-box/cert.pem -days 3650 -nodes -subj "/CN=sing-box" 2>/dev/null && \
chmod 644 /etc/sing-box/cert.pem /etc/sing-box/key.pem) \
|| echo 'cert-gen skipped'`
	stdout, stderr, exitCode, runErr := client.RunWithOutput(ctx, certCmd, 60*time.Second)
	if runErr != nil {
		slog.Warn("deploy: self-signed cert generation command failed",
			"stdout", strings.TrimSpace(stdout), "stderr", strings.TrimSpace(stderr), "exit_code", exitCode, "err", runErr)
	} else if strings.Contains(stdout, "cert-gen skipped") {
		slog.Info("deploy: self-signed cert generation skipped (openssl missing or cert already present)")
	}
}
