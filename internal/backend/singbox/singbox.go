package singbox

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"github.com/alexeylcp/angry-box/internal/version"
)

const (
	// singBoxVersion is the short SHA of the amnezia-box fork commit we build
	// the sing-box binary from. amnezia-box (our fork AlexeyLCP/amnezia-box, a
	// fork of hoaxisr/amnezia-box sing-box 1.14) carries AWG 3.1 userspace
	// endpoint type:"awg" (amneziawg-go /v3) + ports from
	// sing-box-extended: mtproxy (with_mtproxy), TrustTunnel (with_trusttunnel),
	// and fallback round-robin. amneziawg-go is pinned in the fork's go.mod
	// (hoaxisr/amneziawg-go/v3 @ ae4523c / v3.1.0-awgm.1).
	//
	// Built by scripts/build-singbox.sh; tarball in deps/. Mirrored in
	// patchcheck_test.go (patchcheckABXRef) — bump all three together.
	singBoxVersion = "922fc605"
	installPath    = "/usr/local/bin/sing-box"
	configDir      = "/etc/sing-box"
	configFile     = "/etc/sing-box/config.json"
	systemdUnit    = "/etc/systemd/system/sing-box.service"
	logDir         = "/var/log/sing-box"
	systemdService = "sing-box"
)

// singBoxDownloadURLs maps Go arch → the amnezia-box tarball location. The
// tarballs are published as GitHub Release assets so the download is stable
// regardless of repo visibility (raw.githubusercontent only works on public
// repos). Nodes download these instead of compiling Go (weak VPSes never build).
// Only amd64/arm64 are node architectures (see supportedNodeArchs).
var singBoxDownloadURLs = map[string]string{
	"amd64": "https://github.com/AlexeyLCP/angry-box/releases/download/v0.1.0/sing-box-" + singBoxVersion + "-amnezia-linux-amd64.tar.gz",
	"arm64": "https://github.com/AlexeyLCP/angry-box/releases/download/v0.1.0/sing-box-" + singBoxVersion + "-amnezia-linux-arm64.tar.gz",
}

// singBoxChecksums maps Go arch → sha256 of the amnezia-box tarball. Regenerate
// via scripts/build-singbox.sh (writes deps/checksums.txt). Verified on deploy
// (fail-closed via checksumForArch) so a truncated/modified tarball is never
// installed as root on the fleet. The arm64 checksum is pinned once the arm64
// tarball is built + published (scripts/build-singbox.sh ARCHES=arm64).
var singBoxChecksums = map[string]string{
	"amd64": "4272d04d9956b4b4f2fff2af5e50f836e728d34d6934bdd13cec36b2637a884a",
	"arm64": "fe965bee0c59b14887587e66dff08b470cdf445a4e4a0a76908ac359ac16a798",
}

// amneziaWGGoVersion is the SHORT SHA of the amneziawg-go commit pinned in
	// the amnezia-box fork's go.mod (hoaxisr/amneziawg-go/v3 @ ae4523c, module
	// path /v3, AWG 3.1). It is NOT a separate deploy artifact anymore —
// amneziawg-go is compiled INTO the sing-box binary (the `type:"awg"` endpoint
// runs it in-process via transport/awg). The pin is kept here as a
// traceability constant: the InputPackets API that transport/awg/port.go
// depends on lives at this commit, and a bump must update the fork's go.mod
// replace + this const + patchcheckAWGGORef together. See docs/PATCHES.md.
const amneziaWGGoVersion = "ae4523c"

var _ ports.Backend = (*Backend)(nil)

// Compile-time assert that *Backend also implements the optional ClientBackend
// capability (deploy/install over an already-open SSH client). CTO-review §8.
var _ ports.ClientBackend = (*Backend)(nil)

// DefaultConnector returns the production SSH connector. Exposed so the factory
// (which must not import the ssh package directly to avoid an import cycle)
// can fall back to it when no connector is injected.
func DefaultConnector() ports.SSHConnector { return sshclient.DefaultConnector }

// Backend manages sing-box proxy instances on remote hosts.
type Backend struct {
	connector ports.SSHConnector
}

// New creates a new sing-box Backend. If connector is nil, the production SSH
// connector (ssh.DefaultConnector) is used; tests inject a fake.
func New(connector ports.SSHConnector) *Backend {
	if connector == nil {
		connector = sshclient.DefaultConnector
	}
	return &Backend{connector: connector}
}

// priv wraps a command for privilege escalation. When useSudo is true (non-root
// SSH user with passwordless sudo configured), the command is prefixed with
// sudo. For multi-step privileged pipelines the caller should use sudoBash
// instead so sudo applies to the whole pipeline.
func priv(useSudo bool, cmd string) string {
	if useSudo {
		return "sudo " + cmd
	}
	return cmd
}

// sudoBash wraps a whole pipeline in `sudo bash -c '...'` so sudo applies to
// every command in the pipeline (not just the first one, which a bare `sudo a
// && b` would do). Only used when useSudo is true.
func sudoBash(useSudo bool, cmd string) string {
	if useSudo {
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	return cmd
}

// Deploy installs the PATCHED sing-box-extended on the remote host via SSH.
//
// Unlike the previous implementation, this:
//   - downloads our patched tarball (with sha256 verification, not empty)
//   - finds the binary via `find -maxdepth 2` (robust to archive layout)
//   - checks the installed binary at installPath explicitly (not PATH) so a
//     stale/different sing-box earlier on PATH can't fool the "already
//     installed" check and leave the systemd unit pointing at a missing binary
//   - always restarts the service after install (the old fast-path skipped
//     restart, which left a broken node broken forever)
//   - generates a self-signed TLS cert for TLS-based inbounds
//   - installs the AmneziaWG kernel module + awg-quick when requested
func (b *Backend) Deploy(ctx context.Context, host model.Host) (*model.DeployResult, error) {
	return b.DeployOpts(ctx, host, DeployOptions{})
}

// DeployOptions is an alias for model.DeployOptions so existing callers keep
// compiling while the canonical type lives in the domain layer (shared by all
// backends via the Backend.DeployWithOptions interface method).
type DeployOptions = model.DeployOptions

// DeployWithOptions implements ports.Backend and lets callers request sudo /
// AWG-module installation without type-asserting to *Backend (CTO-review H5).
func (b *Backend) DeployWithOptions(ctx context.Context, host model.Host, opts model.DeployOptions) (*model.DeployResult, error) {
	return b.DeployOpts(ctx, host, opts)
}

// DeployOpts is the options-aware variant of Deploy. It opens its own SSH
// connection and closes it on return. Use DeployOptsAndClient to reuse an
// already-open connection (the chain/merged deploy paths open one client per
// host and pass it to every backend method — collapsing what used to be 3 SSH
// dials per merged deploy into 1; CTO-review §8 follow-up).
func (b *Backend) DeployOpts(ctx context.Context, host model.Host, opts DeployOptions) (*model.DeployResult, error) {
	client, err := b.connector.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("singbox: deploy: %w", err)
	}
	defer client.Close()
	return b.deployOptsAndClient(ctx, host, opts, client)
}

