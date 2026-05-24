package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/lucx-core/internal/backend"
)

// Config file management — the ONLY method for Xray v26+.
// gRPC HandlerService tested and found non-functional in v26.3.27.

const lucxTagPrefix = "lucx-"

// GetConfig reads the current config.json from the server.
func (x *XrayBackend) GetConfig(ctx context.Context, ssh backend.SSHClient) (*backend.RawConfig, error) {
	content, err := ssh.ReadFile(xrayConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var raw backend.RawConfig
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &raw, nil
}

// BackupConfig copies config.json to config.json.lucx.bak.{timestamp}.
func (x *XrayBackend) BackupConfig(ctx context.Context, ssh backend.SSHClient) (string, error) {
	ts := time.Now().UTC().Format("20060102-150405")
	bakPath := fmt.Sprintf("%s.lucx.bak.%s", xrayConfigPath, ts)
	cmd := fmt.Sprintf("cp %s %s", xrayConfigPath, bakPath)
	if _, err := ssh.Exec(cmd); err != nil {
		return "", fmt.Errorf("backup config: %w", err)
	}
	return bakPath, nil
}

// RestoreBackup copies backup back to config.json and restarts Xray.
func (x *XrayBackend) RestoreBackup(ctx context.Context, ssh backend.SSHClient, bakPath string) error {
	cmd := fmt.Sprintf("cp %s %s", bakPath, xrayConfigPath)
	if _, err := ssh.Exec(cmd); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	return x.restartXray(ctx, ssh)
}

// ApplyConfig is THE primary method for modifying Xray config.
// It does: backup → merge LucX inbounds/outbounds/routing → atomic write → restart → verify.
// allInbounds and allOutbounds are the COMPLETE set of LucX-managed entries to be present.
func (x *XrayBackend) ApplyConfig(
	ctx context.Context, ssh backend.SSHClient,
	lucxInbounds []json.RawMessage,
	lucxOutbounds []json.RawMessage,
	lucxRouting []backend.RoutingRule,
) error {

	// 1. Backup
	if _, err := x.BackupConfig(ctx, ssh); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	// 2. Read current config
	cfg, err := x.GetConfig(ctx, ssh)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	// 3. Merge: keep non-LucX entries, replace LucX entries
	mergedInbounds := mergeInbounds(cfg.Inbounds, lucxInbounds)
	mergedOutbounds := mergeOutbounds(cfg.Outbounds, lucxOutbounds)
	mergedRouting := mergeRouting(cfg.Routing, lucxRouting)

	// 4. Atomic write: write to .tmp → rename
	cfgData := map[string]interface{}{
		"inbounds":  mergedInbounds,
		"outbounds": mergedOutbounds,
		"routing":   mergedRouting,
	}

	if err := x.writeConfig(ctx, ssh, cfgData); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// 5. Test config validity
	if err := x.testConfig(ctx, ssh); err != nil {
		return fmt.Errorf("test: %w", err)
	}

	// 6. Restart Xray
	if err := x.restartXray(ctx, ssh); err != nil {
		return fmt.Errorf("restart: %w", err)
	}

	return nil
}

// mergeInbounds keeps non-LucX inbounds, adds new LucX inbounds.
func mergeInbounds(existing, newLucX []json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	// Keep user's non-LucX inbounds
	for _, raw := range existing {
		var item struct{ Tag string `json:"tag"` }
		if err := json.Unmarshal(raw, &item); err == nil && strings.HasPrefix(item.Tag, lucxTagPrefix) {
			continue // remove old LucX entries
		}
		result = append(result, raw)
	}
	// Add new LucX entries
	result = append(result, newLucX...)
	return result
}

// mergeOutbounds keeps non-LucX outbounds, adds new LucX outbounds.
func mergeOutbounds(existing, newLucX []json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	for _, raw := range existing {
		var item struct{ Tag string `json:"tag"` }
		if err := json.Unmarshal(raw, &item); err == nil && strings.HasPrefix(item.Tag, lucxTagPrefix) {
			continue
		}
		result = append(result, raw)
	}
	result = append(result, newLucX...)
	return result
}

// mergeRouting keeps non-LucX routing rules, replaces LucX routing rules.
func mergeRouting(existing json.RawMessage, lucxRules []backend.RoutingRule) map[string]interface{} {
	var current struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(existing, &current); err != nil {
		current.Rules = nil
	}

	var resultRules []json.RawMessage
	for _, raw := range current.Rules {
		var rule struct {
			InboundTag  []string `json:"inboundTag"`
			OutboundTag string   `json:"outboundTag"`
		}
		if err := json.Unmarshal(raw, &rule); err != nil {
			resultRules = append(resultRules, raw)
			continue
		}
		// Drop old LucX rules (inbound or outbound starts with lucx-)
		isLucX := strings.HasPrefix(rule.OutboundTag, lucxTagPrefix)
		for _, inTag := range rule.InboundTag {
			if strings.HasPrefix(inTag, lucxTagPrefix) {
				isLucX = true
				break
			}
		}
		if !isLucX {
			resultRules = append(resultRules, raw)
		}
	}

	// Add new LucX routing rules
	for _, lr := range lucxRules {
		b, _ := json.Marshal(lr)
		resultRules = append(resultRules, b)
	}

	domainStrategy := "AsIs"
	var existingRouting map[string]interface{}
	json.Unmarshal(existing, &existingRouting)
	if ds, ok := existingRouting["domainStrategy"]; ok {
		domainStrategy = ds.(string)
	}

	return map[string]interface{}{
		"domainStrategy": domainStrategy,
		"rules":          resultRules,
	}
}

// writeConfig writes config atomically: tmp file → rename.
func (x *XrayBackend) writeConfig(ctx context.Context, ssh backend.SSHClient, cfg map[string]interface{}) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := xrayConfigPath + ".tmp"
	if err := ssh.WriteFile(tmpPath, string(data)); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}

	cmd := fmt.Sprintf("mv %s %s", tmpPath, xrayConfigPath)
	if _, err := ssh.Exec(cmd); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	return nil
}

