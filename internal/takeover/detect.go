// Package takeover implements the "take over an existing VPN server" flow:
// detect an existing VPN (AWG/sing-box/Xray/MTProxy) over SSH, convert its
// config into a sing-box config with the SAME settings, install sing-box,
// disable (not delete) the old VPN, start sing-box, and auto-rollback to the
// old VPN if sing-box fails to come up.
//
// This file: detection. detect.go probes the remote host over SSH for known
// VPN services + config files and returns what it found.
package takeover

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// DetectedVPNType identifies the kind of existing VPN found on a node.
type DetectedVPNType string

const (
	DetectedNone     DetectedVPNType = "none"
	DetectedAWG      DetectedVPNType = "awg"
	DetectedSingBox  DetectedVPNType = "singbox"
	DetectedXray     DetectedVPNType = "xray"
	DetectedMTProxy  DetectedVPNType = "mtproxy"
)

// Detection is the result of probing a node for an existing VPN.
type Detection struct {
	Type         DetectedVPNType `json:"type"`
	ServiceName  string          `json:"service_name,omitempty"`  // systemd unit, e.g. "xray"
	IsActive     bool            `json:"is_active"`
	IsEnabled    bool            `json:"is_enabled"`
	ConfigPath   string          `json:"config_path,omitempty"`
	ConfigContent string         `json:"-"` // raw config (not serialized to JSON API; large)
	Other        []string        `json:"other,omitempty"` // other VPNs also present (warnings)
	Note         string          `json:"note,omitempty"`
}

// candidate service names → VPN type. Probed via `systemctl is-active/is-enabled`.
var candidateServices = []struct {
	unit string
	kind DetectedVPNType
}{
	{"sing-box", DetectedSingBox},
	{"xray", DetectedXray},
	{"x-ui", DetectedXray}, // 3x-ui runs xray under x-ui
	{"mtproxymax", DetectedMTProxy},
	{"mtg", DetectedMTProxy},
	{"telemt", DetectedMTProxy},
	{"awg-quick@awg0", DetectedAWG},
}

// candidate config paths → kind. Read via `cat` (best-effort).
var candidateConfigPaths = []struct {
	path string
	kind DetectedVPNType
}{
	{"/etc/sing-box/config.json", DetectedSingBox},
	{"/usr/local/etc/sing-box/config.json", DetectedSingBox},
	{"/usr/local/etc/xray/config.json", DetectedXray},
	{"/usr/local/x-ui/bin/config.json", DetectedXray}, // 3x-ui
	{"/etc/xray/config.json", DetectedXray},
	{"/etc/mtg/mtg.conf", DetectedMTProxy},
	{"/etc/amnezia/amneziawg/awg0.conf", DetectedAWG},
}

// cfgHit is a config-file detection (path + content).
type cfgHit struct {
	kind    DetectedVPNType
	path    string
	content string
}

// svcHit is a service detection (systemd unit + active/enabled).
type svcHit struct {
	kind    DetectedVPNType
	unit    string
	active  bool
	enabled bool
}

