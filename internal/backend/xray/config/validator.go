package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexeylcp/lucx-core/internal/ssh"
)

const xrayBinary = "/usr/local/bin/xray"

// TestConfig validates config.json via `xray run -test`.
func TestConfig(ctx context.Context, client *ssh.Client) error {
	out, err := client.Exec(fmt.Sprintf("%s run -config %s -test 2>&1", xrayBinary, configPath))
	if err != nil {
		return fmt.Errorf("config test failed: %s", strings.TrimSpace(out))
	}
	if !strings.Contains(out, "Configuration OK") {
		return fmt.Errorf("config invalid: %s", strings.TrimSpace(out))
	}
	return nil
}

// VerifyPort checks if a port is listening after Xray restart.
func VerifyPort(ctx context.Context, client *ssh.Client, port int) error {
	out, err := client.Exec(fmt.Sprintf("ss -tlnp 2>/dev/null | grep ':%d ' || echo NOT_LISTENING", port))
	if err != nil {
		return fmt.Errorf("verify port %d: %w", port, err)
	}
	if strings.Contains(out, "NOT_LISTENING") {
		return fmt.Errorf("port %d not listening", port)
	}
	return nil
}

// CheckNoConflicts verifies LucX tags don't conflict with existing config.
func CheckNoConflicts(ctx context.Context, client *ssh.Client, tags []string) error {
	content, err := client.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	for _, tag := range tags {
		if strings.Count(content, fmt.Sprintf(`"tag":"%s"`, tag)) > 0 ||
			strings.Count(content, fmt.Sprintf(`"tag": "%s"`, tag)) > 0 {
			return fmt.Errorf("tag %q already exists in config", tag)
		}
	}
	return nil
}

// GetCurrentTags extracts all tag values from the current config.
func GetCurrentTags(ctx context.Context, client *ssh.Client) ([]string, error) {
	content, err := client.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var tags []string
	var cfg struct {
		Inbounds  []json.RawMessage `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, err
	}
	for _, raw := range cfg.Inbounds {
		var item struct{ Tag string `json:"tag"` }
		if json.Unmarshal(raw, &item) == nil {
			tags = append(tags, item.Tag)
		}
	}
	for _, raw := range cfg.Outbounds {
		var item struct{ Tag string `json:"tag"` }
		if json.Unmarshal(raw, &item) == nil {
			tags = append(tags, item.Tag)
		}
	}
	return tags, nil
}
