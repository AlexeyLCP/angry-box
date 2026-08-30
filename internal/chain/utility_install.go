package chain

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// ─── Utility installation over SSH ───────────────────────────────────────────
// Same deployment discipline as the sing-box backend: weak nodes never build
// anything (prebuilt caddy binary from GitHub release assets, sha256-verified),
// sudo handled per command, every step verified before the next.

// caddyBuildVersion tags the xcaddy build published to the release assets
// (scripts/build-caddy.sh: caddy + caddyserver/layer4). Bump together with
// the asset + checksum.
const caddyBuildVersion = "2.9.1-layer4"

var caddyDownloadURLs = map[string]string{
	"amd64": "https://github.com/AlexeyLCP/angry-box/releases/download/v0.1.0/caddy-" + caddyBuildVersion + "-linux-amd64.tar.gz",
	"arm64": "",
}

// caddyChecksums pins sha256 per arch (fail-closed like the sing-box binary —
// an unpinned arch refuses to install rather than running an unverified root
// binary). Regenerate via scripts/build-caddy.sh.
var caddyChecksums = map[string]string{
	"amd64": "", // TODO(build): publish the asset + pin the checksum
	"arm64": "",
}

func utilPriv(useSudo bool, cmd string) string {
	if useSudo {
		return "sudo " + cmd
	}
	return cmd
}

func utilSudoBash(useSudo bool, cmd string) string {
	if useSudo {
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}
	return cmd
}

// UtilityReport is the human-readable outcome of a utility operation (rule #6:
// the UI must show what happened on the node).
type UtilityReport struct {
	Steps    []string
	Warnings []string
	version  string
}

func (r *UtilityReport) add(f string, a ...any)  { r.Steps = append(r.Steps, fmt.Sprintf(f, a...)) }
func (r *UtilityReport) warn(f string, a ...any) { r.Warnings = append(r.Warnings, fmt.Sprintf(f, a...)) }

// AddStep records a pre-formatted step line (the web layer passes already
// translated strings).
func (r *UtilityReport) AddStep(s string) { r.Steps = append(r.Steps, s) }

// AddSkip records an already-installed utility (idempotent re-runs).
func (r *UtilityReport) AddSkip(name string) { r.add("%s: already installed, skipped", name) }

// SetVersion/LastVersion carry the installed binary version back to the caller
// for the utility state record.
func (r *UtilityReport) SetVersion(v string) { r.version = v }
func (r *UtilityReport) LastVersion() string { return r.version }

// InstallCaddy installs the layer4-enabled caddy binary + systemd unit on the
// node. Idempotent: a present binary is kept (version reported).
func InstallCaddy(ctx context.Context, client ports.SSHClient, useSudo bool, rep *UtilityReport) error {
	if out, _, _, err := client.RunWithOutput(ctx, utilPriv(useSudo, "test -x "+CaddyBin+" && "+CaddyBin+" version 2>/dev/null || echo NOT_INSTALLED"), 30*time.Second); err == nil && !strings.Contains(out, "NOT_INSTALLED") {
		rep.add("caddy already installed: %s", strings.TrimSpace(firstLine(out)))
		rep.SetVersion(strings.TrimSpace(firstLine(out)))
		return nil
	}

	archOut, _, _, err := client.RunWithOutput(ctx, "uname -m", 30*time.Second)
	if err != nil {
		return fmt.Errorf("detect arch: %w", err)
	}
	goArch := utilArchToGo(strings.TrimSpace(archOut))

	checksum := caddyChecksums[goArch]
	if envSum := os.Getenv("ANGRY_CADDY_CHECKSUM"); envSum != "" {
		checksum = envSum
	}
	if !validSHA256Hex(checksum) {
		return fmt.Errorf("caddy: no pinned sha256 for arch %s — publish the release asset (scripts/build-caddy.sh) and pin caddyChecksums, or set ANGRY_CADDY_CHECKSUM", goArch)
	}
	dlURL := caddyDownloadURLs[goArch]
	if envURL := os.Getenv("ANGRY_CADDY_URL"); envURL != "" {
		dlURL = envURL
	}
	if dlURL == "" {
		return fmt.Errorf("caddy: no download URL for arch %s", goArch)
	}
	if err := utilValidateURL(dlURL); err != nil {
		return err
	}

	script := fmt.Sprintf(`set -e
mkdir -p /tmp/ab-caddy-install %s
cd /tmp/ab-caddy-install
if ! curl -fsSL --connect-timeout 15 '%s' -o caddy.tar.gz; then echo 'ERROR: caddy download failed' >&2; exit 1; fi
echo '%s  caddy.tar.gz' | sha256sum -c -
tar -xzf caddy.tar.gz
BIN=$(find /tmp/ab-caddy-install -maxdepth 2 -name caddy -type f 2>/dev/null | head -1)
if [ -z "$BIN" ]; then echo 'ERROR: caddy binary not found after tar' >&2; exit 1; fi
install -m 0755 "$BIN" %s
rm -rf /tmp/ab-caddy-install
`, CaddyDir, dlURL, checksum, CaddyBin)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, script), 10*time.Minute); err != nil {
		return fmt.Errorf("caddy download/install: %s (exit %d) %s", err, exit, stderr)
	}
	rep.add("caddy binary installed (%s)", caddyBuildVersion)
	rep.SetVersion(caddyBuildVersion)

	unit := `[Unit]
Description=angry-box caddy (spinal cord: SNI router + site + subscriptions)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=` + CaddyBin + ` run --config ` + Caddyfile + `
ExecReload=` + CaddyBin + ` reload --config ` + Caddyfile + ` --force
Restart=on-failure
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`
	if err := utilUploadPriv(ctx, client, useSudo, unit, CaddyUnit, 0o644); err != nil {
		return fmt.Errorf("caddy systemd unit: %w", err)
	}
	if _, _, _, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "systemctl daemon-reload && systemctl enable "+CaddyService), 60*time.Second); err != nil {
		return fmt.Errorf("caddy enable: %w", err)
	}
	rep.add("systemd unit %s installed + enabled", CaddyService)
	return nil
}

