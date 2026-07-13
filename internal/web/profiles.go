package web

// profiles.go — unified clients page (presets moved to presets.go).
// The dead Profile/ClientAssignment handlers were removed as part of
// subproject C1 (per-protocol presets).

import (
	"net/http"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// ─── Unified clients page ───────────────────────────────────────────────────

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListUsers()
	chains, _ := st.ListChains()
	if users == nil {
		users = []*model.User{}
	}
	// Derive the lifecycle Status for display (active/disabled/expired/on_hold;
	// "limited" needs the P0b-2 poller). Display-only — does not persist, so the
	// auto-deactivate behaviour in handleUsers stays the source of truth for the
	// persisted Active flag. Users without a persisted Status get it derived
	// here so the badge renders correctly for legacy records.
	for _, u := range users {
		if u.Status == "" {
			u.Status = u.ComputeStatus()
		}
	}
	s.renderContent(w, r, i18n.T(r.Context(), "Users"), templates.Users(users, chains))
}
// registerClientRoutes wires the unified clients page (replaces the dead
// Profiles page). Single route. CTO-review §4: split out of server.go Register.
func (s *Server) registerClientRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/clients", s.auth(s.handleClients))
}