// DeployOptsAndClient runs the same deploy sequence as DeployOpts but reuses an
// already-open SSH client. The caller owns the client's lifecycle (do NOT
// Close it here — the caller's defer does that). This is the seam that lets the
// chain/merged applier share one connection across Deploy + InstallAWG + the
// config push, instead of dialing three times (CTO-review §8).
func (b *Backend) DeployOptsAndClient(ctx context.Context, host model.Host, opts DeployOptions, client ports.SSHClient) (*model.DeployResult, error) {
	return b.deployOptsAndClient(ctx, host, opts, client)
}

// deployOptsAndClient is the actual deploy body. client is provided by the
// caller (either DeployOpts which dials+closes, or DeployOptsAndClient which
// reuses). It does NOT close the client.
func (b *Backend) deployOptsAndClient(ctx context.Context, host model.Host, opts DeployOptions, client ports.SSHClient) (*model.DeployResult, error) {
	sshHost := hostAddr(host.Addr)

	// 0. Ensure config dir + log dir + self-signed cert (best-effort, before
	// anything that might want the cert).
	if _, _, _, err := client.RunWithOutput(ctx, priv(opts.UseSudo, "mkdir -p "+configDir+" "+logDir), 60*time.Second); err != nil {
		return nil, fmt.Errorf("singbox: deploy: mkdir config/log dir: %w", err)
	}
	if err := ensureSelfSignedCert(ctx, client, sshHost, opts.UseSudo); err != nil {
		// Best-effort: cert may already exist or openssl is absent. Don't fail
		// the whole deploy — sing-box check will surface a missing cert later
		// only if a TLS inbound actually references it.
		_ = err
	}

	// 1. Install (or re-install) the patched binary. We check installPath
	// explicitly so a different sing-box on PATH can't short-circuit this.
	installedVer, _ := installedVersion(ctx, client, opts.UseSudo)
	if !isPatchedExtended(installedVer) {
		if err := installPatchedBinary(ctx, client, opts.UseSudo); err != nil {
			return nil, fmt.Errorf("singbox: deploy: install binary: %w", err)
		}
	}

	// 2. (Optional) AmneziaWG kernel module + awg-quick.
	if opts.InstallAWGModule {
		if err := b.installAWGModule(ctx, client, opts.UseSudo); err != nil {
			// Don't fail the whole deploy: AWG is optional and the node may
			// still serve non-AWG inbounds. Surface the error in the message.
			return &model.DeployResult{
				Success: true,
				Version: singBoxVersion,
				Message: fmt.Sprintf("sing-box %s installed, but AmneziaWG module setup failed: %v (non-AWG inbounds still work)", singBoxVersion, err),
			}, nil
		}
	}

	// 3. systemd unit.
	if err := writeSystemdUnit(ctx, client, opts.UseSudo); err != nil {
		return nil, fmt.Errorf("singbox: deploy: create systemd unit: %w", err)
	}

	// 3b. Ensure a minimal valid config.json exists so the service can start
	// (Deploy only installs the binary — Apply pushes the real config). Without
	// this, a fresh deploy starts sing-box with no config and it crashes. The
	// minimal config has empty inbounds/outbounds + info logging; Apply replaces
	// it on the first push.
	minimalCfg := `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`
	if opts.UseSudo {
		tmp := "/tmp/angry-min-config.json"
		if err := client.UploadText(ctx, minimalCfg, tmp, 0o644); err != nil {
			return nil, fmt.Errorf("singbox: deploy: write minimal config (tmp): %w", err)
		}
		if _, _, _, err := client.RunWithOutput(ctx,
			sudoBash(true, "cp "+tmp+" "+configFile+" && chmod 644 "+configFile+" && rm -f "+tmp), 30*time.Second); err != nil {
			return nil, fmt.Errorf("singbox: deploy: write minimal config: %w", err)
		}
	} else {
		_ = client.UploadText(ctx, minimalCfg, configFile, 0o644) // best-effort; may exist already
	}

	// 4. Reload + enable + restart. Always restart — the previous "already
	// installed" fast path skipped this and left broken nodes broken.
	// Wrap the whole pipeline in `sudo bash -c` so sudo applies to all three
	// systemctl calls (a bare `sudo a && b && c` only sudo's the first).
	restartCmd := sudoBash(opts.UseSudo, "systemctl daemon-reload && systemctl enable "+systemdService+" && systemctl reset-failed "+systemdService+" ; systemctl restart "+systemdService)
	if _, _, _, err := client.RunWithOutput(ctx, restartCmd, 60*time.Second); err != nil {
		return nil, fmt.Errorf("singbox: deploy: enable and start service: %w", err)
	}

	// 5. Real verification: wait for the service to actually come up, not just
	// `systemctl restart` returning 0. Capture journalctl on failure.
	if err := verifyServiceUp(ctx, client, systemdService, opts.UseSudo); err != nil {
		return &model.DeployResult{
			Success: false,
			Version: singBoxVersion,
			Message: fmt.Sprintf("sing-box installed but failed to come up: %v", err),
		}, fmt.Errorf("singbox: deploy: service did not become active: %w", err)
	}

	return &model.DeployResult{
		Success: true,
		Version: singBoxVersion,
		Message: fmt.Sprintf("sing-box-extended %s (patched) installed and started", singBoxVersion),
	}, nil
}

