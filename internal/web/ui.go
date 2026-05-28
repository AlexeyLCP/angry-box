package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/backend/factory"
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
	mux.HandleFunc("GET /ui/hosts/{id}/status", s.handleHostStatus)

	// Chains (real implementation)
	mux.HandleFunc("GET /ui/chains", s.handleChains)
	mux.HandleFunc("POST /ui/chains", s.handleCreateChain)
	mux.HandleFunc("DELETE /ui/chains/{name}", s.handleDeleteChain)
	mux.HandleFunc("POST /ui/chains/{name}/apply", s.handleApplyChain)
	mux.HandleFunc("GET /ui/chains/new", s.handleNewChainForm)

	// Status (still stub)
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
		http.Error(w, fmt.Sprintf("failed to delete host: %v", err), http.StatusInternalServerError)
		return
	}

	// Success: tell HTMX (with hx-swap="delete") to remove the row
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	chains, _ := st.ListChains()
	hosts, _ := st.ListHosts()
	s.renderContent(w, r, "Chains", templates.Chains(chains, hosts))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()

	var content templ.Component
	if len(hosts) == 0 {
		content = &simpleHTML{html: `<div class="text-base-content/70">No hosts registered yet.</div>`}
	} else {
		content = templates.StatusPage(hosts) // we'll add this template
	}
	s.renderContent(w, r, "Status", content)
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

// ─── Chain handlers ───────────────────────────────────────────────────────────

func (s *Server) handleNewChainForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	profiles := chain.ListPresets()
	s.render(w, templates.NewChainForm(hosts, profiles))
}

func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	strategy := strings.TrimSpace(r.FormValue("strategy"))
	if strategy == "" {
		strategy = "urltest"
	}

	transport := model.TransportType(strings.TrimSpace(r.FormValue("transport")))
	if transport == "" {
		transport = model.TransportXHTTP
	}
	userProto := model.UserProtocol(strings.TrimSpace(r.FormValue("user_protocol")))
	if userProto == "" {
		userProto = model.UserProtocolAWG
	}
	profile := strings.TrimSpace(r.FormValue("profile"))

	// Collect selected nodes (supports multiple checkboxes)
	nodeIDs := r.Form["nodes"]
	if len(nodeIDs) == 0 {
		nodeIDs = r.PostForm["nodes"]
	}
	// Dedup while preserving order
	seen := map[string]bool{}
	uniqueNodes := []string{}
	for _, id := range nodeIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			uniqueNodes = append(uniqueNodes, id)
		}
	}
	nodeIDs = uniqueNodes

	if name == "" || len(nodeIDs) < 1 {
		http.Error(w, "name and at least one node are required", http.StatusBadRequest)
		return
	}

	st := s.store()

	// Build ordered ChainNodes by resolving hosts (preserve selection order).
	nodes := make([]model.ChainNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		id = strings.TrimSpace(id)
		h, err := st.GetHost(id)
		if err != nil {
			http.Error(w, fmt.Sprintf("host %q not found", id), http.StatusBadRequest)
			return
		}
		nodes = append(nodes, model.ChainNode{
			ID:      h.ID,
			Addr:    h.Addr,
			User:    h.User,
			KeyPath: h.KeyPath,
		})
	}

	c := &model.Chain{
		Name:               name,
		Nodes:              nodes,
		Strategy:           model.Strategy(strategy),
		Transport:          transport,
		UserProtocol:       userProto,
		ObfuscationProfile: profile,
	}

	// Stable user-entry creds for AWG/TUIC are primarily generated at chain creation via CLI
	// for the "works like clockwork" guarantee. UI creation falls back to generation on first apply.
	_ = userProto // reserved for future full parity with CLI creation flow

	if err := st.SaveChain(c); err != nil {
		http.Error(w, fmt.Sprintf("save failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return the new row so it appends to the table.
	s.render(w, templates.ChainRow(c))
}

func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	st := s.store()
	if err := st.DeleteChain(name); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete chain: %v", err), http.StatusInternalServerError)
		return
	}

	// Success: HTMX with hx-swap="delete" will remove the row
	w.WriteHeader(http.StatusNoContent)
}

// ─── Host status (live check via SSH) ─────────────────────────────────────────

func (s *Server) handleHostStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		s.render(w, &simpleHTML{html: `<span class="text-error text-xs">Host not found</span>`})
		return
	}

	f := factory.New()
	b := f.Create()

	ctx := context.Background()
	status, err := b.GetStatus(ctx, *host)
	if err != nil {
		s.render(w, &simpleHTML{html: `<span class="badge badge-error badge-sm">Error</span>`})
		return
	}

	s.render(w, templates.HostStatus(status))
}

func (s *Server) handleApplyChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		s.render(w, templates.ApplyResult(name, false, nil, "chain not found"))
		return
	}

	// Resolve full connection info for SSH.
	resolved, err := st.ResolveNodes(c)
	if err != nil {
		s.render(w, templates.ApplyResult(name, false, nil, err.Error()))
		return
	}
	c.Nodes = resolved

	// Execute the real applier (same logic as CLI apply-chain).
	f := factory.New()
	applier := chain.NewApplier(f)

	ctx := context.Background()
	// Pass empty clientPubKey — for AWG chains the applier will auto-generate a usable sample.
	report, err := applier.ApplyChain(ctx, c, "")
	if err != nil {
		// Include some detail from the report if available (per-node failures)
		msg := err.Error()
		if report != nil && len(report.Nodes) > 0 {
			for _, n := range report.Nodes {
				if !n.Success && n.Error != "" {
					msg += " | " + n.ID + ": " + n.Error
				}
			}
		}
		s.render(w, templates.ApplyResult(name, false, report, msg))
		return
	}

	if report != nil && len(report.Nodes) > 0 {
		// For rich display we pass the report (template supports it)
		s.render(w, templates.ApplyResult(name, true, report, ""))
	} else {
		s.render(w, templates.ApplyResult(name, true, report, ""))
	}
}