// PushFakesite writes the camouflage site's index page (operator content or
// DefaultFakesite) into the webroot.
func PushFakesite(ctx context.Context, client ports.SSHClient, useSudo bool, content string, rep *UtilityReport) error {
	if strings.TrimSpace(content) == "" {
		content = DefaultFakesite
	}
	if _, _, _, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "mkdir -p "+SiteDir), 30*time.Second); err != nil {
		return fmt.Errorf("fakesite mkdir: %w", err)
	}
	if err := utilUploadPriv(ctx, client, useSudo, content, SiteDir+"/index.html", 0o644); err != nil {
		return fmt.Errorf("fakesite upload: %w", err)
	}
	rep.add("fakesite index.html pushed to %s", SiteDir)
	return nil
}

// EnsureSubDir creates the subscription statics directory (the per-user files
// are pushed by the apply pipeline; this just prepares the mount point).
func EnsureSubDir(ctx context.Context, client ports.SSHClient, useSudo bool, rep *UtilityReport) error {
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "mkdir -p "+SubDir), 30*time.Second); err != nil {
		return fmt.Errorf("sub mkdir: %s (exit %d) %s", err, exit, stderr)
	}
	rep.add("subscription dir %s ready", SubDir)
	return nil
}

// ClearSubStatics removes all pushed subscription files (a fresh push always
// re-renders the full set — this is how deleted/revoked users disappear).
func ClearSubStatics(ctx context.Context, client ports.SSHClient, useSudo bool) error {
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "rm -f "+SubDir+"/* 2>/dev/null; mkdir -p "+SubDir), 30*time.Second); err != nil {
		return fmt.Errorf("sub clear: %s (exit %d) %s", err, exit, stderr)
	}
	return nil
}

// PushSubStatic writes one per-user subscription static file into SubDir.
// name is the bare file name (<token>.b64 etc.) — validated against traversal.
func PushSubStatic(ctx context.Context, client ports.SSHClient, useSudo bool, name, content string) error {
	if strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("sub static: unsafe file name %q", name)
	}
	if _, _, _, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "mkdir -p "+SubDir), 30*time.Second); err != nil {
		return fmt.Errorf("sub mkdir: %w", err)
	}
	return utilUploadPriv(ctx, client, useSudo, content, SubDir+"/"+name, 0o644)
}

// BootstrapCert ensures a cert/key pair exists at the node's cert paths so
// caddy can start BEFORE acme issues the real one. No-op when files exist.
func BootstrapCert(ctx context.Context, client ports.SSHClient, useSudo bool, domain string, rep *UtilityReport) error {
	if !ValidTLSDomain(domain) {
		return fmt.Errorf("bootstrap cert: invalid domain %q", domain)
	}
	cert, key := CertPaths(domain)
	script := fmt.Sprintf(`set -e
if [ -f %s ] && [ -f %s ]; then exit 0; fi
mkdir -p %s/%s
openssl req -x509 -nodes -newkey rsa:2048 -days 30 \
  -keyout %s -out %s \
  -subj "/CN=%s" -addext "subjectAltName=DNS:%s,DNS:panel.%s" >/dev/null 2>&1
chmod 600 %s
echo BOOTSTRAPPED
`, cert, key, CertRoot, domain, key, cert, domain, domain, domain, key)
	out, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, script), 60*time.Second)
	if err != nil {
		return fmt.Errorf("bootstrap cert: %s (exit %d) %s", err, exit, stderr)
	}
	if strings.Contains(out, "BOOTSTRAPPED") {
		rep.warn("using a SELF-SIGNED bootstrap certificate for %s until ACME issues the real one (check DNS A-records)", domain)
	}
	return nil
}