// installedVersion returns the version string reported by the binary at
// installPath (explicit, NOT PATH lookup) so a stale sing-box elsewhere on PATH
// cannot fool us.
func installedVersion(ctx context.Context, client ports.SSHClient, useSudo bool) (string, error) {
	cmd := priv(useSudo, installPath+" version 2>/dev/null || echo NOT_INSTALLED")
	out, _, _, err := client.RunWithOutput(ctx, cmd, 30*time.Second)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// isPatchedExtended returns true if the binary at installPath is already our
// patched extended build and does not need re-downloading.
//
// Detection strategy: our build is amnezia-box (our fork of sing-box 1.14
// alpha carrying the AWG3 userspace endpoint + mtproxy + fallback round-robin).
// It always carries the `with_awg` build tag (see scripts/build-singbox.sh),
// which stock sing-box and the old sing-box-extended do NOT compile in. The tag
// appears in `sing-box version` output's "Tags:" line regardless of whether
// version ldflags were set — so a binary built without ldflags (reporting
// "sing-box version unknown") is still recognized. with_awg is the most
// distinctive tag for our amnezia-box fork (the old canary tags
// with_trusttunnel/with_sudoku were sing-box-extended-only and are gone).
func isPatchedExtended(ver string) bool {
	if ver == "" || strings.Contains(ver, "NOT_INSTALLED") {
		return false
	}
	lower := strings.ToLower(ver)
	// with_awg is unique to amnezia-box (our base sing-box fork). with_mtproxy
	// is our port — also present, but with_awg is the strongest signal.
	return strings.Contains(lower, "with_awg") ||
		strings.Contains(lower, "with_mtproxy")
}

// supportedNodeArchs is the product contract for NODES: angry-box captures and
// manages nodes on amd64 and arm64 only. The PANEL (orchestrator) itself
// installs everywhere (incl. MIPS/armv7 routers — see the release targets), but
// a node must run the patched sing-box binary we publish, and we only publish
// amd64/arm64. Any other node arch is refused up front with a clear message
// instead of dying later on a missing binary/checksum.
var supportedNodeArchs = map[string]bool{
	"amd64": true,
	"arm64": true,
}

// checksumForArch returns the expected sha256 of the patched sing-box tarball
// for the given Go architecture. It fails closed: a missing or empty checksum
// yields an error rather than silently skipping verification, so a compromised
// release/mirror cannot be installed on an architecture we forgot to pin
// (CTO-review M1).
func checksumForArch(goArch string) (string, error) {
	sum := singBoxChecksums[goArch]
	if sum == "" {
		return "", fmt.Errorf("singbox: no pinned sha256 checksum for arch %q — refusing to install an unverified binary (pin it in singBoxChecksums)", goArch)
	}
	return sum, nil
}

// installPatchedBinary downloads our patched tarball, verifies its sha256,
// extracts the binary and installs it at installPath.
func installPatchedBinary(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	archOut, _, _, err := client.RunWithOutput(ctx, "uname -m", 30*time.Second)
	if err != nil {
		return fmt.Errorf("detect arch: %w", err)
	}
	goArch := archToGoArch(strings.TrimSpace(archOut))

	// Product contract: nodes are amd64/arm64 only. Refuse anything else up
	// front with a clear message (the panel itself installs everywhere, but a
	// node must run a patched sing-box build we publish — and we only publish
	// amd64/arm64).
	if !supportedNodeArchs[goArch] {
		return fmt.Errorf("angry-box manages nodes on amd64/arm64 only — this node is %q (%s)", strings.TrimSpace(archOut), goArch)
	}

	// Fail closed BEFORE downloading: if we have no pinned checksum for this
	// arch, refuse to install rather than shipping an unverified binary that
	// runs as root on the fleet (CTO-review M1).
	expectedChecksum, err := checksumForArch(goArch)
	if err != nil {
		return err
	}

	url := singBoxDownloadURLs[goArch]
	if url == "" {
		// Default to GitHub raw under deps/. Adjust the branch/path as needed.
		url = fmt.Sprintf("https://raw.githubusercontent.com/alexeylcp/angry-box/main/deps/sing-box-%s-patched-linux-%s.tar.gz",
			singBoxVersion, goArch)
	}
	// Operator override: point deploy at a mirror/local HTTP server (e.g. for
	// air-gapped installs or testing). Empty = use the default/registry URL.
	if envURL := os.Getenv("ANGRY_BINARY_URL"); envURL != "" {
		url = envURL
	}
	// Mirror fallback (P2c — GitHub release assets are a SPOF and can be
	// unreachable from RU VPS networks): comma-separated extra URLs in
	// ANGRY_BINARY_MIRRORS are tried in order after the primary. Every
	// candidate is verified against the pinned checksum, so a broken or
	// compromised mirror cannot install a bad binary.
	urls := []string{url}
	if mirrors := os.Getenv("ANGRY_BINARY_MIRRORS"); mirrors != "" {
		for _, m := range strings.Split(mirrors, ",") {
			if m = strings.TrimSpace(m); m != "" {
				urls = append(urls, m)
			}
		}
	}
	// Validate every URL before it reaches a root shell — the value is
	// interpolated into a single-quoted bash loop below; a single quote in an
	// operator-supplied URL would break out and run arbitrary commands as
	// root (same class as CodeRabbit M1 on the AWG tarball URL).
	for _, u := range urls {
		if err := validateTarballURL(u); err != nil {
			return fmt.Errorf("sing-box binary URL: %w", err)
		}
	}

	var quoted strings.Builder
	for i, u := range urls {
		if i > 0 {
			quoted.WriteByte(' ')
		}
		fmt.Fprintf(&quoted, "'%s'", u)
	}

	script := fmt.Sprintf(`set -e
mkdir -p /tmp/sing-box-install
cd /tmp/sing-box-install
ok=0
for u in %s; do
  if curl -fsSL --connect-timeout 15 "$u" -o sing-box.tar.gz && echo '%s  sing-box.tar.gz' | sha256sum -c -; then ok=1; break; fi
  echo "mirror failed: $u" >&2
done
if [ "$ok" != 1 ]; then echo 'ERROR: all sing-box download mirrors failed' >&2; exit 1; fi
`, quoted.String(), expectedChecksum)

	script += fmt.Sprintf(`tar -xzf sing-box.tar.gz
SINGBOX_BIN=$(find /tmp/sing-box-install -maxdepth 2 -name sing-box -type f 2>/dev/null | head -1)
if [ -z "$SINGBOX_BIN" ]; then echo 'ERROR: sing-box binary not found after tar' >&2; exit 1; fi
install -m 0755 "$SINGBOX_BIN" %s
rm -rf /tmp/sing-box-install
`, installPath)

	// Wrap the whole install pipeline in `sudo bash -c` when useSudo, so sudo
	// applies to every line (a bare `sudo set -e\nmkdir...` would run `sudo set`
	// as a binary lookup — "sudo: set: command not found"). The install step
	// itself needs sudo only for the final `install` to /usr/local/bin, but
	// running the whole script as root is simplest and the script is trusted.
	if _, stderr, exit, err := client.RunWithOutput(ctx, sudoBash(useSudo, script), 10*time.Minute); err != nil {
		return fmt.Errorf("download/install: %s (exit %d) %s", err, exit, stderr)
	}
	return nil
}

// (standalone amneziawg-go daemon deploy removed) — under the amnezia-box
// migration, userspace AWG runs as the sing-box `type:"awg"` endpoint IN-PROCESS
// (amneziawg-go compiled into the sing-box binary via the fork's go.mod
// replace). There is no separate amneziawg-go binary to download/install on
// nodes anymore. The old Stage-1 standalone-daemon path (amneziaWGGoInstallPath,
// amneziaWGGoDownloadURLs/Checksums, installAmneziaWGGoBinary) was removed when
// we switched to amnezia-box. amneziaWGGoVersion stays as a traceability const
// (the fork's amneziawg-go pin); it is NOT a deploy artifact.

// writeSystemdUnit writes the sing-box.service unit with the capabilities
// needed for TUN + privileged-port binding. No ExecReload (we use restart);
// RestartPreventExitStatus=23 stops the config-changed-restart sentinel from
// triggering a restart loop.
func writeSystemdUnit(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	unit := fmt.Sprintf(`[Unit]
Description=sing-box-extended proxy service
Documentation=https://sing-box.sagernet.org
# Order sing-box after the kernel AWG interface so that, at boot or on
# restart, awg0 (and its TUN overlay target) is up before sing-box tries to
# capture traffic from it. After= is an ordering hint only — it is NOT a
# Requires=/Wants=, so on non-AWG nodes where awg-quick@awg0 doesn't exist
# systemd simply ignores it (no start failure). Matches the dns.idoctor.mom
# reference (VPN/docs/server-dns-idoctor-mom.md).
After=network.target nss-lookup.target awg-quick@awg0.service

[Service]
User=root
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_OVERRIDE
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_OVERRIDE
NoNewPrivileges=true
ExecStart=%s run -c %s
Restart=on-failure
RestartPreventExitStatus=23
LimitNPROC=10000
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, installPath, configFile)

	if useSudo {
		// Write to $HOME first (writable without sudo), then sudo cp into place.
		tmp := "/tmp/sing-box.service"
		if err := client.UploadText(ctx, unit, tmp, 0o644); err != nil {
			return fmt.Errorf("upload unit: %w", err)
		}
		if _, _, _, err := client.RunWithOutput(ctx,
			"sudo cp "+tmp+" "+systemdUnit+" && rm -f "+tmp+" && sudo systemctl daemon-reload",
			60*time.Second); err != nil {
			return fmt.Errorf("install unit: %w", err)
		}
		return nil
	}
	if err := client.UploadText(ctx, unit, systemdUnit, 0o644); err != nil {
		return fmt.Errorf("upload unit: %w", err)
	}
	if _, _, _, err := client.RunWithOutput(ctx, "systemctl daemon-reload", 60*time.Second); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	return nil
}

// ensureSelfSignedCert generates a self-signed RSA 2048 cert (CN=sshHost) for
// TLS-based inbounds (TUIC/Hysteria2/VLESS/Trojan), if not already present.
// Best-effort: openssl may be absent or the dir not writable; callers tolerate
// failure and let `sing-box check` surface a missing cert only when needed.
func ensureSelfSignedCert(ctx context.Context, client ports.SSHClient, sshHost string, useSudo bool) error {
	certCmd := fmt.Sprintf(`test -f %s/cert.pem || (which openssl >/dev/null 2>&1 && \
openssl req -x509 -newkey rsa:2048 -keyout %s/key.pem \
-out %s/cert.pem -days 3650 -nodes -subj "/CN=%s" 2>/dev/null && \
chmod 644 %s/cert.pem %s/key.pem) \
|| echo 'cert-gen skipped'`,
		configDir, configDir, configDir, sshHost, configDir, configDir)
	stdout, stderr, exitCode, runErr := client.RunWithOutput(ctx, sudoBash(useSudo, certCmd), 60*time.Second)
	if runErr != nil {
		slog.Warn("singbox: self-signed cert generation command failed",
			"host", sshHost, "stdout", strings.TrimSpace(stdout), "stderr", strings.TrimSpace(stderr), "exit_code", exitCode, "err", runErr)
	}
	return nil
}

// verifyServiceUp waits up to ~8s for the unit to become active and captures
// journalctl output on failure so the operator sees WHY it didn't start.
func verifyServiceUp(ctx context.Context, client ports.SSHClient, service string, useSudo bool) error {
	// `systemctl is-active` may briefly be "activating" right after restart; a
	// short retry loop handles that without false-positives.
	check := sudoBash(useSudo, "sleep 3 && systemctl is-active --quiet "+service+" && echo UP || echo DOWN")
	for attempt := 0; attempt < 3; attempt++ {
		out, _, _, _ := client.RunWithOutput(ctx, check, 30*time.Second)
		if strings.TrimSpace(out) == "UP" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	// Failed — grab the last 30 log lines for diagnostics.
	journal, _, _, _ := client.RunWithOutput(ctx,
		sudoBash(useSudo, "journalctl -u "+service+" -n 30 --no-pager 2>/dev/null"),
		30*time.Second)
	tail := strings.TrimSpace(journal)
	if len(tail) > 1200 {
		tail = tail[len(tail)-1200:]
	}
	return fmt.Errorf("service not active; journal:\n%s", tail)
}

// InstallAWGModule installs the AmneziaWG kernel module + awg-quick on the host.
// This is the kernel path: AWG tunnels are owned by the kernel (awg-quick) and
// sing-box only does TUN + balancer with bind_interface — sidestepping the
// userspace gVisor AWG panic. Errors are surfaced (not silenced with 2>/dev/null).
func (b *Backend) InstallAWGModule(ctx context.Context, host model.Host) error {
	return b.InstallAWGModuleWithOptions(ctx, host, model.DeployOptions{})
}

// InstallAWGModuleWithOptions installs the AmneziaWG kernel module, wrapping
// privileged commands in sudo when opts.UseSudo is set (non-root sudoer VPS).
// Opens its own SSH connection. Use InstallAWGModuleWithClient to reuse an
// already-open connection (CTO-review §8: collapse 3 dials/merged-deploy → 1).
func (b *Backend) InstallAWGModuleWithOptions(ctx context.Context, host model.Host, opts model.DeployOptions) error {
	client, err := b.connector.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("singbox: InstallAWGModule: %w", err)
	}
	defer client.Close()
	return b.installAWGModule(ctx, client, opts.UseSudo)
}

// InstallAWGModuleWithClient runs the same install as InstallAWGModuleWithOptions
// but reuses an already-open SSH client. The caller owns the client's lifecycle.
func (b *Backend) InstallAWGModuleWithClient(ctx context.Context, opts model.DeployOptions, client ports.SSHClient) error {
	return b.installAWGModule(ctx, client, opts.UseSudo)
}

// awgKernelModuleLoaded reports whether the amneziawg kernel module is loaded.
func (b *Backend) awgKernelModuleLoaded(ctx context.Context, client ports.SSHClient, useSudo bool) bool {
	out, _, _, _ := client.RunWithOutput(ctx,
		sudoBash(useSudo, "lsmod 2>/dev/null | grep -q amneziawg && echo loaded || echo not_loaded"),
		30*time.Second)
	return strings.TrimSpace(out) == "loaded"
}

// awg3Capable reports whether the node can run AWG 3.0 (header protection) via
// the kernel path: the module exports awg_header_protection_set_key (PR #192)
// AND the awg tools parse HeaderProtectionKey (major >= 3). Mirrors
// chain.detectKernelAWG3 but is sudo-aware and local to the install path so the
// installer can decide "skip" vs "upgrade".
func (b *Backend) awg3Capable(ctx context.Context, client ports.SSHClient, useSudo bool) bool {
	sym, _, _, _ := client.RunWithOutput(ctx,
		sudoBash(useSudo, "grep -q awg_header_protection_set_key /proc/kallsyms && echo yes || echo no"),
		30*time.Second)
	if strings.TrimSpace(sym) != "yes" {
		return false
	}
	tools, _, _, _ := client.RunWithOutput(ctx,
		sudoBash(useSudo, "awg --version 2>/dev/null | head -1"),
		30*time.Second)
	return awgToolsMajorAtLeast3(tools)
}

// awgToolsMajorAtLeast3 parses an amneziawg-tools version line (e.g.
// "amneziawg-tools v3.0.20260730") and reports major >= 3. Empty/garbage → false.
func awgToolsMajorAtLeast3(line string) bool {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "amneziawg-tools")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	var n int
	got := false
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		got = true
		n = n*10 + int(r-'0')
	}
	return got && n >= 3
}

// validateTarballURL rejects anything that is not a clean http(s) URL and that
// contains a single quote (the shell-escape character that would break out of
// the curl argument in installAWGModule). Defense against operator-supplied
// ANGRY_AWG_TARBALL_URL being used for SSH command injection (CodeRabbit M1).
func validateTarballURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid ANGRY_AWG_TARBALL_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("ANGRY_AWG_TARBALL_URL must be http(s), got scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("ANGRY_AWG_TARBALL_URL has no host")
	}
	if strings.ContainsAny(raw, "'`\\$") {
		return fmt.Errorf("ANGRY_AWG_TARBALL_URL must not contain shell metacharacters (' ` \\ $)")
	}
	return nil
}

// installAWGModule installs the AmneziaWG kernel module + awg/awg-quick on
// Debian/Ubuntu hosts. Strategy:
//  1. Amnezia PPA (official path for Debian 12 / Ubuntu — package "amneziawg")
//  2. DKMS build from bundled tarball (ANGRY_AWG_TARBALL_URL or GitHub release)
//
// The old amneziawg-tools apt name and upstream install.sh URL are obsolete
// (package missing on Debian 12; install.sh returns 404 as of 2026).
func (b *Backend) installAWGModule(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	// Skip only when the node is ALREADY AWG3-capable (module exports the HPK
	// symbol AND tools parse HeaderProtectionKey). A node stuck on the v1 PPA
	// module is loaded but NOT capable, so it falls through to the upgrade path
	// below (lucx-ui: install is also the upgrade vehicle).
	if b.awgKernelModuleLoaded(ctx, client, useSudo) && b.awg3Capable(ctx, client, useSudo) {
		return b.persistAWGModules(ctx, client, useSudo)
	}

	awgTarballURL := os.Getenv("ANGRY_AWG_TARBALL_URL")
	if awgTarballURL == "" {
		// The AWG3-capable kernel-module source tarball (deps/amneziawg-src.tar.gz,
		// repacked from amnezia-vpn/amneziawg-linux-kernel-module master, PR #192).
		// Pinned to the binary's OWN release tag (version.Version) so each release
		// is self-contained — the v0.1.0 hardcode pointed at a stale tarball and
		// broke the DKMS-from-source fallback on nodes without the amnezia PPA.
		// The same tarball is mirrored to v0.1.0 for older binaries in the field.
		awgTarballURL = "https://github.com/AlexeyLCP/angry-box/releases/download/" + version.Version + "/amneziawg-src.tar.gz"
	}
	// Validate the URL before it reaches a root shell. The previous code
	// interpolated awgTarballURL into `sudo bash -c '... curl ... %s ...'`, so a
	// value containing a single quote (e.g. https://x'; rm -rf / ;') broke out
	// of the curl argument and ran arbitrary commands as root (CodeRabbit M1).
	// Reject anything that is not a clean http(s) URL with no single quote.
	if err := validateTarballURL(awgTarballURL); err != nil {
		return fmt.Errorf("amneziawg install: %w", err)
	}

	// Pass the URL to the remote shell via an exported env var inside the
	// script instead of string interpolation into a curl argument. The URL has
	// been validated by validateTarballURL (no single quotes / shell
	// metacharacters), so interpolating it into a single-quoted bash string is
	// safe here. This avoids the previous `curl '... %s ...'` pattern where a
	// crafted URL could break out of the quotes (CodeRabbit M1).
	cmd := sudoBash(useSudo, fmt.Sprintf(`set -e
export AB_AWG_URL='%s'
export DEBIAN_FRONTEND=noninteractive

# ─── Amnezia PPA key + sources BEFORE any apt-get update ───────────────────
# A stale/broken amnezia list (empty keyring, no signed-by, leftover from lucx/
# ihor/manual) makes the FIRST apt-get update exit 100 with NO_PUBKEY and aborts
# the whole install under set -e — DKMS fallback never runs. RU nodes also often
# cannot reach gpg keyservers. Fix: (1) always (re)write key via curl HTTP or
# embedded armored key, (2) always rewrite sources with signed-by, (3) only then
# apt-get update (non-fatal). Fingerprint 4166F2C257290828 = Launchpad PPA for
# Iurii Egorov (amnezia/ppa).
install -d -m 0755 /usr/share/keyrings /etc/apt/sources.list.d
AB_KEYRING=/usr/share/keyrings/amnezia-archive-keyring.gpg
AB_KEY_OK=0
# Prefer curl HTTP (works when hkps keyservers are firewalled in RU).
if command -v curl >/dev/null 2>&1; then
  for AB_KEY_URL in \
    "https://keyserver.ubuntu.com/pks/lookup?op=get&options=mr&search=0x4166F2C257290828" \
    "http://keyserver.ubuntu.com/pks/lookup?op=get&options=mr&search=0x4166F2C257290828" \
    "https://keys.openpgp.org/vks/v1/by-fingerprint/75C9DD72C799870E310542E24166F2C257290828"
  do
    if curl -fsSL --connect-timeout 8 --max-time 30 "$AB_KEY_URL" 2>/dev/null \
      | gpg --batch --yes --dearmor -o "$AB_KEYRING" 2>/dev/null \
      && [ -s "$AB_KEYRING" ]; then
      AB_KEY_OK=1
      echo "[awg] PPA key installed via curl ($AB_KEY_URL)"
      break
    fi
    rm -f "$AB_KEYRING"
  done
fi
# Embedded armored key — offline / keyserver-blocked fallback (same fingerprint).
if [ "$AB_KEY_OK" != "1" ]; then
  echo "[awg] Installing embedded Amnezia PPA key..."
  gpg --batch --yes --dearmor -o "$AB_KEYRING" <<'AB_PPA_KEY' 2>/dev/null || true
-----BEGIN PGP PUBLIC KEY BLOCK-----

xsFNBGV0UhsBEAC33rMndHSN/k+u7gcZbh9/FjgYfGltQAtVe2QDxzn7UV+k/ChX
OrYRw6Izw/DrhaapkNCThK2jwJE64e0NjboLH7UrrmSJLXMfOlDFbyGJVRA+1sTB
lo7kKHY0xiZ1CHDzjKNV3czbesu80A9nuTZYyWHEn9ax6wsqKG3N8SvzQkUrIOVD
2wZjh0p273CCEGkBnax1ghAV3MF8OrsPU6FRJ+ZakzKbu54g68xoV+2813YECme0
JKsWfUUe/1uEJOXCvuACURSxnYr0sihJd8QI/jHSGlfeq72e5MflFEOrnu5xaDSJ
r2W5lvUetG7EGSxtNKd7Jm/KhUV04g7arA0qydRjRToW3QqyzG7VB2nXKz3AOBYN
earWAuBcTkfPvRVchxbjiYonKZA5tIlVrpawMZsdxKvYwl6LVnpBcccFWPhpudfy
4TpCqCxRoAanOCvSirI3/y7TcZMBw643SaxXi1ifGeg6eyMzrLtP3CeonKBHGzrt
1eeKGtEw/PFN4RmwpBePxi+uj0CoTD6zjCQa3c8EeB4Qz7tt6PnpibxdtZE8sBdd
51wSA/fPGi2tFph8IVAsws7oxcQxZYl8CyncKDLcoR4dxVHYdFEDDf1GjRjoQ3Ai
nD7fxD5qYzExe50DBVpuUbWcAiGICNxfvzQtUSRRtMoSHDcvzsy03KC6VwARAQAB
zR5MYXVuY2hwYWQgUFBBIGZvciBJdXJpaSBFZ29yb3bCwY4EEwEKADgWIQR1yd1y
x5mHDjEFQuJBZvLCVykIKAUCZXRSGwIbAwULCQgHAgYVCgkICwIEFgIDAQIeAQIX
gAAKCRBBZvLCVykIKBu5D/9akmHCHlUqm2RTTBeTMbLNGc0l6YugpPaCM6vz0O9k
BFP5PfRaNSRzyF7wHFHNY3JUHcor28my1fD8AE4+C3PwXz8tVYLh57UUsp4wjqHY
+MTl/1ngDViPGD3PRjB8ZlO+19yerfplZv1Jaw7FZZv2BZOAXb+ddqUG4EmlzOnC
EhcSDdFrzEBB3RGthjIb3QkKWKGbELDiMfogmsO9BE139Raiw23blagDrbnWsG4j
ReZeu3atjG6AW8eL7m+i7bKKshD2CYVMznI5cYGLMKo9w7sb33uylPj1Vx9O7joP
2GFf2rTpCY8wgzk7i1RqsipJ80u1/DY91Xdizv3f2BBe6UY7qHKoK00O11J0y8yU
is2Asycy33Wy51pf6rCFUBLQu+c1fEypHF6jqANmQwaH7pPBliy4gGWvrVggzV4m
xv7SnRiMi4PFyVwjKWm8dmuMxi/B9s++VG/ed+5aYgJYL58MohG3MUI/L58eitSC
DDcQ1iAnBmawnGMKPqzMgRFB3OU3wDwfh7LNVvQqWpQ4q7pr4Cq1CvZvGoggXWDo
1/vylPsRmiiuNetfsoVYmrkgtj1om07m5Xp1v4SyXJH11c3dc/xfMmn/4RlMWIpq
86IsOjpr3avsw3FVUNCgD5Wf5+rHG+7gNmM6Cm/F8MDfAnnRmsw4h6hgvcJNQT5D
ig==
=OY2W
-----END PGP PUBLIC KEY BLOCK-----
AB_PPA_KEY
  if [ -s "$AB_KEYRING" ]; then AB_KEY_OK=1; fi
fi
# Last resort: classic gpg keyserver (often blocked in RU).
if [ "$AB_KEY_OK" != "1" ]; then
  gpg --batch --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys 4166F2C257290828 2>/dev/null \
    || gpg --batch --keyserver hkps://keyserver.ubuntu.com --recv-keys 4166F2C257290828 2>/dev/null \
    || true
  gpg --batch --export 4166F2C257290828 2>/dev/null | gpg --batch --yes --dearmor -o "$AB_KEYRING" 2>/dev/null || true
  if [ -s "$AB_KEYRING" ]; then AB_KEY_OK=1; fi
fi
chmod 0644 "$AB_KEYRING" 2>/dev/null || true

# Drop legacy broken lists (no signed-by / empty keyring / old amnezia.gpg name).
rm -f /etc/apt/sources.list.d/amnezia-ppa.list \
      /etc/apt/sources.list.d/amnezia-ubuntu-ppa-*.list \
      /etc/apt/sources.list.d/amnezia*.list 2>/dev/null || true
# Also strip any amnezia lines from sources.list itself.
if [ -f /etc/apt/sources.list ]; then
  sed -i '/ppa.launchpadcontent.net\/amnezia/d;/ppa.launchpad.net\/amnezia/d' /etc/apt/sources.list 2>/dev/null || true
fi

. /etc/os-release 2>/dev/null || true
AB_CODENAME="${VERSION_CODENAME:-focal}"
# Amnezia PPA publishes Ubuntu series only; map Debian codenames → nearest Ubuntu.
case "$AB_CODENAME" in
  bookworm|trixie|sid|testing|stable) AB_CODENAME=jammy ;;
  noble|jammy|focal|mantic|lunar|kinetic) ;;
  *) AB_CODENAME=jammy ;;
esac
echo "[awg] distro codename for PPA: $AB_CODENAME (key_ok=$AB_KEY_OK)"
if [ "$AB_KEY_OK" = "1" ]; then
  echo "deb [signed-by=$AB_KEYRING] https://ppa.launchpadcontent.net/amnezia/ppa/ubuntu $AB_CODENAME main" \
    > /etc/apt/sources.list.d/amnezia-ppa.list
else
  echo "[awg] WARNING: could not install PPA key — skipping PPA (DKMS fallback only)"
fi

echo "[awg] Installing build prerequisites..."
# Non-fatal: a flaky mirror must not abort before DKMS.
apt-get update -qq || echo "[awg] WARNING: apt-get update failed (continuing)"
# git/libmnl-dev/pkg-config are needed to build amneziawg-tools from source
# (the AWG3 userspace tools) when the PPA tools are missing or < v3.
apt-get install -y -qq dkms build-essential linux-headers-$(uname -r) gnupg2 curl git libmnl-dev pkg-config \
  || echo "[awg] WARNING: prereq apt install failed (continuing to DKMS)"
# Debian 13+ prerequisites (LucX-UI install-awg-module.sh lesson, verified on
# our n1/n2): iptables is no longer installed by default there — our server
# confs' PostUp lines (MASQUERADE/FORWARD) use the iptables shim, without it
# awg-quick up fails (exit 127) and rolls the interface back. openresolv is
# called by awg-quick when a conf carries DNS= (client confs). nftables is
# required for sing-box TUN auto_redirect (AB_AWG_AUTO_REDIRECT=1).
apt-get install -y -qq iptables nftables openresolv || echo "[awg] WARNING: iptables/nftables/openresolv install failed (continuing)"

echo "[awg] Installing amneziawg from PPA (baseline tools)..."
apt-get install -y -qq amneziawg || echo "[awg] PPA install failed (continuing to DKMS)"

	# Ensure an AWG3-capable KERNEL MODULE. The PPA ships the v1 module (no HPK
	# symbol); the bundled tarball is master (PR #192, AWG3-capable). If the
	# loaded/installed module lacks the HPK symbol, build the bundled source via
	# DKMS — this is the v1 -> v3 upgrade path (lucx-ui --force-rebuild lesson).
	if ! grep -q awg_header_protection_set_key /proc/kallsyms 2>/dev/null; then
		echo "[awg] module lacks AWG3 HPK — building bundled AWG3 source via DKMS..."
		rm -rf /tmp/awg-src && mkdir -p /tmp/awg-src
		curl -fsSL "$AB_AWG_URL" -o /tmp/awg-src.tar.gz
		tar -xzf /tmp/awg-src.tar.gz -C /tmp/awg-src --strip-components=1
		# Upstream stamps PACKAGE_VERSION/WIREGUARD_VERSION=1.0.0 into every build,
		# so modinfo would report 1.0.0 even for an AWG3 build (lucx-ui c3001499
		# found the version-parse probe broken by exactly this). Rewrite it so the
		# registered DKMS version (and modinfo) reflect AWG3.
		sed -i 's/^PACKAGE_VERSION=.*/PACKAGE_VERSION="3.0-awg3"/' /tmp/awg-src/dkms.conf 2>/dev/null || true
		AB_AWG_MODVER="3.0-awg3"
		echo "[awg] DKMS module version: $AB_AWG_MODVER"
		rm -rf "/usr/src/amneziawg-$AB_AWG_MODVER"
		cp -r /tmp/awg-src "/usr/src/amneziawg-$AB_AWG_MODVER"
		dkms add -m amneziawg -v "$AB_AWG_MODVER" || true
		dkms build -m amneziawg -v "$AB_AWG_MODVER"
		dkms install -m amneziawg -v "$AB_AWG_MODVER"
		rmmod amneziawg 2>/dev/null || true
		modprobe amneziawg
		rm -rf /tmp/awg-src /tmp/awg-src.tar.gz
	fi

	# Ensure AWG3-capable TOOLS (awg/awg-quick must parse HeaderProtectionKey).
	# If the installed awg is missing or < v3, build amneziawg-tools from
	# upstream master (lucx-ui install-awg-module.sh tools step).
	TOOLS_MAJOR="$(awg version 2>/dev/null | grep -oE '[0-9]+' | head -1)"
	TOOLS_MAJOR="${TOOLS_MAJOR:-0}"
	if [ "$TOOLS_MAJOR" -lt 3 ]; then
		echo "[awg] tools v$TOOLS_MAJOR lack HPK — building amneziawg-tools from master..."
		rm -rf /tmp/awg-tools
		if git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-tools.git /tmp/awg-tools 2>/dev/null; then
			( cd /tmp/awg-tools/src && make && make install ) || echo "[awg] WARNING: amneziawg-tools build failed (continuing)"
		else
			echo "[awg] WARNING: could not clone amneziawg-tools (offline?)"
		fi
		rm -rf /tmp/awg-tools
	fi
`, awgTarballURL))
	if _, stderr, exit, err := client.RunWithOutput(ctx, cmd, 15*time.Minute); err != nil {
		return fmt.Errorf("amneziawg install failed (exit %d): %s %s", exit, err, stderr)
	}

	if err := b.persistAWGModules(ctx, client, useSudo); err != nil {
		return err
	}

	if !b.awgKernelModuleLoaded(ctx, client, useSudo) {
		return fmt.Errorf("amneziawg module not loaded after install (check kernel headers / dkms log)")
	}
	awgQuick, _, _, _ := client.RunWithOutput(ctx, sudoBash(useSudo, "command -v awg-quick 2>/dev/null || echo missing"), 30*time.Second)
	if strings.TrimSpace(awgQuick) == "missing" {
		return fmt.Errorf("awg-quick not found after amneziawg install")
	}
	return nil
}

// persistAWGModules writes modules-load.d entries and modprobes dependencies.
func (b *Backend) persistAWGModules(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	persist := sudoBash(useSudo, `echo amneziawg > /etc/modules-load.d/amneziawg.conf
cat > /etc/modules-load.d/awg-deps.conf << 'EOF'
udp_tunnel
ip6_udp_tunnel
curve25519-x86_64
libcurve25519-generic
EOF
modprobe udp_tunnel 2>/dev/null || true
modprobe ip6_udp_tunnel 2>/dev/null || true
modprobe curve25519-x86_64 2>/dev/null || true
modprobe libcurve25519-generic 2>/dev/null || true
modprobe amneziawg 2>/dev/null || true
`)
	if _, _, _, err := client.RunWithOutput(ctx, persist, 60*time.Second); err != nil {
		return fmt.Errorf("persist awg modules: %w", err)
	}
	return nil
}

// ─── legacy Backup helpers (used by ApplyConfig; pushConfig in applier.go has
// its own correct copy). Kept for the Backend.ApplyConfig path. ───────────────

// createBackup makes a timestamped backup of the current config.
func createBackup(client ports.SSHClient, file string) (string, error) {
	uniqueID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), randInt())
	out, err := client.Run(fmt.Sprintf(`if [ -f %s ]; then
		bak="%s.bak.%s"
		cp -p %s "$bak"
		echo "$bak"
	fi`, file, file, uniqueID, file))
	return strings.TrimSpace(out), err
}

