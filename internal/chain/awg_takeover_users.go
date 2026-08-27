package chain

// awg_takeover_users.go — materializes imported AWG peers (parsed from the
// takeover'd node's awg0-peers.list / awg0.conf [Peer] sections) as model.User
// entries, so per-client source_ip_cidr routing is available on a takeover'd
// AWG inbound. AGENTS.md Known Issue #10: the takeover previously imported the
// peers but created no model.User records, so the peer-render loops in
// RenderServerAWGConf (which read peers from model.User via usersByChainMap /
// usersByInboundMap) never saw them — per-client routing was unavailable.
//
// Materialization is the prerequisite. The kernel awg0.conf is left untouched
// by the takeover (the kernel keeps serving the imported peers); the
// materialized users become visible in the UI and, on the next ApplyMergedNode
// re-apply, are rendered into a fresh awg0.conf by RenderServerAWGConf (a
// separate follow-up switches the takeover to pushing that fresh conf).
//
// Rollback symmetry: rollbackToOldVPN calls DeleteSynthesizedAWGUsers to remove
// the materialized users so a rolled-back takeover leaves no phantom users.

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// MaterializeAWGPeersAsUsers creates a model.User per imported AWG peer so the
// per-client routing plumbing (usersByChainMap / usersByInboundMap →
// RenderServerAWGConf peer-render loops) picks them up. nodeID is the takeover
// target node; it seeds the synthesized user ID (takeover-<nodeID>-<pubKey[:8]>)
// and the user Name. chainName, when non-empty, assigns the user to that chain
// (ChainNames) so usersByChainMap finds it; otherwise the caller wires the
// user to a standalone inbound via ForUsers (not done here — the takeover'd
// inbound is chain-shaped in the AWG case, so ChainNames is the natural fit).
//
// Dedup (do not clobber real users):
//   - ID collision with an existing user whose AWGPublicKey differs → skip
//     (log) — refuse to overwrite a real user.
//   - an existing user with the same AWGPublicKey already managed → skip
//     (already a managed peer; no-op).
//
// Returns the IDs of the users it actually created (for rollback symmetry).
func MaterializeAWGPeersAsUsers(store *Store, nodeID string, peers []AwgPeerEntry, chainName string) ([]string, error) {
	if store == nil {
		return nil, fmt.Errorf("awg takeover users: nil store")
	}
	existing, err := store.ListUsers()
	if err != nil {
		return nil, fmt.Errorf("awg takeover users: list users: %w", err)
	}
	// Index existing users by ID and by AWGPublicKey for the dedup pre-flight.
	byID := make(map[string]*model.User, len(existing))
	byPub := make(map[string]*model.User, len(existing))
	for _, u := range existing {
		byID[u.ID] = u
		if u.AWGPublicKey != "" {
			byPub[u.AWGPublicKey] = u
		}
	}

	var created []string
	for i, p := range peers {
		if p.PublicKey == "" {
			continue // a peer without a public key is not a usable peer
		}
		// Deterministic ID: takeover-<nodeID>-<pubKey[:8]>. No operator would
		// type this, so an ID collision means a prior takeover synthesized the
		// same peer (safe to overwrite with identical content) OR a real user
		// happens to have this ID (extremely unlikely — skip if pubkey differs).
		id := fmt.Sprintf("takeover-%s-%s", nodeID, pubKeyPrefix(p.PublicKey, 8))
		if prev, ok := byID[id]; ok {
			if prev.AWGPublicKey != p.PublicKey {
				slog.Warn("awg takeover: synthesized user ID collision with a different pubkey — skipping",
					"node", nodeID, "id", id)
				continue
			}
			// Same ID + same pubkey → already synthesized previously; skip.
			continue
		}
		if _, ok := byPub[p.PublicKey]; ok {
			// A real user already manages this peer's pubkey; skip (don't
			// duplicate the peer on the server conf).
			continue
		}
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("takeover-%s-%d", nodeID, i)
		}
		u := &model.User{
			ID:           id,
			Name:         name,
			Active:       true,
			Protocols:    []string{"awg"},
			AWGPublicKey: p.PublicKey,
			AWGAddress:   p.AllowedIPs,
			CreatedAt:    timeNow(),
		}
		if chainName != "" {
			u.ChainNames = []string{chainName}
		}
		if err := store.SaveUser(u); err != nil {
			// SaveUser failure on one peer must not abort the whole materialization
			// — the other peers are independent. Log + continue.
			slog.Warn("awg takeover: save synthesized user failed",
				"node", nodeID, "id", id, "err", err)
			continue
		}
		byID[id] = u
		byPub[p.PublicKey] = u
		created = append(created, id)
	}
	return created, nil
}

