package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/web/templates"
)

// Server provides the HTMX web UI following community patterns
// (Pagoda, TemplUI, go-htmx-starters): sidebar nav, HTMX-driven content swaps,
// DaisyUI components, no heavy JS frameworks.
type Server struct {
	storePath string
}

// NewServer creates a UI server bound to the given JSON store file.
func NewServer(storePath string) *Server {
	return &Server{storePath: storePath}
}

// Register wires all UI routes onto the provided mux.
// Call this from serveCmd after loading config.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", s.handleDashboard)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
	})

	// Hosts (real implementation with HTMX CRUD)
	mux.HandleFunc("GET /ui/hosts", s.handleHosts)
	mux.HandleFunc("POST /ui/hosts", s.handleCreateHost)
	mux.HandleFunc("DELETE /ui/hosts/{id}", s.handleDeleteHost)
	mux.HandleFunc("GET /ui/hosts/new", s.handleNewHostForm)

	// Other sections (stubs for now, same navigation pattern)
	mux.HandleFunc("GET /ui/chains", s.handleChains)
	mux.HandleFunc("GET /ui/status", s.handleStatus)
}

func (s *Server) store() *chain.Store {
	return chain.NewStore(s.storePath)
}

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// render writes a templ.Component (fragment or full document).
func (s *Server) render(w http.ResponseWriter, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(context.Background(), w); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// renderContent is a convenience for pages that support both full load and HTMX fragment.
func (s *Server) renderContent(w http.ResponseWriter, r *http.Request, title string, content templ.Component) {
	if isHTMXRequest(r) {
		s.render(w, content)
		return
	}
	s.render(w, templates.Base(title, content))
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.renderContent(w, r, "Dashboard", templates.Index())
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts() // ignore error for UI (empty list on failure is ok for now)
	s.renderContent(w, r, "Hosts", templates.Hosts(hosts))
}

func (s *Server) handleNewHostForm(w http.ResponseWriter, r *http.Request) {
	// This is always a fragment (loaded into modal-container)
	s.render(w, templates.NewHostForm())
}

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	id := strings.TrimSpace(r.FormValue("id"))
	addr := strings.TrimSpace(r.FormValue("addr"))
	user := strings.TrimSpace(r.FormValue("user"))
	if user == "" {
		user = "root"
	}
	keyPath := strings.TrimSpace(r.FormValue("keyPath"))

	if id == "" || addr == "" || keyPath == "" {
		http.Error(w, "id, addr and keyPath are required", http.StatusBadRequest)
		return
	}

	h := &model.Host{
		ID:      id,
		Addr:    addr,
		User:    user,
		KeyPath: keyPath,
	}

	st := s.store()
	if err := st.SaveHost(h); err != nil {
		http.Error(w, fmt.Sprintf("save failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return the new row HTML so the form's hx-swap="beforeend" on #hosts-tbody works.
	// Also works for OOB if we later enhance.
	s.render(w, templates.HostRow(h))
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	st := s.store()
	if err := st.DeleteHost(id); err != nil {
		// Still return 200 so the row is removed from DOM (or show error toast later)
		// For simplicity we just log via header.
		w.Header().Set("X-Error", err.Error())
	}

	// The calling button does hx-swap="outerHTML" on the row, so we can return
	// nothing (or a zero-height element). 204 is clean for delete.
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	// Temporary stub – will be replaced with a real templates.Chains component
	// when we implement the chain management UI (visual editor + apply from web).
	frag := &simpleHTML{html: `<div class="space-y-4"><h2 class="text-2xl font-semibold mb-2">Chains</h2><p class="text-sm opacity-70">Chain builder (create, visualise, apply-chain) coming in the next step.</p></div>`}
	s.renderContent(w, r, "Chains", frag)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	frag := &simpleHTML{html: `<div class="space-y-4"><h2 class="text-2xl font-semibold mb-2">System Status</h2><p class="text-sm opacity-70">Live host/proxy status (HTMX polling every 30s + on-demand pull) will appear here.</p></div>`}
	s.renderContent(w, r, "Status", frag)
}

// simpleHTML lets us return ad-hoc HTML fragments while staying inside the templ.Component interface.
// Only used for the temporary stub pages.
type simpleHTML struct {
	html string
}

func (s *simpleHTML) Render(ctx context.Context, w io.Writer) error {
	_, err := io.WriteString(w, s.html)
	return err
}

// Compile-time interface check.
var _ templ.Component = (*simpleHTML)(nil)