// PushCaddyfile uploads the rendered Caddyfile, validates it with the installed
// caddy binary and (re)starts the service. A failed validation keeps the
// previous file in place (no partial cutover — rule #7 discipline).
func PushCaddyfile(ctx context.Context, client ports.SSHClient, useSudo bool, content string, rep *UtilityReport) error {
	tmp := "/tmp/ab-caddyfile.new"
	if err := utilUploadPriv(ctx, client, useSudo, content, tmp, 0o644); err != nil {
		return fmt.Errorf("caddyfile upload: %w", err)
	}
	check := fmt.Sprintf("%s validate --config %s --adapter caddyfile", CaddyBin, tmp)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, check), 60*time.Second); err != nil {
		return fmt.Errorf("caddyfile validate failed (exit %d): %s", exit, stderr)
	}
	mv := fmt.Sprintf("mkdir -p %s && cp %s %s.bak 2>/dev/null || true; mv %s %s && chmod 644 %s",
		CaddyDir, Caddyfile, Caddyfile, tmp, Caddyfile, Caddyfile)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, mv), 30*time.Second); err != nil {
		return fmt.Errorf("caddyfile install: %s (exit %d) %s", err, exit, stderr)
	}
	up := fmt.Sprintf("systemctl reset-failed %s 2>/dev/null; systemctl reload-or-restart %s && sleep 1 && systemctl is-active --quiet %s",
		CaddyService, CaddyService, CaddyService)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, up), 90*time.Second); err != nil {
		return fmt.Errorf("caddy service did not come up: %s (exit %d) %s", err, exit, stderr)
	}
	rep.add("Caddyfile validated + applied, %s is active", CaddyService)
	return nil
}

// InstallAcme installs acme.sh under the privileged user (idempotent).
func InstallAcme(ctx context.Context, client ports.SSHClient, useSudo bool, rep *UtilityReport) error {
	if out, _, _, err := client.RunWithOutput(ctx, utilPriv(useSudo, "test -x "+AcmeBin+" && echo OK || echo MISSING"), 30*time.Second); err == nil && strings.Contains(out, "OK") {
		rep.add("acme.sh already installed")
		return nil
	}
	sum := os.Getenv("ANGRY_ACME_CHECKSUM")
	dlURL := os.Getenv("ANGRY_ACME_URL")
	if dlURL == "" {
		dlURL = "https://github.com/acmesh-official/acme.sh/archive/refs/tags/3.1.1.tar.gz"
	}
	if err := utilValidateURL(dlURL); err != nil {
		return fmt.Errorf("acme.sh: %w", err)
	}
	if !validSHA256Hex(sum) {
		return fmt.Errorf("acme.sh: no pinned sha256 — set ANGRY_ACME_CHECKSUM (refusing unsigned installer)")
	}
	script := fmt.Sprintf(`set -e
mkdir -p /tmp/ab-acme-install
cd /tmp/ab-acme-install
if ! curl -fsSL --connect-timeout 15 '%s' -o acme.tar.gz; then echo 'ERROR: acme.sh download failed' >&2; exit 1; fi
echo '%s  acme.tar.gz' | sha256sum -c -
tar -xzf acme.tar.gz
INST=$(find /tmp/ab-acme-install -maxdepth 2 -name acme.sh -type f | head -1)
if [ -z "$INST" ]; then echo 'ERROR: acme.sh not in archive' >&2; exit 1; fi
sh "$INST" --install --nocron >/dev/null 2>&1 || sh "$INST" --install >/dev/null 2>&1
rm -rf /tmp/ab-acme-install
test -x %s
`, dlURL, sum, AcmeBin)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, script), 5*time.Minute); err != nil {
		return fmt.Errorf("acme.sh install: %s (exit %d) %s", err, exit, stderr)
	}
	// Cron entry for renewals (the --nocron install path skips it).
	cron := fmt.Sprintf(`( crontab -l 2>/dev/null | grep -v ab-acme-renew; echo '17 3 * * * %s --cron --home /root/.acme.sh >> /var/log/ab-acme.log 2>&1 # ab-acme-renew' ) | crontab -`, AcmeBin)
	if _, _, _, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, cron), 30*time.Second); err != nil {
		rep.warn("acme renewal cron not installed (%v) — renew manually", err)
	} else {
		rep.add("acme.sh installed + renewal cron (daily 03:17)")
	}
	return nil
}