// DeleteSynthesizedAWGUsers removes the model.User entries the AWG takeover
// materialized (by ID), for rollback symmetry. Best-effort: a missing user
// (already deleted) is logged, not fatal. Errors on other failures are logged
// too — a partial cleanup is better than aborting the rollback mid-way.
func DeleteSynthesizedAWGUsers(store *Store, ids []string) {
	if store == nil {
		return
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := store.DeleteUser(id); err != nil {
			// ErrUserNotFound means it was already deleted (e.g. operator removed
			// it via the UI) — fine, not a rollback failure. Other errors: log.
			slog.Warn("awg takeover rollback: delete synthesized user failed",
				"id", id, "err", err)
		}
	}
}

// pubKeyPrefix returns the first n characters of a WireGuard public key (base64),
// lowercased, for use in a deterministic synthesized user ID. Returns the whole
// key when shorter than n (defensive — a valid WG pubkey is 44 base64 chars).
func pubKeyPrefix(pub string, n int) string {
	p := strings.ToLower(strings.TrimSpace(pub))
	if len(p) < n {
		return p
	}
	return p[:n]
}
// AwgServerConfigToAmnezia converts an imported AwgServerConfig (flat
// JC/JMIN/JMAX/S1-S4 ints + H1-H4/I1-I5 strings) to a *config.AmneziaOptions
// for RenderServerAWGConf. Returns nil when JC==0 (no obfuscation — plain WG).
// The field shapes match 1:1 with writeAmneziaConfLines (awg_server.go).
func AwgServerConfigToAmnezia(s *AwgServerConfig) *config.AmneziaOptions {
	if s == nil || s.JC == 0 {
		return nil
	}
	return &config.AmneziaOptions{
		JC:   s.JC,
		JMIN: s.JMIN,
		JMAX: s.JMAX,
		S1:   s.S1, S2: s.S2, S3: s.S3, S4: s.S4,
		H1: s.H1, H2: s.H2, H3: s.H3, H4: s.H4,
		I1: s.I1, I2: s.I2, I3: s.I3, I4: s.I4, I5: s.I5,
	}
}

// RenderTakeoverAWGConf builds a fresh kernel awg0.conf (via RenderServerAWGConf)
// from the imported server config + materialized users, so the orchestrator
// OWNS the awg0.conf after takeover (instead of leaving the imported one
// untouched). Returns the AWGConfFile for pushing via PushConfigWithAWG.
// peers are built from the materialized model.User entries (each user's
// AWGPublicKey + AWGAddress), NOT from the raw AwgPeerEntry — so future user
// add/remove re-renders correctly.
func RenderTakeoverAWGConf(server *AwgServerConfig, users []model.User) AWGConfFile {
	var peers []AWGServerPeer
	for _, u := range users {
		if !u.Active || u.AWGPublicKey == "" || u.AWGAddress == "" {
			continue
		}
		peers = append(peers, AWGServerPeer{PublicKey: u.AWGPublicKey, AllowedIPs: u.AWGAddress, PresharedKey: u.AWGPresharedKey})
	}
	tunnelAddr := server.Address
	if tunnelAddr == "" {
		tunnelAddr = "10.8.0.1/24"
	}
	return AWGConfFile{
		Path:        awg0ConfPath,
		ServiceName: awgServiceName("awg0"),
		Content: RenderServerAWGConf(AWGServerConfParams{
			ServerPrivateKey: server.PrivateKey,
			ListenPort:       server.ListenPort,
			TunnelAddress:    tunnelAddr,
			MTU:              1420,
			Amnezia:          AwgServerConfigToAmnezia(server),
			Peers:            peers,
			TUNInterface:     tunInterfaceName,
			InterfaceName:    "awg0",
		}),
	}
}
