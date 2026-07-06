package web

// profiles.go — unified clients page (presets moved to presets.go).
// The dead Profile/ClientAssignment handlers were removed as part of
// subproject C1 (per-protocol presets).

import (
	"net/http"

	"github.com/alexeylcp/angry-box/internal/domain/model"
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
	s.render(w, r, templates.Users(users, chains))
}
// registerClientRoutes wires the unified clients page (replaces the dead
// Profiles page). Single route. CTO-review §4: split out of server.go Register.
func (s *Server) registerClientRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/clients", s.auth(s.handleClients))
}