// IssueNodeCert issues (or renews) the node's SAN certificate via HTTP-01
// webroot through the running caddy, then installs it to the shared cert paths
// with a reload hook for BOTH caddy and sing-box (path-based TLS inbounds pick
// the rotated cert up on reload).
func IssueNodeCert(ctx context.Context, client ports.SSHClient, useSudo bool, domain string, sans []string, rep *UtilityReport) error {
	if !ValidTLSDomain(domain) {
		return fmt.Errorf("acme: invalid domain %q", domain)
	}
	if len(sans) == 0 {
		sans = []string{domain, "panel." + domain}
	}
	var dflags strings.Builder
	for _, d := range sans {
		if !ValidTLSDomain(d) {
			return fmt.Errorf("acme: unsafe domain %q", d)
		}
		fmt.Fprintf(&dflags, " -d %s", d)
	}
	cert, key := CertPaths(domain)
	script := fmt.Sprintf(`set -e
%s --issue%s --webroot %s --server letsencrypt --keylength ec-256 --force || %s --issue%s --webroot %s --server letsencrypt --keylength ec-256
mkdir -p %s/%s
%s --install-cert -d %s \
  --key-file %s --fullchain-file %s \
  --reloadcmd "systemctl reload-or-restart %s; systemctl reload-or-restart sing-box"
chmod 600 %s
`, AcmeBin, dflags.String(), SiteDir, AcmeBin, dflags.String(), SiteDir, CertRoot, domain, AcmeBin, domain, key, cert, CaddyService, key)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, script), 10*time.Minute); err != nil {
		return fmt.Errorf("acme issue failed (exit %d): %s — check that A-records for %v point to this node and port 80 is open", exit, stderr, sans)
	}
	rep.add("ACME certificate issued for %v → %s", sans, cert)
	return nil
}

// UninstallUtility stops and removes one utility. Certificates and acme state
// are deliberately kept (re-install is idempotent; deleting secrets is not an
// uninstall side effect).
func UninstallUtility(ctx context.Context, client ports.SSHClient, useSudo bool, name string, rep *UtilityReport) error {
	switch name {
	case model.UtilityCaddy:
		cmd := fmt.Sprintf("systemctl disable --now %s 2>/dev/null; rm -f %s; rm -rf %s; systemctl daemon-reload", CaddyService, CaddyUnit, CaddyDir)
		if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, cmd), 60*time.Second); err != nil {
			return fmt.Errorf("uninstall caddy: %s (exit %d) %s", err, exit, stderr)
		}
		rep.add("caddy stopped + removed (certs kept)")
	case model.UtilityFakesite:
		if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "rm -rf "+SiteDir), 30*time.Second); err != nil {
			return fmt.Errorf("uninstall fakesite: %s (exit %d) %s", err, exit, stderr)
		}
		rep.add("fakesite removed")
	case model.UtilitySub:
		if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, "rm -rf "+SubDir), 30*time.Second); err != nil {
			return fmt.Errorf("uninstall sub: %s (exit %d) %s", err, exit, stderr)
		}
		rep.add("subscription statics removed")
	case model.UtilityACME:
		cmd := fmt.Sprintf("( crontab -l 2>/dev/null | grep -v ab-acme-renew ) | crontab - 2>/dev/null; rm -f %s", AcmeBin)
		if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, cmd), 30*time.Second); err != nil {
			return fmt.Errorf("uninstall acme: %s (exit %d) %s", err, exit, stderr)
		}
		rep.add("acme.sh removed (issued certs kept)")
	default:
		return fmt.Errorf("unknown utility %q", name)
	}
	return nil
}

// utilUploadPriv uploads content to a possibly-privileged path: UploadText to
// /tmp, then sudo cp into place (the applier_push.go pattern — UploadText
// itself cannot escalate).
func utilUploadPriv(ctx context.Context, client ports.SSHClient, useSudo bool, content, remotePath string, mode os.FileMode) error {
	if !useSudo {
		return client.UploadText(ctx, content, remotePath, mode)
	}
	tmp := "/tmp/ab-upload-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := client.UploadText(ctx, content, tmp, mode); err != nil {
		return err
	}
	qTmp, err := sshclient.QuotePOSIX(tmp)
	if err != nil {
		return err
	}
	qRemote, err := sshclient.QuotePOSIX(remotePath)
	if err != nil {
		return err
	}
	mv := fmt.Sprintf("cp %s %s && chmod %o %s && rm -f %s", qTmp, qRemote, mode, qRemote, qTmp)
	if _, stderr, exit, err := client.RunWithOutput(ctx, utilSudoBash(useSudo, mv), 30*time.Second); err != nil {
		return fmt.Errorf("%s (exit %d) %s", err, exit, stderr)
	}
	return nil
}

func utilArchToGo(arch string) string {
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

func utilValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("caddy URL invalid: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("caddy URL must be http(s), got scheme %q", u.Scheme)
	}
	if strings.ContainsAny(raw, "'`\\$") {
		return fmt.Errorf("caddy URL must not contain shell metacharacters")
	}
	return nil
}

func validSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
