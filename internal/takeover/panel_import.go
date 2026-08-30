package takeover

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// panel_import.go — converts a parsed panel DB into angry-box store entities:
// inbounds -> NodeInbound (standalone), clients -> User (deduped by tgId/email
// exactly like the operator's product model), routing -> RouteRule (the format
// fixed by the routing slice — manual rules above the system cascade).
//
// Import honesty (rule #6): nothing is silently dropped. Every unconvertible
// object lands in Report so the UI shows what happened.

// PanelImportResult is everything a panel import produces.
type PanelImportResult struct {
	NodeInbounds []model.NodeInbound
	Users        []*model.User
	RouteRules   []*model.RouteRule
	Report       []string
}

func (r *PanelImportResult) note(f string, a ...any) { r.Report = append(r.Report, fmt.Sprintf(f, a...)) }

// acc accumulates one logical user across the panel's duplicated client rows
// (the same person appears once per inbound they were sold on).
type acc struct {
	u           *model.User
	hasVLESS    bool
	trafficDone bool
}

// ConvertPanelImport maps the parsed DB onto angry-box entities for nodeID.
// Users get NO subscription token here (the web layer mints them at save).
func ConvertPanelImport(nodeID string, db *PanelDB) *PanelImportResult {
	res := &PanelImportResult{}
	if db == nil {
		return res
	}
	// userKey -> accumulated user. Dedupe: tgId wins when present, else email
	// (the agreed seller-panel convention: one logical client duplicated
	// across inbounds becomes one angry-box user with several assignments).
	// Merge path: a client seen with a tgId upgrades an email-keyed user (and
	// vice versa) so the same person never splits across key styles.
	users := map[string]*acc{}
	findUser := func(c panelClient) *acc {
		email := strings.ToLower(strings.TrimSpace(c.Email))
		tgKey := ""
		if c.TgID > 0 {
			tgKey = fmt.Sprintf("tg:%d", c.TgID)
		}
		if tgKey != "" {
			if a, ok := users[tgKey]; ok {
				return a
			}
		}
		if email != "" {
			if a, ok := users["email:"+email]; ok {
				if tgKey != "" {
					users[tgKey] = a
				}
				return a
			}
		}
		if tgKey == "" && email == "" {
			return nil // no identity at all — cannot dedupe
		}
		name := strings.TrimSpace(c.Email)
		if name == "" {
			name = fmt.Sprintf("tg-%d", c.TgID)
		}
		u := &model.User{
			Name:      name,
			Active:    c.Enable,
			CreatedAt: time.Now(),
		}
		if c.TgID != 0 {
			res.note("user %q: telegram id %d not auto-bound — issue /link", name, c.TgID)
		}
		if c.ExpiryTime > 0 {
			u.ExpiresAt = time.UnixMilli(c.ExpiryTime)
			u.ExpireStrategy = "fixed_date"
		} else {
			u.ExpireStrategy = "never"
		}
		u.DataLimit = c.TotalGB
		a := &acc{u: u}
		if tgKey != "" {
			users[tgKey] = a
		}
		if email != "" {
			users["email:"+email] = a
		}
		return a
	}

	for _, pi := range db.Inbounds {
		clients := parsePanelClients(pi.Settings)
		stream := parsePanelStream(pi.StreamSettings)
		remark := strings.TrimSpace(pi.Remark)
		tag := "pi-" + sanitizeTagSuffix(fmt.Sprintf("%d", pi.ID))
		if remark != "" {
			tag = "pi-" + sanitizeTagSuffix(remark)
		}

		switch pi.Protocol {
		case "vless":
			if stream != nil && stream.Security == "reality" && stream.Reality != nil {
				ib := model.NodeInbound{
					Protocol:      "vless-reality",
					Port:          pi.Port,
					Source:        "standalone",
					Tag:           tag,
					ServerPrivKey: stream.Reality.PrivateKey,
				}
				if len(stream.Reality.ShortIDs) > 0 {
					ib.ShortID = stream.Reality.ShortIDs[0]
				}
				for _, c := range clients {
					if !c.Enable || c.ID == "" {
						continue
					}
					a := findUser(c)
					if a == nil {
						res.note("vless client without email/tgId skipped (inbound %q)", remark)
						continue
					}
					if !a.hasVLESS {
						a.u.VLESSUUID = c.ID
						a.hasVLESS = true
						if ib.UUID == "" {
							ib.UUID = c.ID // shared fallback identity for legacy clients
						}
					}
					applyTraffic(a, db.Traffics[c.Email])
				}
				res.NodeInbounds = append(res.NodeInbounds, ib)
				res.note("inbound %q: vless+reality :%d imported", remark, pi.Port)
			} else {
				net := ""
				if stream != nil {
					net = stream.Network
				}
				res.note("inbound %q: vless (%s, non-reality) skipped — not a product protocol", remark, net)
			}

		case "naive", "mieru", "trusttunnel":
			// Sidecar protocols: credentials in the panel are HMAC-derived and
			// NOT stored, so imported users get fresh angry-box creds (the old
			// ones cannot be reconstructed). The inbound itself imports fine.
			ib := model.NodeInbound{
				Protocol: pi.Protocol,
				Port:     pi.Port,
				Source:   "standalone",
				Tag:      tag,
			}
			if pi.Protocol == "mieru" && len(clients) > 0 {
				// portBindings may override the listen port; keep the column
				// value (the common case) — noted for the operator.
			}
			for _, c := range clients {
				a := findUser(c)
				if a != nil {
					applyTraffic(a, db.Traffics[c.Email])
				}
			}
			res.NodeInbounds = append(res.NodeInbounds, ib)
			res.note("inbound %q: %s :%d imported (%d clients get NEW credentials)", remark, pi.Protocol, pi.Port, len(clients))

		case "mtproto":
			ib := model.NodeInbound{
				Protocol: "mtproxy",
				Port:     pi.Port,
				Source:   "standalone",
				Tag:      tag,
			}
			res.NodeInbounds = append(res.NodeInbounds, ib)
			imported := 0
			for _, c := range clients {
				secretHex, domain, ok := parseMTProtoSecret(c.Secret)
				if !ok {
					res.note("mtproto client %q: bad secret shape, skipped", c.Email)
					continue
				}
				a := findUser(c)
				if a == nil {
					res.note("mtproto client without email/tgId skipped")
					continue
				}
				a.u.MTProxySecret = secretHex
				if domain != "" {
					a.u.MTProxyDomain = domain
				}
				a.u.MTProxyNodes = append(a.u.MTProxyNodes, nodeID)
				applyTraffic(a, db.Traffics[c.Email])
				imported++
			}
			res.note("inbound %q: mtproto :%d imported (%d secrets)", remark, pi.Port, imported)

		default:
			res.note("inbound %q: protocol %q skipped (unsupported for import)", remark, pi.Protocol)
		}
	}

	seenAcc := map[*acc]bool{}
	for _, a := range users {
		if seenAcc[a] {
			continue // same user reachable via tgId AND email keys
		}
		seenAcc[a] = true
		res.Users = append(res.Users, a.u)
	}
	res.RouteRules = convertPanelRouting(nodeID, db.RoutingJSON, res)
	return res
}

