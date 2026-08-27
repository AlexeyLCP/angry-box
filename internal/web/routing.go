package web

// routing.go — per-node manual routing UI (LucX routing slice 2, 2026-08-27):
// RouteRule CRUD + preset shortcuts + the effective route table (what enters
// where the traffic goes, 3x-ui style). Generation/deploy lives in
// internal/chain/routing.go + applier_push.go.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
	"github.com/alexeylcp/angry-box/web/templates"
)

// registerRoutingRoutes wires the per-node routing panel endpoints.
func (s *Server) registerRoutingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/routing/summary", s.auth(s.handleRoutingSummary))
	mux.HandleFunc("GET /ui/nodes/{id}/routing", s.auth(s.handleNodeRoutingForm))
	mux.HandleFunc("POST /ui/nodes/{id}/routing", s.auth(s.handleAddRouteRule))
	mux.HandleFunc("POST /ui/nodes/{id}/routing/{rid}/delete", s.auth(s.handleDeleteRouteRule))
	mux.HandleFunc("POST /ui/nodes/{id}/routing/{rid}/toggle", s.auth(s.handleToggleRouteRule))
}

// handleRoutingSummary renders the fleet-wide routing overview (every node's
// manual rules) — the spider page's "Routing summary" entry point.
func (s *Server) handleRoutingSummary(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	var rows []templates.RoutingSummaryRow
	for _, h := range hosts {
		rules, _ := st.ListRouteRulesForNode(h.ID)
		rows = append(rows, templates.RoutingSummaryRow{NodeID: h.ID, Rules: rules})
	}
	s.render(w, r, templates.RoutingSummary(rows, chain.GetRoutingPresets("")))
}

// routeRuleView is everything the routing panel renders for one node.
type routeRuleView struct {
	Host      *model.Host
	Rules     []*model.RouteRule
	Users     []*model.User
	Presets   []chain.RoutingPreset
	Effective []templates.EffectiveRouteRow
	Warnings  []string
}

func (s *Server) routingView(nodeID string) (*routeRuleView, error) {
	st := s.store()
	host, err := st.GetHost(nodeID)
	if err != nil {
		return nil, err
	}
	rules, _ := st.ListRouteRulesForNode(nodeID)
	users, _ := st.ListUsers()

	v := &routeRuleView{
		Host:    host,
		Rules:   rules,
		Users:   users,
		Presets: chain.GetRoutingPresets(""),
	}

	// Effective route table: render the node's merged config (deploy-equivalent)
	// and flatten its route section into human rows.
	info, _ := st.GetNodeInfo(nodeID)
	if info != nil {
		chainsForNode, _ := st.GetChainsForNode(nodeID)
		if cfg, _, err := chain.RenderMergedNodeConfigStore(st, info, chainsForNode); err == nil && cfg.Route != nil {
			for _, r := range cfg.Route.Rules {
				v.Effective = append(v.Effective, templates.EffectiveRouteRow{
					Match:  describeRouteMatch(r),
					Action: describeRouteAction(r),
				})
			}
		}
	}
	return v, nil
}

// describeRouteMatch summarizes a sing-box route rule's matcher fields into a
// compact human string for the effective-route table.
func describeRouteMatch(r config.RouteRuleEntry) string {
	var parts []string
	add := func(name string, vals []string) {
		if len(vals) > 0 {
			parts = append(parts, name+": "+strings.Join(vals, ", "))
		}
	}
	add("inbound", r.Inbound)
	add("domain", r.Domain)
	add("domain_suffix", r.DomainSuffix)
	add("domain_keyword", r.DomainKeyword)
	add("ip_cidr", r.IPCidr)
	add("source_ip_cidr", r.SourceIPCIDR)
	add("auth_user", r.AuthUser)
	add("protocol", r.Protocol)
	add("rule_set", r.RuleSet)
	if len(parts) == 0 {
		return "any"
	}
	return strings.Join(parts, " · ")
}