// DetectVPN connects to host over SSH and probes for an existing VPN. Returns
// the primary detection (priority: active service > enabled service > config
// file present) plus any other VPNs found in Detection.Other. A non-nil error
// means SSH itself failed; a successful probe with nothing found returns
// Detection{Type: DetectedNone} and nil error.
func DetectVPN(ctx context.Context, host model.Host, useSudo bool, connector ...ports.SSHConnector) (*Detection, error) {
	conn := sshclient.DefaultConnector
	if len(connector) > 0 && connector[0] != nil {
		conn = connector[0]
	}
	// TODO(ssh-keys): wire resolveHostKey once store ref available — DetectVPN
	// currently takes no *chain.Store, so the panel default-key fallback is not
	// applied here. Callers in web/takeover.go pass info.Host directly; the
	// detect step is read-only (no deploy), so the impact is limited to nodes
	// that rely solely on the default key. Adding a store param would require
	// updating the DetectVPN signature + all callers (web + e2e) — deferred.
	client, err := conn.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("takeover: detect: ssh connect: %w", err)
	}
	defer client.Close()

	priv := func(cmd string) string {
		if useSudo {
			return "sudo " + cmd
		}
		return cmd
	}
	sudoBash := func(cmd string) string {
		if !useSudo {
			return cmd
		}
		return fmt.Sprintf("sudo bash -c '%s'", strings.ReplaceAll(cmd, "'", `'\''`))
	}

	// Probe each candidate service: is-active + is-enabled.
	var hits []svcHit
	for _, c := range candidateServices {
		activeOut, _, _, _ := client.RunWithOutput(ctx, priv("systemctl is-active "+c.unit+" 2>/dev/null"), 15*time.Second)
		enabledOut, _, _, _ := client.RunWithOutput(ctx, priv("systemctl is-enabled "+c.unit+" 2>/dev/null"), 15*time.Second)
		active := strings.TrimSpace(activeOut) == "active"
		enabled := strings.TrimSpace(enabledOut) == "enabled"
		if active || enabled {
			hits = append(hits, svcHit{c.kind, c.unit, active, enabled})
		}
	}

	// Probe each candidate config path (best-effort cat).
	var cfgHits []cfgHit
	for _, c := range candidateConfigPaths {
		out, _, exit, _ := client.RunWithOutput(ctx, priv("cat "+c.path+" 2>/dev/null"), 15*time.Second)
		if exit == 0 && strings.TrimSpace(out) != "" {
			cfgHits = append(cfgHits, cfgHit{c.kind, c.path, out})
		}
	}

	// Also probe binaries (informational — feeds Detection.Other / Note).
	binOut, _, _, _ := client.RunWithOutput(ctx, sudoBash("command -v sing-box xray mtg awg awg-quick 2>/dev/null"), 15*time.Second)
	binaries := strings.Fields(binOut)
	_ = binaries // noted but service/config hits are authoritative

	// Build the primary detection. Priority: active service (with its config if
	// found) > enabled service > config file present. AWG via awg0.conf is
	// detected even without a single unit (awg-quick@awg0 may be inactive in
	// WSL but the conf present).
	primary := &Detection{Type: DetectedNone}

	// 1. Active service wins.
	for _, h := range hits {
		if h.active {
			primary.Type = h.kind
			primary.ServiceName = h.unit
			primary.IsActive = true
			primary.IsEnabled = h.enabled
			if cfg, ok := configForKind(cfgHits, h.kind); ok {
				primary.ConfigPath = cfg.path
				primary.ConfigContent = cfg.content
			}
			break
		}
	}
	// 2. Enabled-but-inactive service.
	if primary.Type == DetectedNone {
		for _, h := range hits {
			primary.Type = h.kind
			primary.ServiceName = h.unit
			primary.IsActive = false
			primary.IsEnabled = true
			if cfg, ok := configForKind(cfgHits, h.kind); ok {
				primary.ConfigPath = cfg.path
				primary.ConfigContent = cfg.content
			}
			break
		}
	}
	// 3. Config file present (no service) — e.g. an installed-but-stopped VPN.
	if primary.Type == DetectedNone {
		for _, c := range cfgHits {
			primary.Type = c.kind
			primary.ConfigPath = c.path
			primary.ConfigContent = c.content
			// No service known; try to guess a unit name for rollback.
			primary.ServiceName = defaultUnitFor(c.kind)
			break
		}
	}

	// Collect "other" VPNs for warning (anything detected that isn't the primary).
	seen := map[DetectedVPNType]bool{primary.Type: true}
	for _, h := range hits {
		if !seen[h.kind] {
			primary.Other = append(primary.Other, string(h.kind)+" ("+h.unit+")")
			seen[h.kind] = true
		}
	}
	for _, c := range cfgHits {
		if !seen[c.kind] {
			primary.Other = append(primary.Other, string(c.kind)+" config at "+c.path)
			seen[c.kind] = true
		}
	}

	if primary.Type == DetectedNone {
		primary.Note = "No existing VPN detected. Use Install to deploy sing-box from scratch."
	}
	return primary, nil
}

// configForKind returns the first config hit matching kind.
func configForKind(hits []cfgHit, kind DetectedVPNType) (cfgHit, bool) {
	for _, h := range hits {
		if h.kind == kind {
			return h, true
		}
	}
	return cfgHit{}, false
}

// defaultUnitFor guesses the systemd unit name for a VPN kind (used when only a
// config file is found, no service — for rollback we need a unit to re-enable).
func defaultUnitFor(kind DetectedVPNType) string {
	switch kind {
	case DetectedSingBox:
		return "sing-box"
	case DetectedXray:
		return "xray"
	case DetectedMTProxy:
		return "mtg"
	case DetectedAWG:
		return "awg-quick@awg0"
	}
	return ""
}