func randInt() int {
	b := make([]byte, 4)
	rand.Read(b)
	return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
}

// performRollback restores the backup via cp (NOT mv) so the backup is
// preserved for a second recovery attempt, then restarts the service.
func performRollback(client ports.SSHClient, file, backupPath, serviceName string) error {
	if backupPath == "" {
		return fmt.Errorf("no backup path provided")
	}
	cmd := fmt.Sprintf(`test -f %s && cp %s %s; systemctl restart %s; sleep 2; systemctl is-active --quiet %s`,
		backupPath, backupPath, file, serviceName, serviceName)
	_, err := client.Run(cmd)
	if err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}
	return nil
}

// cleanupBackups keeps only the last 5 backups.
func cleanupBackups(client ports.SSHClient, file string) {
	client.Run(fmt.Sprintf(`ls -t %s.bak.* 2>/dev/null | tail -n +6 | xargs rm -f 2>/dev/null || true`, file))
}

// ApplyConfig generates a config and pushes it to the remote host with the
// reliable deploy sequence (backup → upload → check → rollback on check-fail →
// restart → health-probe). See chain.pushConfig for the chain-path equivalent.
func (b *Backend) ApplyConfig(ctx context.Context, host model.Host, cfgType model.ConfigType, params model.ConfigParams) error {
	cfg, err := b.GenerateConfig(cfgType, params)
	if err != nil {
		return fmt.Errorf("singbox: applyConfig: %w", err)
	}

	client, err := b.connector.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("singbox: applyConfig: %w", err)
	}
	defer client.Close()

	// Validate JSON structure before touching remote.
	var js bytesJS
	if err := js.Unmarshal([]byte(cfg.Content)); err != nil {
		return fmt.Errorf("singbox: applyConfig: invalid JSON: %w", err)
	}

	// 1. Backup (best-effort, never blocks).
	backupPath, _ := createBackup(client, configFile)

	// 2. Upload via stdin cat (no heredoc).
	if err := client.UploadText(ctx, cfg.Content, configFile, 0o644); err != nil {
		return fmt.Errorf("singbox: applyConfig: write config: %w", err)
	}

	// 3. sing-box check — capture both streams.
	checkCmd := fmt.Sprintf("%s check -c %s", installPath, configFile)
	_, stderr, exit, err := client.RunWithOutput(ctx, checkCmd, 60*time.Second)
	if err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, systemdService)
			if rbErr != nil {
				// Rollback failed → node left in a broken state → not retry-able.
				return fmt.Errorf("singbox: applyConfig: check failed (exit %d): %s | AND rollback failed: %v: %w", exit, stderr, rbErr, chain.ErrRollbackFailed)
			}
			// Rollback succeeded → node still running old config → retry-able.
			return fmt.Errorf("singbox: applyConfig: rolled back — check failed (exit %d): %s: %w", exit, stderr, chain.ErrDeployFailed)
		}
		// No backup (first deploy) → nothing to roll back to → deploy-failed.
		return fmt.Errorf("singbox: applyConfig: check failed (exit %d, no backup): %s: %w", exit, stderr, chain.ErrDeployFailed)
	}

	// 4. Restart.
	if _, _, _, err := client.RunWithOutput(ctx, "systemctl restart "+systemdService, 60*time.Second); err != nil {
		if backupPath != "" {
			rbErr := performRollback(client, configFile, backupPath, systemdService)
			if rbErr != nil {
				return fmt.Errorf("singbox: applyConfig: restart failed: %v | AND rollback failed: %w", err, chain.ErrRollbackFailed)
			}
			return fmt.Errorf("singbox: applyConfig: rolled back — restart failed: %v: %w", err, chain.ErrDeployFailed)
		}
		return fmt.Errorf("singbox: applyConfig: restart failed (no backup): %v: %w", err, chain.ErrDeployFailed)
	}

	// 5. Health-probe (real, not just restart exit 0).
	if err := verifyServiceUp(ctx, client, systemdService, false); err != nil {
		if backupPath != "" {
			if rbErr := performRollback(client, configFile, backupPath, systemdService); rbErr != nil {
				slog.Error("singbox: applyConfig: health-probe rollback also failed",
					"file", configFile, "backup", backupPath, "err", rbErr)
				return fmt.Errorf("singbox: applyConfig: service not active after restart: %v | AND rollback failed: %v: %w", err, rbErr, chain.ErrRollbackFailed)
			}
		}
		return fmt.Errorf("singbox: applyConfig: service not active after restart: %v: %w", err, chain.ErrDeployFailed)
	}

	cleanupBackups(client, configFile)
	return nil
}

