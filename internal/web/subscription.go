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
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/awg/vpnuri"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// setSubInfoHeaders emits the subscription metadata headers client apps read:
// Profile-Title (shown as the subscription name) and Subscription-Userinfo
// (upload/download/total/expiry — v2rayNG/Hiddify/SFA render usage + expiry
// from it). Traffic: AWG byte counters (the only per-user stats we collect —
// sing-box has no xray-style per-user counters); quota: DataLimit (0 =
// unlimited); expire: ExpiresAt unix (0 = no expiry).
func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, s)
}

func setSubInfoHeaders(w http.ResponseWriter, u *model.User) {
	w.Header().Set("Profile-Title", headerSafe(u.Name))
	up, down := u.AWGTxBytes, u.AWGRxBytes
	if u.UsedTraffic > 0 && up+down == 0 {
		// P0b poller stats without an AWG split — attribute to download.
		down = u.UsedTraffic
	}
	expire := int64(0)
	if !u.ExpiresAt.IsZero() {
		expire = u.ExpiresAt.Unix()
	}
	total := u.DataLimit
	w.Header().Set("Subscription-Userinfo",
		fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", up, down, total, expire))
}

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
		if wantsHTMLSub(r) {
			format = "html"
		} else {
			format = defaultSubFormatByUA(r.UserAgent())
		}
	}
	// Client-app metadata headers (the 3x-ui convention): subscription apps
	// (v2rayNG / Hiddify / SFA...) surface the profile title and the usage /
	// expiry counters in their own UI from Subscription-Userinfo.
	setSubInfoHeaders(w, u)
	w.Header().Set("Cache-Control", "private, no-store")
	switch format {
	case "vpn":
		body := strings.Join(vpnLinksFrom(links), "\n")
		if body == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "inline")
		w.Write([]byte(body))
	case "clash", "clashmeta", "mihomo":
		body := buildClashYAML(u.Name, links)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clash.yaml"`)
		w.Write([]byte(body))
	case "singbox", "sing-box", "sfa", "sfi":
		body, err := buildUserSingboxJSON(u, links)
		if err != nil || body == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="config.json"`)
		w.Write([]byte(body))
	case "html":
		settings, _ := st.GetSettings()
		lang := "en"
		if settings != nil && settings.Language != "" {
			lang = settings.Language
		}
		ctx := context.WithValue(r.Context(), i18n.LangKey, lang)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.SubPage(u.Name, links, vpnLinksFrom(links)).Render(ctx, w)
	case "base64":
		body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="sub"`)
		w.Write([]byte(body))
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "inline")
		w.Write([]byte(strings.Join(links, "\n")))
	}
}

func wantsHTMLSub(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if !strings.Contains(accept, "text/html") {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	for _, k := range []string{"v2rayng", "nekoray", "nekobox", "shadowrocket", "sing-box", "clash", "hiddify"} {
		if strings.Contains(ua, k) {
			return false
		}
	}
	return strings.Contains(ua, "mozilla")
}

func vpnLinksFrom(links []string) []string {
	var out []string
	for _, l := range links {
		if !vpnuri.IsAWGConf(l) {
			continue
		}
		uri, err := vpnuri.EncodeConf(l)
		if err != nil {
			continue
		}
		out = append(out, uri)
	}
	return out
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
func persistUserCreds(st *chain.Store, u *model.User) {
	if u == nil || st == nil {
		return
	}
	before := u.NaivePassword + u.MieruPassword + u.AWGPresharedKey
	_ = chain.EnsureUserCreds(u)
	if u.NaivePassword+u.MieruPassword+u.AWGPresharedKey != before {
		_ = st.SaveUser(u)
	}
}

func (s *Server) collectUserLinks(u *model.User, st *chain.Store) []string {
	persistUserCreds(st, u)
	var links []string

	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		links = append(links, buildConnectionLink(st, c, u))
	}

	nodes, _ := st.ListNodeInfos()
	ensureStandaloneAWGMaterial(st, nodes)
	for _, node := range nodes {
		for i, ib := range node.Inbounds {
			if chain.IsChainSourcedInbound(&ib) {
				continue // chain-entry materialization — served via the chain link above
			}
			if ib.Protocol == "naive" || ib.Protocol == "mieru" || ib.Protocol == "trusttunnel" || contains(ib.ForUsers, u.ID) {
				links = append(links, buildStandaloneLinkFor(node, i, u))
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
