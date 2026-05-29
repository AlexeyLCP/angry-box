package singbox

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
	singBoxVersion = "1.13.11-extended-2.1.0"
	installPath    = "/usr/local/bin/sing-box"
	configDir      = "/etc/sing-box"
	configFile     = "/etc/sing-box/config.json"
	systemdUnit    = "/etc/systemd/system/sing-box.service"
	logDir         = "/var/log/sing-box"
)

// sing-box-extended releases from shtorm-7/sing-box-extended
// Archive name pattern: sing-box-{version}-extended-{ext_ver}-linux-{arch}.tar.gz
var singBoxChecksums = map[string]string{
	"amd64": "",
	"arm64": "",
	"armv7": "",
}

var _ ports.Backend = (*Backend)(nil)

// Backend manages sing-box proxy instances on remote hosts.
type Backend struct{}

// New creates a new sing-box Backend.
func New() *Backend {
	return &Backend{}
}

// Deploy installs sing-box on the remote host via SSH.
func (b *Backend) Deploy(ctx context.Context, host model.Host) (*model.DeployResult, error) {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("singbox: deploy: %w", err)
	}
	defer client.Close()


	// Check if already installed.
	output, err := client.Run("sing-box version 2>/dev/null || echo NOT_INSTALLED")
	if err != nil {
		return nil, fmt.Errorf("singbox: deploy: check version: %w", err)
	}

	if !strings.Contains(output, "NOT_INSTALLED") {
		installedVersion := strings.TrimSpace(strings.TrimPrefix(output, "sing-box version "))
		return &model.DeployResult{
			Success: true,
			Version: installedVersion,
			Message: "sing-box already installed",
		}, nil
	}

	// Detect architecture.
	archOut, err := client.Run("uname -m")
	if err != nil {
		return nil, fmt.Errorf("singbox: deploy: detect arch: %w", err)
	}

	arch := strings.TrimSpace(archOut)
	goArch := archToGoArch(arch)

	// Download and install sing-box-extended from shtorm-7 community releases.
	// This fork includes AmneziaWG (wireguard inbound) and advanced obfuscation support.
	downloadURL := fmt.Sprintf(
		"https://github.com/shtorm-7/sing-box-extended/releases/download/v%s/sing-box-%s-linux-%s.tar.gz",
		singBoxVersion, singBoxVersion, goArch,
	)

	expectedChecksum, hasChecksum := singBoxChecksums[goArch]

	installScript := fmt.Sprintf(
		`set -e
mkdir -p /tmp/sing-box-install
cd /tmp/sing-box-install
curl -fsSL '%s' -o sing-box.tar.gz
`,
		downloadURL,
	)

	if hasChecksum && expectedChecksum != "" {
		installScript += fmt.Sprintf(
			`echo '%s  sing-box.tar.gz' | sha256sum -c -
`,
			expectedChecksum,
		)
	} else {
		// Warn in log if no checksum (should be temporary)
		installScript += "echo 'WARNING: No checksum available for this architecture - skipping verification' >&2\n"
	}

	installScript += fmt.Sprintf(
		`tar xzf sing-box.tar.gz
cp sing-box-*/sing-box %s
chmod +x %s
mkdir -p %s %s
rm -rf /tmp/sing-box-install
`,
		installPath, installPath, configDir, logDir,
	)

	_, err = client.Run(installScript)
	if err != nil {
		return nil, fmt.Errorf("singbox: deploy: install binary: %w", err)
	}

	// Create systemd service.
	systemdContent := fmt.Sprintf(`[Unit]
Description=sing-box service
Documentation=https://sing-box.sagernet.org
After=network.target nss-lookup.target

[Service]
User=root
WorkingDirectory=%s
ExecStart=%s run -c %s
Restart=on-failure
RestartSec=10
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, configDir, installPath, configFile)

	writeCmd := fmt.Sprintf("cat > %s << 'SYSTEMD_UNIT_EOF'\n%s\nSYSTEMD_UNIT_EOF", systemdUnit, systemdContent)
	_, err = client.Run(writeCmd)
	if err != nil {
		// Attempt partial cleanup
		_, _ = client.Run(fmt.Sprintf("rm -f %s", installPath))
		return nil, fmt.Errorf("singbox: deploy: create systemd unit: %w", err)
	}


	// Reload systemd, enable and start.
	_, err = client.Run("systemctl daemon-reload && systemctl enable sing-box && systemctl start sing-box")
	if err != nil {
		return nil, fmt.Errorf("singbox: deploy: enable and start service: %w", err)
	}

	return &model.DeployResult{
		Success: true,
		Version: singBoxVersion,
		Message: fmt.Sprintf("sing-box %s installed and started", singBoxVersion),
	}, nil
}

// ApplyConfig generates a config and pushes it to the remote host.
// It creates a backup of the previous config and attempts rollback on failure.
func (b *Backend) ApplyConfig(ctx context.Context, host model.Host, cfgType model.ConfigType, params model.ConfigParams) error {
	cfg, err := b.GenerateConfig(cfgType, params)
	if err != nil {
		return fmt.Errorf("singbox: applyConfig: %w", err)
	}

	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("singbox: applyConfig: %w", err)
	}
	defer client.Close()

	// Validate JSON structure before touching remote.
	var js json.RawMessage
	if err := json.Unmarshal([]byte(cfg.Content), &js); err != nil {
		return fmt.Errorf("singbox: applyConfig: invalid JSON: %w", err)
	}

	// Backup existing config if present.
	backupCmd := fmt.Sprintf(
		"if [ -f %s ]; then cp %s %s.bak.$(date +%%s); fi",
		configFile, configFile, configFile,
	)
	_, _ = client.Run(backupCmd) // best effort

	// Write new config.
	writeCmd := fmt.Sprintf("mkdir -p %s && cat > %s << 'CONFIG_EOF'\n%s\nCONFIG_EOF",
		configDir, configFile, cfg.Content)

	if _, err := client.Run(writeCmd); err != nil {
		return fmt.Errorf("singbox: applyConfig: write config: %w", err)
	}

	// Validate with sing-box check.
	checkCmd := fmt.Sprintf("%s check -c %s", installPath, configFile)
	if _, err := client.Run(checkCmd); err != nil {
		// Attempt rollback
		rollbackCmd := fmt.Sprintf(
			"if [ -f %s.bak.* ]; then latest=$(ls -t %s.bak.* | head -1); cp \"$latest\" %s; fi",
			configFile, configFile, configFile,
		)
		_, _ = client.Run(rollbackCmd)
		return fmt.Errorf("singbox: applyConfig: config validation failed (rollback attempted): %w", err)
	}

	// Prefer reload for minimal disruption, fall back to restart.
	reloadCmd := fmt.Sprintf("%s reload -c %s 2>/dev/null || systemctl reload sing-box 2>/dev/null || systemctl restart sing-box", installPath, configFile)
	if _, err := client.Run(reloadCmd); err != nil {
		return fmt.Errorf("singbox: applyConfig: reload/restart failed: %w", err)
	}

	return nil
}

// Remove stops the service and removes all installed files from the remote host.
func (b *Backend) Remove(ctx context.Context, host model.Host) error {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("singbox: remove: %w", err)
	}
	defer client.Close()

	script := `systemctl stop sing-box 2>/dev/null || true
systemctl disable sing-box 2>/dev/null || true
rm -f /etc/systemd/system/sing-box.service
systemctl daemon-reload 2>/dev/null || true
rm -f /usr/local/bin/sing-box
rm -rf /etc/sing-box
rm -rf /var/log/sing-box
# Clean up old config backups (keep last 3 days worth if any)
find /etc/sing-box -name 'config.json.bak.*' -mtime +3 -delete 2>/dev/null || true
`

	if _, err := client.Run(script); err != nil {
		return fmt.Errorf("singbox: remove: %w", err)
	}

	return nil
}

// GetStatus retrieves the sing-box process status from the remote host.
func (b *Backend) GetStatus(ctx context.Context, host model.Host) (*model.Status, error) {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("singbox: getStatus: %w", err)
	}
	defer client.Close()

	output, err := client.Run("systemctl is-active sing-box 2>/dev/null || echo unknown")
	if err != nil {
		// Non-zero exit from systemctl means inactive.
	}

	status := &model.Status{
		Running: strings.TrimSpace(output) == "active",
	}

	// Get version.
	if verOut, err := client.Run("sing-box version 2>/dev/null || echo NONE"); err == nil {
		status.Version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(verOut), "sing-box version "))
	}

	// Get PID and uptime.
	if pidOut, err := client.Run("systemctl show sing-box --property=MainPID --value 2>/dev/null || echo 0"); err == nil {
		pidStr := strings.TrimSpace(pidOut)
		fmt.Sscanf(pidStr, "%d", &status.PID)
	}

	if status.Running {
		if uptimeOut, err := client.Run("systemctl show sing-box --property=ActiveEnterTimestamp --value 2>/dev/null || echo ''"); err == nil {
			status.Uptime = strings.TrimSpace(uptimeOut)
		}
	}

	return status, nil
}

// Reload sends a graceful reload signal to sing-box on the remote host.
func (b *Backend) Reload(ctx context.Context, host model.Host) error {
	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("singbox: reload: %w", err)
	}
	defer client.Close()

	// Validate config first, then reload.
	checkCmd := fmt.Sprintf("%s check -c %s", installPath, configFile)
	if _, err := client.Run(checkCmd); err != nil {
		return fmt.Errorf("singbox: reload: refusing reload, config invalid: %w", err)
	}

	reloadCmd := "systemctl reload sing-box 2>/dev/null || systemctl kill -s HUP sing-box"
	if _, err := client.Run(reloadCmd); err != nil {
		return fmt.Errorf("singbox: reload: %w", err)
	}

	return nil
}

// Name returns the backend identifier.
func (b *Backend) Name() string { return "sing-box" }

// Version returns the managed sing-box version.
func (b *Backend) Version() string { return singBoxVersion }

func archToGoArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv7", "arm":
		return "armv7"
	case "i386", "i686":
		return "386"
	default:
		return arch
	}
}
