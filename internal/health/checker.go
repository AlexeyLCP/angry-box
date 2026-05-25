package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/lucx-core/internal/backend"
	"github.com/alexeylcp/lucx-core/internal/ssh"
)

// Status represents a server health check result.
type Status struct {
	Online      bool   `json:"online"`
	LatencyMs   int64  `json:"latency_ms"`
	XrayRunning bool   `json:"xray_running"`
	XrayVersion string `json:"xray_version"`
	Error       string `json:"error,omitempty"`
}

// Check runs a health check against a server via SSH.
func Check(ctx context.Context, client *ssh.Client, be backend.ProxyBackend) *Status {
	s := &Status{Online: true}

	start := time.Now()
	if _, err := client.Exec("echo ok"); err != nil {
		s.Online = false
		s.Error = fmt.Sprintf("ssh: %v", err)
		return s
	}
	s.LatencyMs = time.Since(start).Milliseconds()

	out, err := client.Exec("systemctl is-active xray 2>/dev/null || echo stopped")
	if err != nil {
		s.Error = fmt.Sprintf("xray check: %v", err)
		return s
	}
	s.XrayRunning = strings.TrimSpace(out) == "active"

	if s.XrayRunning {
		verOut, _ := client.Exec("xray version 2>/dev/null | head -1")
		s.XrayVersion = strings.TrimSpace(verOut)
	}

	return s
}