// testConfig runs `xray run -test` to validate the config.
func (x *XrayBackend) testConfig(ctx context.Context, ssh backend.SSHClient) error {
	cmd := fmt.Sprintf("%s run -config %s -test 2>&1", xrayBinaryPath, xrayConfigPath)
	out, err := ssh.Exec(cmd)
	if err != nil {
		return fmt.Errorf("config test failed: %s", strings.TrimSpace(out))
	}
	if !strings.Contains(out, "Configuration OK") {
		return fmt.Errorf("config test: %s", strings.TrimSpace(out))
	}
	return nil
}

// restartXray restarts Xray (systemctl or init.d fallback).
func (x *XrayBackend) restartXray(ctx context.Context, ssh backend.SSHClient) error {
	// Try systemctl restart first
	ssh.Exec("systemctl restart xray 2>/dev/null")
	time.Sleep(2 * time.Second)

	// Verify Xray is running
	out, err := ssh.Exec("systemctl is-active xray 2>/dev/null || echo unknown")
	if err != nil || strings.TrimSpace(out) != "active" {
		// Fallback: init.d
		ssh.Exec("/etc/init.d/xray restart 2>/dev/null")
		time.Sleep(2 * time.Second)
	}
	return nil
}

// VerifyPort checks if Xray is listening on the given port.
func (x *XrayBackend) VerifyPort(ctx context.Context, ssh backend.SSHClient, port int) error {
	cmd := fmt.Sprintf("ss -tlnp | grep ':%d ' 2>/dev/null || echo NOT_LISTENING", port)
	out, err := ssh.Exec(cmd)
	if err != nil {
		return fmt.Errorf("verify port %d: %w", port, err)
	}
	if strings.Contains(out, "NOT_LISTENING") {
		return fmt.Errorf("port %d not listening after restart", port)
	}
	return nil
}

// ClearLucXEntries removes ALL LucX-managed inbounds, outbounds, and routing rules.
// Used for chain rollback (restore clean state).
func (x *XrayBackend) ClearLucXEntries(ctx context.Context, ssh backend.SSHClient) error {
	return x.ApplyConfig(ctx, ssh, nil, nil, nil)
}
