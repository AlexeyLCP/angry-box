package web

// dashboard.go — dashboard + host-key trust handlers (extracted from ui.go as
// part of the M11 ui.go split).

import (
	"net/http"
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
	fingerprint := r.FormValue("fingerprint")

	if addr != "" && fingerprint != "" {
		st := s.store()
		kh := &model.KnownHost{
			Addr:        addr,
			Fingerprint: fingerprint,
			FirstSeen:   time.Now(),
			Trusted:     true,
		}
		_ = st.SaveKnownHost(kh)
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