// describeRouteAction summarizes what a route rule does with matched traffic.
func describeRouteAction(r config.RouteRuleEntry) string {
	switch r.Action {
	case "sniff":
		return "sniff"
	case "hijack-dns":
		return "hijack-dns"
	case "reject":
		return "reject"
	case "direct":
		return "direct"
	}
	if r.Outbound != "" {
		return "→ " + r.Outbound
	}
	return "→ (final)"
}

func (s *Server) handleNodeRoutingForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, err := s.routingView(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "node not found"), http.StatusNotFound)
		return
	}
	s.render(w, r, templates.NodeRoutingPanel(v.Host, v.Rules, v.Users, v.Presets, v.Effective))
}

func (s *Server) handleAddRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	matchType := strings.TrimSpace(r.FormValue("match_type"))
	matchValues := strings.TrimSpace(r.FormValue("match_values"))
	if matchType == "preset" {
		matchValues = strings.TrimSpace(r.FormValue("preset_id"))
	}
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "direct", "reject", "route":
	case "":
		action = "direct"
	default:
		http.Error(w, i18n.T(ctx, "unsupported action"), http.StatusBadRequest)
		return
	}
	prio, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("priority")))

	rule := &model.RouteRule{
		NodeID:      id,
		Priority:    prio,
		MatchType:   matchType,
		MatchValues: matchValues,
		Action:      action,
		OutboundTag: strings.TrimSpace(r.FormValue("outbound_tag")),
		UserIDs:     cleanIDs(r.Form["user_ids"]),
		Comment:     strings.TrimSpace(r.FormValue("comment")),
		Enabled:     true,
	}
	// Fail fast on garbage input (unknown preset, empty values, bad geo name)
	// so the operator sees the reason instead of a deploy warning later.
	st := s.store()
	users, _ := st.ListUsers()
	if warns := chain.ValidateRouteRule(rule, derefUsers(users)); len(warns) > 0 {
		http.Error(w, strings.Join(warns, "; "), http.StatusBadRequest)
		return
	}

	if err := st.SaveRouteRule(rule); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "create", "route_rule", rule.ID, chain.AuditPayload{"node": id, "match": matchType, "action": action}, "operator")
	chain.ScheduleAutoApply(id, "route-rule-add")
	s.rerenderRoutingPanel(w, r, id)
}

func (s *Server) handleDeleteRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rid := r.PathValue("rid")
	st := s.store()
	if err := st.DeleteRouteRule(rid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "delete", "route_rule", rid, chain.AuditPayload{"node": id}, "operator")
	chain.ScheduleAutoApply(id, "route-rule-delete")
	s.rerenderRoutingPanel(w, r, id)
}

func (s *Server) handleToggleRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rid := r.PathValue("rid")
	st := s.store()
	rules, err := st.ListRouteRulesForNode(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, rule := range rules {
		if rule.ID != rid {
			continue
		}
		rule.Enabled = !rule.Enabled
		if err := st.SaveRouteRule(rule); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		chain.WriteAudit(st, "update", "route_rule", rid, chain.AuditPayload{"node": id, "enabled": rule.Enabled}, "operator")
		chain.ScheduleAutoApply(id, "route-rule-toggle")
		break
	}
	s.rerenderRoutingPanel(w, r, id)
}

// rerenderRoutingPanel re-renders the whole routing modal into
// #modal-container (the panel's sub-forms target it).
func (s *Server) rerenderRoutingPanel(w http.ResponseWriter, r *http.Request, nodeID string) {
	v, err := s.routingView(nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, templates.NodeRoutingPanel(v.Host, v.Rules, v.Users, v.Presets, v.Effective))
}

func cleanIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func derefUsers(users []*model.User) []model.User {
	out := make([]model.User, 0, len(users))
	for _, u := range users {
		if u != nil {
			out = append(out, *u)
		}
	}
	return out
}
