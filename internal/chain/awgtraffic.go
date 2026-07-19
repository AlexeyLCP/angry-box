package chain

// awgtraffic.go — per-peer AWG traffic accounting (LucX CollectTraffic port).
// The kernel tracks rx/tx per peer (`awg show <iface> transfer`); peers are
// our per-user WireGuard identities (User.AWGPublicKey), so folding the
// kernel counters into per-user cumulative bytes gives real per-user usage
// without any agent on the node. Counters reset on interface restart, so the
// folder keeps the last seen values per (node, peer) and handles resets
// (current < last → add the full current value instead of a negative delta).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// ParseAWGTransfer parses `awg show <iface> transfer` output: one
// "<pubkey>\t<rx>\t<tx>" line per peer (bytes). Malformed lines are skipped.
func ParseAWGTransfer(out string) map[string][2]int64 {
	res := map[string][2]int64{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		rx, err1 := strconv.ParseInt(fields[1], 10, 64)
		tx, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		res[fields[0]] = [2]int64{rx, tx}
	}
	return res
}

// FoldAWGTraffic folds one node's current per-peer counters into per-user
// cumulative totals. Deltas are computed against NodeMetrics.AWGPeerTransfer
// (the previous snapshot — updated in place and saved by the caller through
// SaveMetrics). Peers without a matching user (unknown/removed) are tracked
// in the snapshot but not folded. Interface-restart counter resets are
// handled by treating current<last as a fresh start. Returns the number of
// user records updated.
func FoldAWGTraffic(st *Store, nodeID, iface string, current map[string][2]int64, m *model.NodeMetrics) int {
	if m.AWGPeerTransfer == nil {
		m.AWGPeerTransfer = map[string][2]int64{}
	}
	users, err := st.ListUsers()
	if err != nil {
		return 0
	}
	byPub := map[string]*model.User{}
	for _, u := range users {
		if u.AWGPublicKey != "" {
			byPub[u.AWGPublicKey] = u
		}
	}
	updated := 0
	for pub, cur := range current {
		last, seen := m.AWGPeerTransfer[pub]
		var dRx, dTx int64
		switch {
		case !seen:
			dRx, dTx = cur[0], cur[1] // first observation — count everything
		case cur[0] < last[0] || cur[1] < last[1]:
			dRx, dTx = cur[0], cur[1] // counter reset (interface restarted)
		default:
			dRx, dTx = cur[0]-last[0], cur[1]-last[1]
		}
		m.AWGPeerTransfer[pub] = cur
		if dRx == 0 && dTx == 0 {
			continue
		}
		if u, ok := byPub[pub]; ok {
			u.AWGRxBytes += dRx
			u.AWGTxBytes += dTx
			u.AWGTrafficAt = time.Now()
			if err := st.SaveUser(u); err == nil {
				updated++
			}
		}
	}
	return updated
}

// AWGTransferCommand is the remote command read for one interface's counters.
// Separate helper so tests pin the exact command shape.
func AWGTransferCommand(iface string) string {
	return fmt.Sprintf("awg show %s transfer", iface)
}

// awgUserIfaces are the kernel interfaces carrying user-facing AWG peers
// (awg0 = chain entry / standalone, awg1 = co-located standalone on a second
// subnet). Exit-link interfaces (awg-exit-nX) are service tunnels, not users.
var awgUserIfaces = []string{"awg0", "awg1"}

// CollectAWGTrafficForNode reads the per-peer counters from the node's
// user-facing AWG interfaces and folds them into per-user totals (v0.7).
// Silent on any per-interface failure (a node without awg1, or without AWG at
// all, is normal — the caller runs this on every healthy node). Returns the
// number of user records updated across all interfaces.
func CollectAWGTrafficForNode(ctx context.Context, client ports.SSHClient, st *Store, nodeID string, useSudo bool) int {
	updated := 0
	for _, iface := range awgUserIfaces {
		out, _, _, err := client.RunWithOutput(ctx, sudoWrap(useSudo, AWGTransferCommand(iface)), 15*time.Second)
		if err != nil {
			continue
		}
		current := ParseAWGTransfer(out)
		if len(current) == 0 {
			continue
		}
		m, err := st.GetMetrics(nodeID)
		if err != nil || m == nil {
			m = &model.NodeMetrics{HostID: nodeID}
		}
		updated += FoldAWGTraffic(st, nodeID, iface, current, m)
		_ = st.SaveMetrics(m)
	}
	return updated
}

// SelfHealAWGRules re-asserts the AWG interface's PostUp iptables rules when
// they vanished (LucX ensureNatRules pattern: fail2ban/docker iptables flushes
// silently kill FORWARD/MASQUERADE → egress dies with the tunnel looking up).
// Agent-less: the health loop runs this on healthy nodes; the heal itself is
// `sed -n 's/^PostUp = //p' <conf> | sh` — our PostUp lines are idempotent
// (-C … || -A) and re-assert ip_forward + rp_filter + FORWARD/MASQUERADE.
// Returns true when a heal was applied (caller writes the audit entry).
func SelfHealAWGRules(ctx context.Context, client ports.SSHClient, iface string, useSudo bool) (bool, error) {
	conf := "/etc/amnezia/amneziawg/" + iface + ".conf"
	check := sudoWrap(useSudo, fmt.Sprintf("test -f %s && iptables -C FORWARD -i %s -o sing-box-tun -j ACCEPT", conf, iface))
	if _, _, _, err := client.RunWithOutput(ctx, check, 15*time.Second); err == nil {
		return false, nil // rules present — nothing to do
	}
	heal := sudoWrap(useSudo, fmt.Sprintf("sed -n 's/^PostUp = //p' %s | sh", conf))
	if _, stderr, _, err := client.RunWithOutput(ctx, heal, 20*time.Second); err != nil {
		return false, fmt.Errorf("re-run PostUp %s: %v (%s)", conf, err, strings.TrimSpace(stderr))
	}
	return true, nil
}