// Remove stops the service and removes all installed files from the remote host.
func (b *Backend) Remove(ctx context.Context, host model.Host) error {
	client, err := b.connector.Connect(host.Addr, host.User, host.KeyPath)
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
find /etc/sing-box -name 'config.json.bak.*' -mtime +3 -delete 2>/dev/null || true
`

	if _, _, _, err := client.RunWithOutput(ctx, script, 60*time.Second); err != nil {
		return fmt.Errorf("singbox: remove: %w", err)
	}

	return nil
}

// GetStatus retrieves the sing-box process status from the remote host.
func (b *Backend) GetStatus(ctx context.Context, host model.Host) (*model.Status, error) {
	client, err := b.connector.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("singbox: getStatus: %w", err)
	}
	defer client.Close()

	output, _, _, _ := client.RunWithOutput(ctx, "systemctl is-active sing-box 2>/dev/null || echo unknown", 30*time.Second)

	status := &model.Status{
		Running: strings.TrimSpace(output) == "active",
	}

	if verOut, _, _, err := client.RunWithOutput(ctx, installPath+" version 2>/dev/null | head -1 || echo NONE", 30*time.Second); err == nil {
		ver := strings.TrimSpace(verOut)
		ver = strings.TrimPrefix(ver, "sing-box version ")
		if idx := strings.Index(ver, "\n"); idx > 0 {
			ver = ver[:idx]
		}
		status.Version = ver
	}

	if pidOut, _, _, err := client.RunWithOutput(ctx, "systemctl show sing-box --property=MainPID --value 2>/dev/null || echo 0", 30*time.Second); err == nil {
		fmt.Sscanf(strings.TrimSpace(pidOut), "%d", &status.PID)
	}

	if status.Running {
		if uptimeOut, _, _, err := client.RunWithOutput(ctx, "systemctl show sing-box --property=ActiveEnterTimestamp --value 2>/dev/null || echo ''", 30*time.Second); err == nil {
			status.Uptime = strings.TrimSpace(uptimeOut)
		}
	}

	// OS — distro pretty name.
	if osOut, _, _, err := client.RunWithOutput(ctx,
		`. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME" || (lsb_release -ds 2>/dev/null || echo "")`,
		30*time.Second); err == nil {
		status.OS = strings.TrimSpace(strings.Trim(osOut, "\""))
	}

	// SingBoxInstalled — binary present, independent of Running.
	if sbOut, _, _, err := client.RunWithOutput(ctx,
		"command -v sing-box >/dev/null 2>&1 && echo yes || echo no", 30*time.Second); err == nil {
		status.SingBoxInstalled = strings.TrimSpace(sbOut) == "yes"
	}

	// AWGModuleInstalled — kernel module currently loaded.
	status.AWGModuleInstalled = b.awgKernelModuleLoaded(ctx, client, false)

	return status, nil
}

