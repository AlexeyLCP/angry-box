package web

// subscription.go — the public /sub/{token} subscription endpoint. Returns a
// per-user config blob (share-links list) fetched by client apps (v2rayNG /
// Nekoray / sing-box clients / curl). The endpoint is OUTSIDE s.auth (public —
// client apps do not send Basic-Auth) and is registered directly on the mux
// in Server.Register. GET passes the CSRF middleware automatically (safe-method
// bypass).
//
// Format negotiation: ?format=raw|base64; default by User-Agent — v2rayNG /
// Nekoray / NekoBox / Shadowrocket / sing-box clients get base64 (the v2ray
// subscription convention: newline-joined links, base64-encoded), everything
// else gets the raw newline-joined links (for an operator pasting into a
// client by hand).
//
// The endpoint reuses the existing buildConnectionLink / buildStandaloneLink /
// MTProxy-link builders via collectUserLinks (extracted from handleUserConfig
// + handleUserQR so all three share one code path). It honors ComputeStatus:
// expired / disabled / limited users get a 404 so the subscription stops
// working when the user is deactivated (forward-compatible with the P0b-2
// quota poller). start_on_first_use users get FirstUseAt stamped on first
// fetch (no enforcement, just record).

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// handleSubscription serves a user's config blob by subscription token.
// Public (no auth): registered directly on the mux without s.auth.
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	st := s.store()
	u, err := st.GetUserBySubscriptionToken(token)
	if err != nil {
		// Unknown token — 404, do not leak existence.
		http.NotFound(w, r)
		return
	}
	// Honor lifecycle status. "limited" is set only by the P0b-2 poller (not
	// yet wired), but checking it now makes the endpoint forward-compatible.
	switch u.ComputeStatus() {
	case "disabled", "expired", "limited":
		http.NotFound(w, r)
		return
	}

	// Lazy token backfill for legacy users (added before the token field
	// existed). Mint one on first fetch so the operator never needs a manual
	// migration. New users get a token at create time, so this only ever
	// touches legacy users once.
	if u.SubscriptionToken == "" {
		if t, err := chain.GenerateSubscriptionToken(); err == nil {
			u.SubscriptionToken = t
			_ = st.SaveUser(u)
		}
	}
	// start_on_first_use activation: stamp the first fetch time. No enforcement
	// this slice — the stamp is the basis a future expiry timer reads.
	if u.ExpireStrategy == "start_on_first_use" && u.FirstUseAt.IsZero() {
		u.FirstUseAt = time.Now()
		_ = st.SaveUser(u)
	}

	links := s.collectUserLinks(u, st)
	if len(links) == 0 {
		// User has no chains/inbounds assigned — return 404 so a client treats
		// it as an empty/expired subscription rather than a 200 with no body.
		http.NotFound(w, r)
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = defaultSubFormatByUA(r.UserAgent())
	}
	body := strings.Join(links, "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// Short TTL — expiry/status changes should propagate but we don't want
	// clients hammering. 60s matches common v2ray subscription TTLs.
	w.Header().Set("Cache-Control", "public, max-age=60")
	if format == "base64" {
		body = base64.StdEncoding.EncodeToString([]byte(body))
		w.Header().Set("Content-Disposition", `attachment; filename="sub"`)
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}
	w.Write([]byte(body))
}

// defaultSubFormatByUA picks base64 for known v2ray-family clients (v2rayNG /
// Nekoray / NekoBox / Shadowrocket / sing-box) and raw for everything else
// (curl, browsers — an operator pasting by hand).
func defaultSubFormatByUA(ua string) string {
	ua = strings.ToLower(ua)
	for _, k := range []string{"v2rayng", "nekoray", "nekobox", "shadowrocket", "sing-box"} {
		if strings.Contains(ua, k) {
			return "base64"
		}
	}
	return "raw"
}

// collectUserLinks gathers the per-user share-link/config strings from the
// user's assigned chains, standalone inbounds, and MTProxy nodes. Shared by
// handleSubscription, handleUserConfig, and handleUserQR so all three render
// the identical link set. Pure read — no store writes.
func (s *Server) collectUserLinks(u *model.User, st *chain.Store) []string {
	var links []string

	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		links = append(links, buildConnectionLink(c, u))
	}

	nodes, _ := st.ListNodeInfos()
	for _, node := range nodes {
		for _, ib := range node.Inbounds {
			if contains(ib.ForUsers, u.ID) {
				links = append(links, buildStandaloneLink(node.Addr, ib, u))
			}
		}
	}

	// MTProxy client links (one per node in u.MTProxyNodes).
	if u.MTProxySecret != "" && len(u.MTProxyNodes) > 0 {
		for _, nodeID := range u.MTProxyNodes {
			host, err := st.GetHost(nodeID)
			if err != nil {
				continue
			}
			addr := host.Addr
			if i := strings.Index(addr, ":"); i > 0 {
				addr = addr[:i] // strip the SSH port — MTProxy listens on its own port
			}
			port := 443
			if info, err := st.GetNodeInfo(nodeID); err == nil {
				for _, ib := range info.Inbounds {
					if ib.Protocol == "mtproxy" && ib.Port > 0 {
						port = ib.Port
						break
					}
				}
			}
			fullSecret, err := chain.MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)
			if err != nil {
				continue
			}
			links = append(links, fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", addr, port, fullSecret))
		}
	}

	return links
}

// registerSubscriptionRoute wires the public /sub/{token} endpoint directly on
// the mux WITHOUT s.auth (client apps do not send Basic-Auth). GET passes the
// CSRF middleware automatically (safe-method bypass at csrf.go).
func (s *Server) registerSubscriptionRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /sub/{token}", s.handleSubscription)
}