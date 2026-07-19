package web

// dashboard.go — dashboard + host-key trust handlers (extracted from ui.go as
// part of the M11 ui.go split).

import (
	"net/http"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	users, _ := st.ListUsers()
	metrics, _ := st.ListMetrics()
	infos, _ := st.ListNodeInfos()

	hc := computeHealthCounts(metrics)

	// Pending changes: nodes whose rendered config differs from the last
	// deployed hash (or never deployed) — the "needs apply" count.
	pending := 0
	for _, row := range s.computeDeployStatusRows(r) {
		if row.HasPending {
			pending++
		}
	}
	// Recent audit events (last 10 — the full log lives on /ui/audit).
	audit, _ := st.ListAuditLogs(10)

	stats := templates.DashboardStats{
		TotalHosts:     len(hosts),
		OnlineHosts:    hc.Online,
		DownHosts:      hc.Down,
		BlockedHosts:   hc.Blocked,
		TotalChains:    len(chains),
		TotalUsers:     len(users),
		PendingChanges: pending,
	}

	s.renderContent(w, r, i18n.T(r.Context(), "Dashboard"), templates.Dashboard(stats, hosts, metrics, infos, chains, audit))
}

func (s *Server) handleTrustHostKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	addr := r.FormValue("addr")
	fingerprint := strings.TrimSpace(r.FormValue("fingerprint"))

	st := s.store()
	// Verify the submitted fingerprint matches the one the orchestrator
	// actually observed during the failed capture/apply attempt. Without
	// this check, a forged POST (CSRF / social engineering) could trust an
	// arbitrary MITM fingerprint. The pending fingerprint is set in
	// handleCaptureNode when the HostKeyError is rendered. (CTO-review §6.)
	info, _ := st.GetNodeInfo(id)
	pending := ""
	if info != nil {
		pending = info.PendingHostKeyFingerprint
	}
	if pending == "" {
		// No prior observed fingerprint on record — refuse to trust blindly.
		// This happens if the store was wiped between the warning render and
		// the trust POST, or the POST is forged without a preceding capture.
		http.Error(w, i18n.T(r.Context(), "No pending host key fingerprint — re-capture the node first."), http.StatusBadRequest)
		return
	}
	if fingerprint == "" || fingerprint != pending {
		http.Error(w, i18n.T(r.Context(), "Fingerprint does not match the observed host key."), http.StatusBadRequest)
		return
	}
	if addr != "" && fingerprint != "" {
		kh := &model.KnownHost{
			Addr:        addr,
			Fingerprint: fingerprint,
			FirstSeen:   time.Now(),
			Trusted:     true,
		}
		_ = st.SaveKnownHost(kh)
		// Clear the pending fingerprint so it cannot be reused.
		if info != nil {
			info.PendingHostKeyFingerprint = ""
			_ = st.SaveNodeInfo(info)
		}
	}

	// Redirect back to capture form to try again. (HTMX expects HTML or redirect)
	w.Header().Set("HX-Redirect", "/ui/nodes/"+id+"/capture")
	http.Redirect(w, r, "/ui/nodes/"+id+"/capture", http.StatusSeeOther)
}

func (s *Server) handleDashboardStatsHTML(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	users, _ := st.ListUsers()
	metrics, _ := st.ListMetrics()

	hc := computeHealthCounts(metrics)
	stats := templates.DashboardStats{
		TotalHosts:   len(hosts),
		OnlineHosts:  hc.Online,
		DownHosts:    hc.Down,
		BlockedHosts: hc.Blocked,
		TotalChains:  len(chains),
		TotalUsers:   len(users),
	}
	s.render(w, r, templates.StatsCards(stats))
}

// healthCounts tallies node metrics by health state. Online counts healthy
// nodes (State==healthy, or back-compat Online==true with empty State); Down
// counts down + unreachable; Blocked counts operator-marked blocks. Suspect
// + unknown are not surfaced in the dashboard summary (they're transient or
// "no data") — only actionable states get a count.
type healthCounts struct {
	Online  int
	Down   int
	Blocked int
}

func computeHealthCounts(metrics []*model.NodeMetrics) healthCounts {
	var hc healthCounts
	for _, m := range metrics {
		if m == nil {
			continue
		}
		switch templates.NodeState(m) {
		case model.NodeStateHealthy:
			hc.Online++
		case model.NodeStateDown, model.NodeStateUnreachable:
			hc.Down++
		case model.NodeStateBlocked:
			hc.Blocked++
		}
	}
	return hc
}
// registerDashboardRoutes wires the dashboard + the dashboard stats partial
// (HTMX) + the trust-host-key POST. trust-host-key is a node-path route but the
// handler lives here; registered here to keep handler+registration together.
// CTO-review §4: split out of server.go Register.
func (s *Server) registerDashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", s.auth(s.handleDashboard))
	mux.HandleFunc("GET /ui/dashboard/stats", s.auth(s.handleDashboardStatsHTML))
	mux.HandleFunc("POST /ui/nodes/{id}/trust", s.auth(s.handleTrustHostKey))
}