// Reload sends a graceful reload signal to sing-box on the remote host.
// Validates config first (refuses reload if invalid).
func (b *Backend) Reload(ctx context.Context, host model.Host) error {
	client, err := b.connector.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return fmt.Errorf("singbox: reload: %w", err)
	}
	defer client.Close()

	checkCmd := fmt.Sprintf("%s check -c %s", installPath, configFile)
	if _, stderr, exit, err := client.RunWithOutput(ctx, checkCmd, 60*time.Second); err != nil {
		return fmt.Errorf("singbox: reload: refusing reload, config invalid (exit %d): %s", exit, stderr)
	}

	// No ExecReload in our unit, so fall back to HUP.
	if _, _, _, err := client.RunWithOutput(ctx, "systemctl kill -s HUP sing-box 2>/dev/null || systemctl restart sing-box", 60*time.Second); err != nil {
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
	default:
		return arch
	}
}

// hostAddr returns the host part of addr (strips :port) for use as a cert CN.
func hostAddr(addr string) string {
	if h, _, err := splitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func splitHostPort(addr string) (string, string, error) {
	// net.SplitHostPort handles bracketed IPv6 literals ([2001:db8::1]:51820)
	// correctly, stripping the brackets and splitting on the port colon. The
	// previous last-':' heuristic mis-split IPv6 addresses (CTO-review H8).
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, "", err
	}
	return host, port, nil
}

// bytesJS is a thin json.RawMessage alias to avoid importing encoding/json here
// just for a validity check.
type bytesJS []byte

func (b *bytesJS) Unmarshal(data []byte) error {
	// Minimal brace/quote validation: real validation is done by `sing-box
	// check` on the remote. Here we just ensure it starts/ends as JSON.
	s := strings.TrimSpace(string(data))
	if len(s) == 0 || (s[0] != '{' && s[0] != '[') {
		return fmt.Errorf("not a JSON object/array")
	}
	*b = bytesJS(s)
	return nil
}
