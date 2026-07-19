package chain

// awg_peersync.go — live peer sync for kernel awg-quick interfaces, ported
// from LucX-UI's awg.Manager.SyncPeers. The stock deploy path
// (pushAWGConfs → enableAWGService) restarts awg-quick@<iface> on every push,
// which drops every connected client (handshake reset) even when the only
// change is one added/removed user peer. When the pushed .conf's [Interface]
// section is identical to what already lives on the node and the service is
// active, we instead diff the peer set and apply it with `awg set` — peers
// not touched by the diff never notice (zero-downtime user add/remove).
// [Interface] changes (keys, ports, amnezia, PostUp) still take the full
// restart path — the kernel cannot re-read those without down/up.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// splitAWGConf parses an awg-quick .conf into its [Interface] section (as a
// normalized string for equality comparison) and the list of [Peer] entries
// (PublicKey + AllowedIPs — the only fields our renderers emit per peer).
// Lines are trimmed; blank lines and section headers are dropped from the
// normalized interface part so cosmetic differences don't force a restart.
func splitAWGConf(content string) (ifaceNorm string, peers []AWGServerPeer) {
	var ifaceLines []string
	var cur *AWGServerPeer
	inPeer := false
	flush := func() {
		if cur != nil {
			peers = append(peers, *cur)
			cur = nil
		}
	}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case line == "[Interface]":
			inPeer = false
		case line == "[Peer]":
			flush()
			cur = &AWGServerPeer{}
			inPeer = true
		case inPeer:
			k, v, ok := strings.Cut(line, "=")
			if !ok || cur == nil {
				continue
			}
			switch strings.TrimSpace(k) {
			case "PublicKey":
				cur.PublicKey = strings.TrimSpace(v)
			case "AllowedIPs":
				cur.AllowedIPs = strings.TrimSpace(v)
			}
		default:
			ifaceLines = append(ifaceLines, line)
		}
	}
	flush()
	return strings.Join(ifaceLines, "\n"), peers
}

// serviceActive reports whether a systemd unit is currently active on the node.
func serviceActive(ctx context.Context, client ports.SSHClient, service string, useSudo bool) bool {
	out, _, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, "systemctl is-active "+service), 15*time.Second)
	return err == nil && strings.TrimSpace(out) == "active"
}

// awgIfaceFromService derives the kernel interface name from the systemd unit
// ("awg-quick@awg0" → "awg0"). awg-quick names the interface after the conf
// filename, which the unit template substitutes.
func awgIfaceFromService(service string) string {
	if i := strings.LastIndex(service, "@"); i >= 0 {
		return service[i+1:]
	}
	return service
}

// syncAWGPeers applies the desired peer set to a live interface without a
// restart: every desired peer is (re)asserted via `awg set` (idempotent —
// add or update), and every live peer absent from the desired set is removed.
// Live state is read from `awg show <iface> peers` (one pubkey per line).
func syncAWGPeers(ctx context.Context, client ports.SSHClient, iface string, desired []AWGServerPeer, useSudo bool) error {
	out, _, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, "awg show "+iface+" peers"), 15*time.Second)
	if err != nil {
		return fmt.Errorf("list peers on %s: %w", iface, err)
	}
	live := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if pub := strings.TrimSpace(line); pub != "" {
			live[pub] = true
		}
	}
	want := map[string]bool{}
	for _, p := range desired {
		if p.PublicKey == "" {
			continue
		}
		want[p.PublicKey] = true
		allowed := p.AllowedIPs
		if allowed == "" {
			allowed = "0.0.0.0/0, ::/0"
		}
		cmd := fmt.Sprintf("awg set %s peer %s allowed-ips %s", iface, p.PublicKey, allowed)
		if _, stderr, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, cmd), 15*time.Second); err != nil {
			return fmt.Errorf("set peer %s… on %s: %w (%s)", p.PublicKey[:8], iface, err, stderr)
		}
	}
	for pub := range live {
		if want[pub] {
			continue
		}
		cmd := fmt.Sprintf("awg set %s peer %s remove", iface, pub)
		if _, stderr, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, cmd), 15*time.Second); err != nil {
			return fmt.Errorf("remove peer %s… on %s: %w (%s)", pub[:8], iface, err, stderr)
		}
	}
	return nil
}

// tryPeerSync decides whether the pushed conf can be applied by live peer
// sync instead of an awg-quick restart, and performs the sync when so.
// Returns true when the sync was applied (caller skips the restart).
// Conditions: the service is active AND the remote .conf's [Interface]
// section is byte-identical (normalized) to the pushed one — the kernel
// cannot re-read interface fields (keys/amnezia/PostUp) without down/up, so
// any interface change takes the restart path.
func tryPeerSync(ctx context.Context, client ports.SSHClient, file AWGConfFile, useSudo bool) bool {
	if !serviceActive(ctx, client, file.ServiceName, useSudo) {
		return false
	}
	out, _, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, "cat "+file.Path), 15*time.Second)
	if err != nil {
		return false
	}
	remoteIface, _ := splitAWGConf(out)
	newIface, newPeers := splitAWGConf(file.Content)
	if remoteIface != newIface {
		return false
	}
	if err := syncAWGPeers(ctx, client, awgIfaceFromService(file.ServiceName), newPeers, useSudo); err != nil {
		// A failed sync falls back to the restart path — safer than leaving a
		// half-applied peer set (the restart re-reads the full conf anyway).
		return false
	}
	return true
}

// sudoWrap prefixes a command with sudo when the deploy runs as a non-root
// SSH user with passwordless sudo (model.DeployOptions.UseSudo).
func sudoWrap(useSudo bool, cmd string) string {
	if !useSudo {
		return cmd
	}
	return "sudo " + cmd
}
