package config

import (
	"context"
	"fmt"
	"time"

	"github.com/alexeylcp/lucx-core/internal/ssh"
)

const configPath = "/usr/local/etc/xray/config.json"

// DefaultBackupRetention is how many backups to keep after apply/rollback.
const DefaultBackupRetention = 7

// Backup creates a timestamped backup of config.json.
// Returns the backup file path for later rollback.
func Backup(ctx context.Context, client *ssh.Client) (string, error) {
	ts := time.Now().UTC().Format("20060102-150405")
	bakPath := fmt.Sprintf("%s.lucx.bak.%s", configPath, ts)
	if _, err := client.Exec(fmt.Sprintf("cp %s %s", configPath, bakPath)); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	return bakPath, nil
}

// Restore copies a backup back to config.json and returns the restored path.
func Restore(ctx context.Context, client *ssh.Client, bakPath string) error {
	if _, err := client.Exec(fmt.Sprintf("cp %s %s", bakPath, configPath)); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

// ListBackups returns available lucx backup files (newest first).
func ListBackups(ctx context.Context, client *ssh.Client) ([]string, error) {
	out, err := client.Exec(fmt.Sprintf("ls -t %s.lucx.bak.* 2>/dev/null", configPath))
	if err != nil {
		return nil, nil // no backups is OK
	}
	return splitLines(out), nil
}

// CleanOldBackups removes backups older than keepN (keeps newest keepN).
func CleanOldBackups(ctx context.Context, client *ssh.Client, keepN int) error {
	backups, err := ListBackups(ctx, client)
	if err != nil {
		return err
	}
	for i := keepN; i < len(backups); i++ {
		client.Exec(fmt.Sprintf("rm -f %s", backups[i]))
	}
	return nil
}

func splitLines(s string) []string {
	var result []string
	for _, line := range split(s, '\n') {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func split(s string, sep byte) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}
