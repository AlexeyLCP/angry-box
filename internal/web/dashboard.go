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

	// Build stats
	onlineCount := 0
	for _, m := range metrics {
		if m.Online {
			onlineCount++
		}
	}

	stats := templates.DashboardStats{
		TotalHosts:  len(hosts),
		OnlineHosts: onlineCount,
		TotalChains: len(chains),
		TotalUsers:  len(users),
	}

	s.renderContent(w, r, i18n.T(r.Context(), "Dashboard"), templates.Dashboard(stats, hosts, metrics, infos, chains))
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

	online := 0
	for _, m := range metrics {
		if m.Online {
			online++
		}
	}
	stats := templates.DashboardStats{
		TotalHosts:  len(hosts),
		OnlineHosts: online,
		TotalChains: len(chains),
		TotalUsers:  len(users),
	}
	s.render(w, r, templates.StatsCards(stats))
}