package xray

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexeylcp/lucx-core/internal/backend"
)

const (
	xrayInstallScript = "https://github.com/XTLS/Xray-install/raw/main/install-release.sh"
	xrayConfigPath    = "/usr/local/etc/xray/config.json"
	xrayBinaryPath    = "/usr/local/bin/xray"
)

func (x *XrayBackend) Install(ctx context.Context, ssh backend.SSHClient) (string, error) {
	out, err := ssh.Exec(fmt.Sprintf("%s version 2>/dev/null", xrayBinaryPath))
	if err == nil && strings.Contains(out, "Xray") {
		return xrayBinaryPath, nil
	}
	cmd := fmt.Sprintf("bash -c \"$(curl -L %s)\" @ install", xrayInstallScript)
	out, err = ssh.Exec(cmd)
	if err != nil {
		return "", fmt.Errorf("install xray: %w\noutput: %s", err, out)
	}
	return xrayBinaryPath, nil
}

func (x *XrayBackend) Start(ctx context.Context, ssh backend.SSHClient) error {
	_, err := ssh.Exec("systemctl start xray 2>/dev/null || service xray start 2>/dev/null || /etc/init.d/xray start 2>/dev/null")
	return err
}

func (x *XrayBackend) Stop(ctx context.Context, ssh backend.SSHClient) error {
	_, err := ssh.Exec("systemctl stop xray 2>/dev/null || service xray stop 2>/dev/null || /etc/init.d/xray stop 2>/dev/null")
	return err
}

func (x *XrayBackend) Status(ctx context.Context, ssh backend.SSHClient) (backend.BackendStatus, error) {
	status := backend.BackendStatus{}
	// Simple status check: return "active" or "stopped"/"unknown"
	out, _ := ssh.Exec("systemctl is-active xray 2>/dev/null || echo stopped")
	status.Running = strings.TrimSpace(out) == "active"
	verOut, _ := ssh.Exec(fmt.Sprintf("%s version 2>/dev/null | head -1", xrayBinaryPath))
	status.Version = strings.TrimSpace(verOut)
	return status, nil
}