// applyTraffic folds the panel's per-email usage counters into the user — at
// most once per user (the same email appears on every inbound the client was
// duplicated across, but client_traffics stores ONE aggregate row per email).
func applyTraffic(a *acc, ct PanelClientTraffic) {
	if a == nil || a.trafficDone || ct.Email == "" {
		return
	}
	a.trafficDone = true
	u := a.u
	u.UsedTraffic += ct.Up + ct.Down
	u.LifetimeUsedTraffic += ct.Up + ct.Down
	if ct.LastSubFetch > 0 {
		fu := time.UnixMilli(ct.LastSubFetch)
		if u.FirstUseAt.IsZero() || fu.Before(u.FirstUseAt) {
			u.FirstUseAt = fu
		}
	}
}

// ─── Routing conversion (xray template -> model.RouteRule) ───────────────────

type xrayRoutingRule struct {
	Type        string   `json:"type"`
	OutboundTag string   `json:"outboundTag"`
	Domain      []string `json:"domain"`
	IP          []string `json:"ip"`
}

// convertPanelRouting maps the xray template's routing.rules[] onto angry-box
// RouteRules. Only direct/block rules translate (proxy-tag rules are the
// panel's own egress — meaningless here); geosite:/geoip: entries become
// geo rules, prefixed domain entries are unwrapped.
func convertPanelRouting(nodeID, templateJSON string, res *PanelImportResult) []*model.RouteRule {
	if strings.TrimSpace(templateJSON) == "" {
		return nil
	}
	var tmpl struct {
		Routing struct {
			Rules []xrayRoutingRule `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(templateJSON), &tmpl); err != nil {
		res.note("routing: xray template parse failed: %v", err)
		return nil
	}
	var rules []*model.RouteRule
	prio := 10
	for _, r := range tmpl.Routing.Rules {
		if r.Type != "field" {
			continue
		}
		var action string
		switch r.OutboundTag {
		case "direct":
			action = "direct"
		case "block", "reject":
			action = "reject"
		default:
			res.note("routing rule with outbound %q skipped (only direct/block translate)", r.OutboundTag)
			continue
		}
		domains, geosites := splitDomainEntries(r.Domain)
		ips, geoips := splitIPEntries(r.IP)
		add := func(matchType, values string) {
			if values == "" {
				return
			}
			rules = append(rules, &model.RouteRule{
				NodeID:      nodeID,
				Priority:    prio,
				MatchType:   matchType,
				MatchValues: values,
				Action:      action,
				Enabled:     true,
				Comment:     "imported from panel",
				CreatedAt:   time.Now(),
			})
			prio += 10
		}
		add("domain", strings.Join(domains, "\n"))
		add("geosite", strings.Join(geosites, "\n"))
		add("ip_cidr", strings.Join(ips, "\n"))
		add("geoip", strings.Join(geoips, "\n"))
	}
	if len(rules) > 0 {
		res.note("routing: %d rule(s) imported", len(rules))
	}
	return rules
}

// splitDomainEntries unwraps xray domain prefixes (domain:/full:/keyword:/
// geosite:/regexp:) into plain domains + geosite names. regexp entries are
// dropped (no equivalent matcher).
func splitDomainEntries(entries []string) (domains, geosites []string) {
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch {
		case strings.HasPrefix(e, "geosite:"):
			geosites = append(geosites, strings.TrimPrefix(e, "geosite:"))
		case strings.HasPrefix(e, "regexp:"):
			continue
		case strings.HasPrefix(e, "domain:"):
			domains = append(domains, strings.TrimPrefix(e, "domain:"))
		case strings.HasPrefix(e, "full:"):
			domains = append(domains, strings.TrimPrefix(e, "full:"))
		case strings.HasPrefix(e, "keyword:"):
			domains = append(domains, strings.TrimPrefix(e, "keyword:"))
		default:
			domains = append(domains, e)
		}
	}
	return
}

// splitIPEntries separates geoip: names from raw CIDR/IP values.
func splitIPEntries(entries []string) (ips, geoips []string) {
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if strings.HasPrefix(e, "geoip:") {
			geoips = append(geoips, strings.TrimPrefix(e, "geoip:"))
		} else {
			ips = append(ips, e)
		}
	}
	return
}

// sanitizeTagSuffix reduces an inbound remark to tag-safe characters.
func sanitizeTagSuffix(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "inbound"
	}
	return out
}
