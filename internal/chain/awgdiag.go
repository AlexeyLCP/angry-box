package chain

// awgdiag.go — read-only AWG diagnostics probe chain (LucX-UI diagnostics.go
// pattern, adapted to the orchestrator's agent-less SSH model). Where the P1a
// health monitor answers "is the node/service up" (systemd level), this
// answers "WHY is the AWG data plane broken" — the exact checks from the
// AGENTS.md debugging patterns: interface state, kernel listeners, peer
// handshake freshness, ip_forward, rp_filter, FORWARD rules between awg0 and
// the sing-box TUN overlay, sing-box service, and the Debian-13 package
// prerequisites (iptables/nftables). Every check carries its evidence so the
// UI modal shows the operator what was actually read, not just red/green.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// DiagStatus is one check's outcome.
type DiagStatus string

const (
	DiagOK   DiagStatus = "ok"
	DiagWarn DiagStatus = "warn"
	DiagFail DiagStatus = "fail"
)

// DiagCheck is one probe result: what was checked and the evidence read.
type DiagCheck struct {
	Name   string     `json:"name"`
	Status DiagStatus `json:"status"`
	Detail string     `json:"detail"` // evidence: the value/command output that decided the verdict
}

// DiagnoseAWGNode runs the probe chain against one node's user-facing AWG
// interface (awg0, or awg1 for a co-located standalone on a second interface).
// iface may be empty — it is derived from the node's confs (defaults awg0).
// The chain always runs to completion so the operator sees the full picture;
// individual probe errors become DiagFail entries, not aborts.
func DiagnoseAWGNode(ctx context.Context, client ports.SSHClient, iface string, useSudo bool) []DiagCheck {
	if iface == "" {
		iface = "awg0"
	}
	var checks []DiagCheck
	run := func(cmd string) (string, error) {
		out, stderr, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, cmd), 15*time.Second)
		if err != nil {
			return out, fmt.Errorf("%v (%s)", err, strings.TrimSpace(stderr))
		}
		return strings.TrimSpace(out), nil
	}
	add := func(name string, status DiagStatus, detail string) {
		checks = append(checks, DiagCheck{Name: name, Status: status, Detail: detail})
	}

	// 1. systemd unit active.
	svc := "awg-quick@" + iface
	out, err := run("systemctl is-active " + svc)
	switch {
	case err != nil:
		add("systemd "+svc, DiagFail, "is-active failed: "+err.Error())
	case out == "active":
		add("systemd "+svc, DiagOK, "active")
	default:
		add("systemd "+svc, DiagFail, "state: "+out)
	}

	// 2. Kernel interface exists + UP.
	out, err = run("ip -br link show " + iface)
	switch {
	case err != nil || out == "":
		add("interface "+iface, DiagFail, "not present (ip link returned nothing)")
	case strings.Contains(out, "UP"):
		add("interface "+iface, DiagOK, out)
	default:
		add("interface "+iface, DiagFail, "not UP: "+out)
	}

	// 3. Listen port (kernel actually bound).
	out, err = run("awg show " + iface + " listen-port")
	if err != nil {
		add("listen port", DiagFail, "awg show listen-port: "+err.Error())
	} else {
		add("listen port", DiagOK, "UDP "+out)
	}

	// 4. Peers + handshake freshness (transfer counters + latest handshakes).
	peers, perr := run("awg show " + iface + " peers")
	handshakes, herr := run("awg show " + iface + " latest-handshakes")
	switch {
	case perr != nil:
		add("peers", DiagFail, perr.Error())
	case strings.TrimSpace(peers) == "":
		add("peers", DiagWarn, "no peers configured (no users?)")
	default:
		n := len(strings.Split(strings.TrimSpace(peers), "\n"))
		if herr != nil {
			add("peers", DiagWarn, fmt.Sprintf("%d peer(s); latest-handshakes unreadable: %v", n, herr))
		} else {
			fresh, stale := handshakeFreshness(handshakes, time.Now())
			switch {
			case fresh > 0:
				add("peers", DiagOK, fmt.Sprintf("%d peer(s), %d with handshake <5min", n, fresh))
			case stale > 0:
				add("peers", DiagWarn, fmt.Sprintf("%d peer(s), none with a fresh handshake (stale or never connected)", n))
			default:
				add("peers", DiagWarn, fmt.Sprintf("%d peer(s), no handshakes yet", n))
			}
		}
	}

	// 5. ip_forward (without it the kernel drops awg0↔sing-box-tun traffic).
	out, err = run("sysctl -n net.ipv4.ip_forward")
	if err != nil || out != "1" {
		add("ip_forward", DiagFail, "net.ipv4.ip_forward="+out+" (must be 1)")
	} else {
		add("ip_forward", DiagOK, "1")
	}

	// 6. rp_filter disabled on the AWG interface (strict RPF drops return traffic).
	out, err = run("sysctl -n net.ipv4.conf." + iface + ".rp_filter")
	switch {
	case err != nil:
		add("rp_filter", DiagWarn, "unreadable: "+err.Error())
	case out == "0":
		add("rp_filter", DiagOK, "0 (disabled on "+iface+")")
	default:
		add("rp_filter", DiagFail, iface+" rp_filter="+out+" (must be 0 — return packets get dropped otherwise)")
	}

	// 7. FORWARD rules between the AWG interface and the sing-box TUN overlay.
	fwdOut, fwdErr := run("iptables -C FORWARD -i " + iface + " -o sing-box-tun -j ACCEPT")
	if fwdErr != nil {
		add("FORWARD "+iface+"→sing-box-tun", DiagFail, "missing (iptables -C: "+fwdOut+") — egress through the TUN overlay silently fails")
	} else {
		add("FORWARD "+iface+"→sing-box-tun", DiagOK, "present")
	}

	// 8. iptables presence (Debian 13 doesn't ship it; PostUp rules need the shim).
	if _, err := run("command -v iptables"); err != nil {
		add("iptables package", DiagFail, "iptables not installed — awg-quick PostUp rules fail on Debian 13+")
	} else {
		add("iptables package", DiagOK, "installed")
	}

	// 9. sing-box service + TUN overlay interface.
	out, _ = run("systemctl is-active sing-box")
	if out == "active" {
		add("sing-box service", DiagOK, "active")
	} else {
		add("sing-box service", DiagFail, "state: "+out)
	}
	out, err = run("ip -br link show sing-box-tun")
	switch {
	case err != nil || out == "":
		add("sing-box-tun", DiagFail, "TUN overlay interface missing (sing-box config has no tun inbound?)")
	case strings.Contains(out, "UP"):
		add("sing-box-tun", DiagOK, out)
	default:
		add("sing-box-tun", DiagWarn, "present but not UP: "+out)
	}

	return checks
}

// handshakeFreshness counts peers with a handshake timestamp within 5 minutes
// (fresh) vs older/non-zero (stale). `awg show <iface> latest-handshakes`
// prints "<pubkey>\t<unix-seconds>" per peer, 0 = never.
func handshakeFreshness(out string, now time.Time) (fresh, stale int) {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ts, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || ts == 0 {
			continue
		}
		if now.Sub(time.Unix(ts, 0)) < 5*time.Minute {
			fresh++
		} else {
			stale++
		}
	}
	return fresh, stale
}
