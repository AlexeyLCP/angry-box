// Package xray implements the Backend interface for Xray-core.
//
// This backend is considered secondary / best-effort compared to sing-box.
// It is maintained at a basic functional level but receives significantly less
// development attention.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

const (
	xrayVersion = "26.0.0"
	installPath = "/usr/local/bin/xray"
	configDir   = "/usr/local/etc/xray"
	configFile  = "/usr/local/etc/xray/config.json"
	systemdUnit = "/etc/systemd/system/xray.service"
)

var _ ports.Backend = (*Backend)(nil)

// Backend manages xray proxy instances on remote hosts.
type Backend struct{}

// New creates a new xray Backend.
func New() *Backend {
	return &Backend{}
}

// Deploy installs xray on the remote host via SSH.
func (b *Backend) Deploy(ctx context.Context, host model.Host) (*model.DeployResult, error) {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("xray: deploy: %w", err)
	}
	defer client.Close()

	output, err := client.Run("xray version 2>/dev/null || echo NOT_INSTALLED")
	if err != nil {
		return nil, fmt.Errorf("xray: deploy: check version: %w", err)
	}

	if !strings.Contains(output, "NOT_INSTALLED") {
		return &model.DeployResult{
			Success: true,
			Version: strings.TrimSpace(output),
			Message: "xray already installed",
		}, nil
	}

	archOut, err := client.Run("uname -m")
	if err != nil {
		return nil, fmt.Errorf("xray: deploy: detect arch: %w", err)
	}

	arch := strings.TrimSpace(archOut)
	goArch := archToGoArch(arch)

	downloadURL := fmt.Sprintf(
		"https://github.com/XTLS/Xray-core/releases/download/v%s/Xray-linux-%s.zip",
		xrayVersion, goArch,
	)

	installScript := fmt.Sprintf(
		`set -e
mkdir -p /tmp/xray-install
cd /tmp/xray-install
curl -fsSL '%s' -o xray.zip
unzip -o xray.zip
cp xray %s
chmod +x %s
mkdir -p %s
rm -rf /tmp/xray-install
`,
		downloadURL, installPath, installPath, configDir,
	)

	_, err = client.Run(installScript)
	if err != nil {
		return nil, fmt.Errorf("xray: deploy: install: %w", err)
	}

	systemdContent := fmt.Sprintf(`[Unit]
Description=Xray Service
Documentation=https://github.com/XTLS/Xray-core
After=network.target nss-lookup.target

[Service]
User=root
ExecStart=%s run -config %s
Restart=on-failure
RestartSec=10
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, installPath, configFile)

	writeCmd := fmt.Sprintf("cat > %s << 'SYSTEMD_UNIT_EOF'\n%s\nSYSTEMD_UNIT_EOF", systemdUnit, systemdContent)
	_, err = client.Run(writeCmd)
	if err != nil {
		return nil, fmt.Errorf("xray: deploy: create systemd unit: %w", err)
	}

	_, err = client.Run("systemctl daemon-reload && systemctl enable xray && systemctl start xray")
	if err != nil {
		return nil, fmt.Errorf("xray: deploy: start service: %w", err)
	}

	return &model.DeployResult{
		Success: true,
		Version: xrayVersion,
		Message: fmt.Sprintf("xray %s installed and started", xrayVersion),
	}, nil
}

// ApplyConfig generates a config and pushes it to the remote host, then restarts xray.
func (b *Backend) ApplyConfig(ctx context.Context, host model.Host, cfgType model.ConfigType, params model.ConfigParams) error {
	cfg, err := b.GenerateConfig(cfgType, params)
	if err != nil {
		return fmt.Errorf("xray: applyConfig: %w", err)
	}

	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("xray: applyConfig: %w", err)
	}
	defer client.Close()

	var js json.RawMessage
	if err := json.Unmarshal([]byte(cfg.Content), &js); err != nil {
		return fmt.Errorf("xray: applyConfig: invalid JSON: %w", err)
	}

	writeCmd := fmt.Sprintf("mkdir -p %s && cat > %s << 'CONFIG_EOF'\n%s\nCONFIG_EOF",
		configDir, configFile, cfg.Content)

	if _, err := client.Run(writeCmd); err != nil {
		return fmt.Errorf("xray: applyConfig: write config: %w", err)
	}

	// Xray validates config at startup; test it.
	if _, err := client.Run(fmt.Sprintf("%s run -test -config %s", installPath, configFile)); err != nil {
		return fmt.Errorf("xray: applyConfig: config test failed: %w", err)
	}

	if _, err := client.Run("systemctl restart xray"); err != nil {
		return fmt.Errorf("xray: applyConfig: restart: %w", err)
	}

	return nil
}

// Remove stops the xray service and removes all installed files.
func (b *Backend) Remove(ctx context.Context, host model.Host) error {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("xray: remove: %w", err)
	}
	defer client.Close()

	script := `systemctl stop xray 2>/dev/null || true
systemctl disable xray 2>/dev/null || true
rm -f /etc/systemd/system/xray.service
systemctl daemon-reload 2>/dev/null || true
rm -f /usr/local/bin/xray
rm -rf /usr/local/etc/xray
rm -rf /var/log/xray
`

	if _, err := client.Run(script); err != nil {
		return fmt.Errorf("xray: remove: %w", err)
	}

	return nil
}

// GetStatus retrieves the xray process status from the remote host.
func (b *Backend) GetStatus(ctx context.Context, host model.Host) (*model.Status, error) {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("xray: getStatus: %w", err)
	}
	defer client.Close()

	output, err := client.Run("systemctl is-active xray 2>/dev/null || echo unknown")
	if err != nil {
		// Non-fatal: we can still return partial status
	}

	status := &model.Status{
		Running: strings.TrimSpace(output) == "active",
	}

	if verOut, err := client.Run("xray version 2>/dev/null || echo NONE"); err == nil {
		status.Version = strings.TrimSpace(verOut)
	}

	if pidOut, err := client.Run("systemctl show xray --property=MainPID --value 2>/dev/null || echo 0"); err == nil {
		pidStr := strings.TrimSpace(pidOut)
		fmt.Sscanf(pidStr, "%d", &status.PID)
	}

	if status.Running {
		if uptimeOut, err := client.Run("systemctl show xray --property=ActiveEnterTimestamp --value 2>/dev/null || echo ''"); err == nil {
			status.Uptime = strings.TrimSpace(uptimeOut)
		}
	}

	return status, nil
}

// Reload sends a graceful reload to xray on the remote host.
func (b *Backend) Reload(ctx context.Context, host model.Host) error {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("xray: reload: %w", err)
	}
	defer client.Close()

	if _, err := client.Run("systemctl reload xray 2>/dev/null || systemctl kill -s HUP xray"); err != nil {
		return fmt.Errorf("xray: reload: %w", err)
	}

	return nil
}

// Name returns the backend identifier.
func (b *Backend) Name() string { return "xray" }

// Version returns the managed xray version.
func (b *Backend) Version() string { return xrayVersion }

// archToGoArch maps uname -m output to the archive suffix used by Xray-core releases.
// Note: This mapping is Xray-specific and differs from sing-box.
func archToGoArch(arch string) string {
	switch arch {
	case "x86_64":
		return "64"
	case "aarch64":
		return "arm64-v8a"
	case "armv7l":
		return "armv7a"
	default:
		return "64"
	}
}
