package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/internal/takeover"
	webassets "github.com/alexeylcp/angry-box/web"
	"github.com/alexeylcp/angry-box/web/templates"
)

// Server provides the HTMX web UI.
type Server struct {
	storePath        string
	stopCh           chan struct{}
	devMode          bool
	cfg              *config.Config
	ActiveListenAddr string
	// factory is the composition-root dependency for creating proxy backends.
	// Injected once at construction (NewServer) instead of ad-hoc factory.New()
	// scattered across handlers (CTO-review M11).
	factory ports.Factory
}

// NewServer creates a web UI server.
// If devMode is true, static files are served from web/static/ instead of the embedded filesystem.
// f is the composition-root factory used by all deploy/apply handlers.
func NewServer(storePath string, devMode bool, cfg *config.Config, activeListenAddr string, f ports.Factory) *Server {
	if devMode {
		log.Println("[dev] Loading UI from filesystem (web/static/)")
	} else {
		log.Println("[prod] Loading embedded UI")
	}
	if f == nil {
		f = factory.New()
	}
	return &Server{storePath: storePath, stopCh: make(chan struct{}), devMode: devMode, cfg: cfg, ActiveListenAddr: activeListenAddr, factory: f}
}

// staticFS returns the filesystem to use for static assets.
func (s *Server) staticFS() (fs.FS, error) {
	if s.devMode {
		// Find web/static/ relative to CWD or module root
		dirs := []string{"web/static", "../web/static", "../../web/static"}
		for _, d := range dirs {
			if info, err := os.Stat(d); err == nil && info.IsDir() {
				log.Printf("[dev] Serving static files from %s", d)
				return os.DirFS(d), nil
			}
		}
		return nil, fmt.Errorf("web/static/ not found in any of %v (run from project root)", dirs)
	}
	// Production: use embedded filesystem
	sub, err := fs.Sub(webassets.StaticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("embedded static: %w", err)
	}
	return sub, nil
}

// StartBackgroundMetrics begins periodic metrics collection.
// interval is in minutes. Call Stop() to halt.
func (s *Server) StartBackgroundMetrics(intervalMinutes int) {
	if intervalMinutes <= 0 {
		intervalMinutes = 15 // default 15 minutes
	}
	// Collect immediately on startup, then periodically.
	go func() {
		s.collectAllMetrics()
		ticker := time.NewTicker(time.Duration(intervalMinutes) * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.collectAllMetrics()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop halts background collection.
func (s *Server) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// collectAllMetrics checks all hosts and records their status.
func (s *Server) collectAllMetrics() {
	st := s.store()
	hosts, _ := st.ListHosts()
	f := s.factory
	b := f.Create()
	ctx := context.Background()

	for _, h := range hosts {
		start := time.Now()

		status, err := b.GetStatus(ctx, *h)

		latency := time.Since(start).Milliseconds()
		if err != nil {
			st.SaveMetrics(&model.NodeMetrics{HostID: h.ID, Online: false, LatencyMs: latency})
			continue
		}
		st.SaveMetrics(&model.NodeMetrics{
			HostID:    h.ID,
			Online:    status.Running,
			Version:   status.Version,
			LatencyMs: latency,
		})
	}
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return BasicAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, _ := s.store().GetSettings()
		lang := settings.Language
		if lang == "" {
			lang = "en"
		}
		ctx := context.WithValue(r.Context(), i18n.LangKey, lang)
		h(w, r.WithContext(ctx))
	}), s.cfg)
}

func (s *Server) Register(mux *http.ServeMux) {
	// Static files (CSS, JS, images) — from disk in dev, from embed in prod
	staticFS, err := s.staticFS()
	if err != nil {
		log.Printf("WARNING: static files unavailable: %v", err)
	} else {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	mux.HandleFunc("GET /ui", s.auth(s.handleDashboard))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
	})

	// Dashboard stats partial (HTMX, used by the dashboard template).
	mux.HandleFunc("GET /ui/dashboard/stats", s.auth(s.handleDashboardStatsHTML))

	// Hosts (kept for backward compat, redirect to nodes)
	mux.HandleFunc("GET /ui/hosts", s.auth(s.handleNodes))
	// Hosts (kept for backward compat: status endpoint is used by node/chain tables).
	mux.HandleFunc("GET /ui/hosts", s.auth(s.handleNodes))
	mux.HandleFunc("GET /ui/hosts/{id}/status", s.auth(s.handleHostStatus))

	// Nodes (new CRUD)
	mux.HandleFunc("GET /ui/nodes", s.auth(s.handleNodes))
	mux.HandleFunc("POST /ui/nodes", s.auth(s.handleCreateNode))
	mux.HandleFunc("GET /ui/nodes/new", s.auth(s.handleNewNodeForm))
	mux.HandleFunc("GET /ui/nodes/{id}/edit", s.auth(s.handleEditNodeForm))
	mux.HandleFunc("POST /ui/nodes/{id}/edit", s.auth(s.handleUpdateNode))
	mux.HandleFunc("DELETE /ui/nodes/{id}", s.auth(s.handleDeleteNode))
	mux.HandleFunc("POST /ui/nodes/{id}/capture", s.auth(s.handleCaptureNode))
	mux.HandleFunc("GET /ui/nodes/{id}/capture", s.auth(s.handleNodeCaptureForm))
	mux.HandleFunc("POST /ui/nodes/{id}/trust", s.auth(s.handleTrustHostKey))
	mux.HandleFunc("GET /ui/nodes/{id}/inbounds", s.auth(s.handleNodeInboundsForm))
	mux.HandleFunc("POST /ui/nodes/{id}/inbounds", s.auth(s.handleSaveNodeInbounds))
	mux.HandleFunc("POST /ui/nodes/{id}/apply", s.auth(s.handleApplyNode))

	// Takeover (detect existing VPN → convert → cutover → rollback on failure)
	mux.HandleFunc("GET /ui/nodes/{id}/detect-vpn", s.auth(s.handleDetectVPN))
	mux.HandleFunc("POST /ui/nodes/{id}/takeover", s.auth(s.handleTakeover))

	// Chains (existing)
	mux.HandleFunc("GET /ui/chains", s.auth(s.handleChains))
	mux.HandleFunc("POST /ui/chains", s.auth(s.handleCreateChain))
	mux.HandleFunc("DELETE /ui/chains/{name}", s.auth(s.handleDeleteChain))
	mux.HandleFunc("POST /ui/chains/{name}/apply", s.auth(s.handleApplyChain))
	mux.HandleFunc("GET /ui/chains/new", s.auth(s.handleNewChainForm))
	mux.HandleFunc("GET /ui/chains/{name}/edit", s.auth(s.handleEditChainForm))
	mux.HandleFunc("POST /ui/chains/{name}/edit", s.auth(s.handleUpdateChain))

	// Spider Web (visual chain editor)
	mux.HandleFunc("GET /ui/spider", s.auth(s.handleSpiderWeb))
	mux.HandleFunc("POST /ui/spider/links", s.auth(s.handleCreateSpiderLink))
	mux.HandleFunc("DELETE /ui/spider/links/{id}", s.auth(s.handleDeleteSpiderLink))
	mux.HandleFunc("POST /ui/spider/nodes/{id}/position", s.auth(s.handleSaveNodePosition))
	mux.HandleFunc("POST /ui/spider/apply/{name}", s.auth(s.handleApplyChain))

	// Users
	mux.HandleFunc("GET /ui/users", s.auth(s.handleUsers))
	mux.HandleFunc("POST /ui/users", s.auth(s.handleCreateUser))
	mux.HandleFunc("GET /ui/users/new", s.auth(s.handleNewUserForm))
	mux.HandleFunc("GET /ui/users/{id}/edit", s.auth(s.handleEditUserForm))
	mux.HandleFunc("POST /ui/users/{id}/edit", s.auth(s.handleUpdateUser))
	mux.HandleFunc("DELETE /ui/users/{id}", s.auth(s.handleDeleteUser))
	mux.HandleFunc("GET /ui/users/{id}/config", s.auth(s.handleUserConfig))
	mux.HandleFunc("GET /ui/users/{id}/qr", s.auth(s.handleUserQR))
		mux.HandleFunc("GET /ui/qr-image", s.auth(s.handleQRImage))

	// Settings
	mux.HandleFunc("GET /ui/settings", s.auth(s.handleSettings))
	mux.HandleFunc("POST /ui/settings", s.auth(s.handleSaveSettings))
	// SSH Keys management
	mux.HandleFunc("POST /ui/settings/ssh-keys", s.auth(s.handleAddSSHKey))
	mux.HandleFunc("DELETE /ui/settings/ssh-keys/{id}", s.auth(s.handleDeleteSSHKey))

	// Status
	mux.HandleFunc("GET /ui/status", s.auth(s.handleStatus))

	// Audit log
	mux.HandleFunc("GET /ui/audit", s.auth(s.handleAudit))

	// Deploy status (pending-changes)
	mux.HandleFunc("GET /ui/deploy-status", s.auth(s.handleDeployStatus))

	// Profiles + ClientAssignments
	mux.HandleFunc("GET /ui/profiles", s.auth(s.handleProfiles))
	mux.HandleFunc("POST /ui/profiles", s.auth(s.handleCreateProfile))
	mux.HandleFunc("GET /ui/profiles/new", s.auth(s.handleNewProfileForm))
	mux.HandleFunc("GET /ui/profiles/{id}/edit", s.auth(s.handleEditProfileForm))
	mux.HandleFunc("POST /ui/profiles/{id}/edit", s.auth(s.handleUpdateProfile))
	mux.HandleFunc("DELETE /ui/profiles/{id}", s.auth(s.handleDeleteProfile))
	mux.HandleFunc("POST /ui/profiles/{id}/assignments", s.auth(s.handleCreateAssignment))
	mux.HandleFunc("DELETE /ui/profiles/{id}/assignments/{aid}", s.auth(s.handleDeleteAssignment))

	// Unified clients page
	mux.HandleFunc("GET /ui/clients", s.auth(s.handleClients))
}

func (s *Server) store() *chain.Store { return chain.NewStore(s.storePath) }

func isHTMXRequest(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		http.Error(w, i18n.T(r.Context(), "render error"), http.StatusInternalServerError)
	}
}

func (s *Server) renderContent(w http.ResponseWriter, r *http.Request, title string, content templ.Component) {
	if isHTMXRequest(r) {
		s.render(w, r, content)
		return
	}
	s.render(w, r, templates.Base(title, content))
}

func (s *Server) renderJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, jsonMarshal(data))
}

// detectSystemKeys scans ~/.ssh/ for common key files.
func detectSystemKeys() []model.SSHKeyEntry {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := home + "/.ssh"
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}
	var keys []model.SSHKeyEntry
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip public keys, config, known_hosts, PuTTY keys, etc.
		if strings.HasSuffix(name, ".pub") || strings.Contains(name, "known_hosts") ||
			name == "config" || name == "authorized_keys" || strings.HasSuffix(name, ".swp") ||
			strings.HasSuffix(name, ".ppk") || strings.HasSuffix(name, ".old") {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		keys = append(keys, model.SSHKeyEntry{
			ID:      "system-" + name,
			Name:    name,
			KeyPath: sshDir + "/" + name,
			Source:  "system",
		})
	}
	return keys
}

// looksLikePrivateKey checks that the data looks like a valid PEM-encoded private key.
// mergeSSHKeys combines stored and system keys into one list.
func mergeSSHKeys(stored, system []model.SSHKeyEntry) []model.SSHKeyEntry {
	all := make([]model.SSHKeyEntry, 0, len(stored)+len(system))
	all = append(all, stored...)
	all = append(all, system...)
	return all
}

// ─── Existing handlers (kept for backward compatibility) ───────────────────────

func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	chains, _ := st.ListChains()
	hosts, _ := st.ListHosts()
	s.renderContent(w, r, i18n.T(r.Context(), "Chains"), templates.Chains(chains, hosts))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	metrics, _ := st.ListMetrics()

	var content templ.Component
	if len(hosts) == 0 {
		content = &simpleHTML{html: `<div class="text-base-content/70 py-8 text-center">` + i18n.T(r.Context(), "No hosts registered yet.") + ` <a href="/ui/nodes" class="link link-primary">` + i18n.T(r.Context(), "Add nodes first") + `</a>.</div>`}
	} else {
		content = templates.StatusPage(hosts, metrics)
	}
	s.renderContent(w, r, i18n.T(r.Context(), "Status"), content)
}

func (s *Server) handleHostStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, i18n.T(r.Context(), "missing id"), http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<span class="text-error text-xs">` + i18n.T(r.Context(), "Host not found") + `</span>`})
		return
	}
	f := s.factory
	b := f.Create()
	ctx := context.Background()

	hostCopy := *host

	status, err := b.GetStatus(ctx, hostCopy)

	if err != nil {
		// Record offline metric
		st.SaveMetrics(&model.NodeMetrics{HostID: id, Online: false})
		s.render(w, r, &simpleHTML{html: `<span class="badge badge-error badge-sm">` + i18n.T(r.Context(), "Error") + `</span>`})
		return
	}
	st.SaveMetrics(&model.NodeMetrics{
		HostID:  id,
		Online:  status.Running,
		Version: status.Version,
	})
	s.render(w, r, templates.HostStatus(status))
}

// ─── Chain handlers ───────────────────────────────────────────────────────────

func (s *Server) handleNewChainForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	profiles := chain.ListPresets()
	s.render(w, r, templates.NewChainForm(hosts, profiles))
}

func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
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

	nodeIDs := r.Form["nodes"]
	if len(nodeIDs) == 0 {
		nodeIDs = r.PostForm["nodes"]
	}
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
		http.Error(w, i18n.T(r.Context(), "name and at least one node are required"), http.StatusBadRequest)
		return
	}

	st := s.store()
	nodes := make([]model.ChainNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		h, err := st.GetHost(id)
		if err != nil {
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), id), http.StatusBadRequest)
			return
		}
		nodes = append(nodes, model.ChainNode{ID: h.ID, Addr: h.Addr, User: h.User, KeyPath: h.KeyPath})
	}

	c := &model.Chain{
		Name:               name,
		Nodes:              nodes,
		Strategy:           model.Strategy(strategy),
		Transport:          transport,
		UserProtocol:       userProto,
		ObfuscationProfile: profile,
	}

	// Generate stable AWG/TUIC creds at creation time
	if userProto == model.UserProtocol("awg") {
		priv, pub, err := chain.GenerateWireGuardKeypair()
		if err == nil {
			c.AWGEntryServerPriv = priv
			c.AWGEntryServerPub = pub
		}
	}
	if userProto == model.UserProtocol("tuic") {
		uuid, _ := chain.GenerateStableTUICUserCreds()
		c.TUICEntryUserUUID = uuid
		c.TUICEntryUserPassword = uuid
	}

	if err := st.SaveChain(c); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	s.render(w, r, templates.ChainRow(c))
}

func (s *Server) handleEditChainForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "chain not found"), http.StatusNotFound)
		return
	}
	hosts, _ := st.ListHosts()
	profiles := chain.ListPresets()
	s.render(w, r, templates.EditChainForm(c, hosts, profiles))
}

func (s *Server) handleUpdateChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "chain not found"), http.StatusNotFound)
		return
	}

	c.Strategy = model.Strategy(strings.TrimSpace(r.FormValue("strategy")))
	if c.Strategy == "" {
		c.Strategy = "urltest"
	}
	transport := model.TransportType(strings.TrimSpace(r.FormValue("transport")))
	if transport != "" {
		c.Transport = transport
	}
	userProto := model.UserProtocol(strings.TrimSpace(r.FormValue("user_protocol")))
	if userProto != "" {
		c.UserProtocol = userProto
	}
	c.ObfuscationProfile = strings.TrimSpace(r.FormValue("profile"))

	// Update nodes if new ones selected
	nodeIDs := r.Form["nodes"]
	if len(nodeIDs) == 0 {
		nodeIDs = r.PostForm["nodes"]
	}
	if len(nodeIDs) > 0 {
		seen := map[string]bool{}
		uniqueNodes := []string{}
		for _, id := range nodeIDs {
			id = strings.TrimSpace(id)
			if id != "" && !seen[id] {
				seen[id] = true
				uniqueNodes = append(uniqueNodes, id)
			}
		}
		nodes := make([]model.ChainNode, 0, len(uniqueNodes))
		for _, id := range uniqueNodes {
			h, err := st.GetHost(id)
			if err != nil {
				continue
			}
			nodes = append(nodes, model.ChainNode{ID: h.ID, Addr: h.Addr, User: h.User, KeyPath: h.KeyPath})
		}
		if len(nodes) > 0 {
			c.Nodes = nodes
		}
	}

	if err := st.SaveChain(c); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	// Return updated row
	s.render(w, r, templates.ChainRow(c))
}

func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, i18n.T(r.Context(), "missing name"), http.StatusBadRequest)
		return
	}
	if err := s.store().DeleteChain(name); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "failed: %v"), err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *Server) handleApplyChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, i18n.T(r.Context(), "missing name"), http.StatusBadRequest)
		return
	}
	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		s.render(w, r, templates.ApplyResult(name, false, nil, i18n.T(r.Context(), "chain not found")))
		return
	}
	resolved, err := st.ResolveNodes(c)
	if err != nil {
		s.render(w, r, templates.ApplyResult(name, false, nil, err.Error()))
		return
	}

	c.Nodes = resolved

	applier := chain.NewApplier(s.factory)
	ctx := context.Background()
	report, err := applier.ApplyChain(ctx, st, c, "")
	if err != nil {
		msg := err.Error()
		if report != nil && len(report.Nodes) > 0 {
			for _, n := range report.Nodes {
				if !n.Success && n.Error != "" {
					msg += " | " + n.ID + ": " + n.Error
				}
			}
		}
		s.render(w, r, templates.ApplyResult(name, false, report, msg))
		return
	}
	s.render(w, r, templates.ApplyResult(name, true, report, ""))
}

func (s *Server) handleApplyNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, i18n.T(r.Context(), "missing id"), http.StatusBadRequest)
		return
	}
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		s.render(w, r, templates.ApplyResult(id, false, nil, i18n.T(r.Context(), "node not found")))
		return
	}

	applier := chain.NewApplier(s.factory)
	ctx := context.Background()

	report, mergeReport, err := applier.ApplyMergedNode(ctx, st, info)
	st.SaveNodeInfo(info)

	if err != nil {
		msg := err.Error()
		if report != nil && len(report.Nodes) > 0 {
			for _, n := range report.Nodes {
				if !n.Success && n.Error != "" {
					msg += " | " + n.ID + ": " + n.Error
				}
			}
		}
		s.render(w, r, templates.ApplyResult(id, false, report, msg))
		return
	}

	resultMsg := ""
	if mergeReport != nil {
			parts := []string{fmt.Sprintf(i18n.T(r.Context(), "%d standalone inbounds + chains: %v"),
				mergeReport.StandaloneCount, mergeReport.ChainsIncluded)}
		if len(mergeReport.AddedInbounds) > 0 {
			parts = append(parts, fmt.Sprintf("+%s", strings.Join(mergeReport.AddedInbounds, ", +")))
		}
		if len(mergeReport.RemovedInbounds) > 0 {
			parts = append(parts, fmt.Sprintf("-%s", strings.Join(mergeReport.RemovedInbounds, ", -")))
		}
		resultMsg = strings.Join(parts, " | ")
	}
	s.render(w, r, templates.ApplyResult(id, true, report, resultMsg))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type simpleHTML struct{ html string }

func (s *simpleHTML) Render(ctx context.Context, w io.Writer) error {
	_, err := io.WriteString(w, s.html)
	return err
}

var _ templ.Component = (*simpleHTML)(nil)

func jsonMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data)
}

// ─── Audit log ───────────────────────────────────────────────────────────────

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	limit := 200
	logs, err := st.ListAuditLogs(limit)
	if err != nil {
		s.renderContent(w, r, i18n.T(r.Context(), "Audit"), &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "failed to load audit log: %s")+`</div>`, escHTML(err.Error()))})
		return
	}
	if logs == nil {
		logs = []*model.AuditLog{}
	}
	// Minimal table; replaced by a templ component in the UI block.
	var b strings.Builder
	b.WriteString(`<div class="card bg-base-100 shadow"><div class="card-body"><h2 class="card-title">` + i18n.T(r.Context(), "Audit Log") + `</h2><div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>` + i18n.T(r.Context(), "Time") + `</th><th>` + i18n.T(r.Context(), "Action") + `</th><th>` + i18n.T(r.Context(), "Target") + `</th><th>` + i18n.T(r.Context(), "ID") + `</th><th>` + i18n.T(r.Context(), "Actor") + `</th><th>` + i18n.T(r.Context(), "Payload") + `</th></tr></thead><tbody>`)
	for _, a := range logs {
		b.WriteString(fmt.Sprintf("<tr><td class="+"whitespace-nowrap"+">%s</td><td><span class=\"badge badge-sm\">%s</span></td><td>%s</td><td class=\"font-mono text-xs\">%s</td><td>%s</td><td class=\"font-mono text-xs text-base-content/60\">%s</td></tr>",
			a.TS.Format("2006-01-02 15:04:05"), escHTML(a.Action), escHTML(a.TargetType), escHTML(a.TargetID), escHTML(a.Actor), escHTML(truncForDisplay(a.PayloadJSON, 80))))
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Audit"), &simpleHTML{html: b.String()})
}

// ─── Deploy status (pending-changes) ─────────────────────────────────────────

// deployStatusRow is one row of the deploy-status view.
type deployStatusRow struct {
	NodeID           string    `json:"node_id"`
	Name             string    `json:"name"`
	Role             string    `json:"role"`
	HasPending       bool      `json:"has_pending_changes"`
	LastDeployedAt   time.Time `json:"last_deployed_at,omitempty"`
	CurrentHash      string    `json:"current_hash,omitempty"`
	LastDeployedHash string    `json:"last_deployed_hash,omitempty"`
}

func (s *Server) handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	rows := s.computeDeployStatusRows(r)
	var b strings.Builder
	b.WriteString(`<div class="card bg-base-100 shadow"><div class="card-body"><h2 class="card-title">` + i18n.T(r.Context(), "Deploy Status") + `</h2><div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>` + i18n.T(r.Context(), "Node") + `</th><th>` + i18n.T(r.Context(), "Role") + `</th><th>` + i18n.T(r.Context(), "Status") + `</th><th>` + i18n.T(r.Context(), "Last deployed") + `</th><th>` + i18n.T(r.Context(), "Hash") + `</th></tr></thead><tbody>`)
	for _, row := range rows {
		status := `<span class="badge badge-success badge-sm">` + i18n.T(r.Context(), "applied") + `</span>`
		if row.HasPending {
			status = `<span class="badge badge-warning badge-sm">` + i18n.T(r.Context(), "pending") + `</span>`
		}
		last := i18n.T(r.Context(), "never")
		if !row.LastDeployedAt.IsZero() {
			last = row.LastDeployedAt.Format("2006-01-02 15:04:05")
		}
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class=\"font-mono text-xs text-base-content/50\">%s</td></tr>",
			escHTML(row.Name), escHTML(row.Role), status, last, escHTML(truncForDisplay(row.LastDeployedHash, 12))))
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Deploy Status"), &simpleHTML{html: b.String()})
}

// computeDeployStatusRows builds the per-node deploy-status list. has_pending =
// never deployed (LastDeployedHash=="") OR render error OR current hash != last
// hash. Current hash is computed from the merged config render (best-effort; a
// render error counts as pending).
func (s *Server) computeDeployStatusRows(r *http.Request) []deployStatusRow {
	st := s.store()
	infos, _ := st.ListNodeInfos()
	rows := make([]deployStatusRow, 0, len(infos))
	for _, info := range infos {
		role := inferNodeRole(info)
		row := deployStatusRow{
			NodeID:           info.ID,
			Name:             info.ID,
			Role:             role,
			LastDeployedAt:   info.LastDeployedAt,
			LastDeployedHash: info.LastDeployedHash,
		}
		// Current render hash (best-effort).
		if current, err := renderCurrentNodeConfig(st, info); err == nil {
			row.CurrentHash = chain.ConfigHash([]byte(current))
		}
		row.HasPending = info.LastDeployedHash == "" || row.CurrentHash == "" || row.CurrentHash != info.LastDeployedHash
		rows = append(rows, row)
	}
	return rows
}

// inferNodeRole guesses a node's role from its inbounds/chain membership, for
// display purposes only (does not affect config generation).
func inferNodeRole(info *model.NodeInfo) string {
	for _, ib := range info.Inbounds {
		if ib.Protocol == "mtproxy" {
			return "mtproxy_server"
		}
	}
	// AWG balancer if it has AWG inbounds; else proxy_node.
	for _, ib := range info.Inbounds {
		if ib.Protocol == "awg" {
			return "awg_balancer"
		}
	}
	return "proxy_node"
}

// renderCurrentNodeConfig renders the current merged config for a node without
// pushing it (for hash comparison). Returns the JSON string.
func renderCurrentNodeConfig(st *chain.Store, info *model.NodeInfo) (string, error) {
	nodeChains, _ := st.GetChainsForNode(info.ID)
	cfg, _, err := chain.RenderMergedNodeConfig(info, nodeChains)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// truncForDisplay shortens s to n chars with an ellipsis for table cells.
func truncForDisplay(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ─── Takeover (detect existing VPN → convert → cutover → rollback) ───────────

func (s *Server) handleDetectVPN(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "node not found: %s")+`</div>`, escHTML(err.Error()))})
		return
	}
	det, err := takeover.DetectVPN(r.Context(), info.Host, info.UseSudo)
	if err != nil {
		chain.WriteAudit(st, "takeover", "node", id, chain.AuditPayload{"phase": "detect", "error": err.Error()}, "operator")
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Detect failed: %s")+`</div>`, escHTML(err.Error()))})
		return
	}

	// Render a warning + confirm modal.
	var b strings.Builder
	if det.Type == takeover.DetectedNone {
		b.WriteString(`<div class="alert alert-info">` + i18n.T(r.Context(), "No existing VPN detected on this node. Use Install to deploy sing-box from scratch.") + `</div>`)
		if det.Note != "" {
			b.WriteString(fmt.Sprintf(`<div class="text-sm text-base-content/60">%s</div>`, det.Note))
		}
		s.render(w, r, &simpleHTML{html: b.String()})
		return
	}

	b.WriteString(`<div class="alert alert-warning"><svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"/></svg><div>`)
	b.WriteString(fmt.Sprintf(`<div><b>`+i18n.T(r.Context(), "Existing VPN detected: %s")+`</b><br>`+i18n.T(r.Context(), "Service:")+` <code>%s</code> (`+i18n.T(r.Context(), "active:")+` %v, `+i18n.T(r.Context(), "enabled:")+` %v)<br>`+i18n.T(r.Context(), "Config:")+` <code>%s</code></div>`,
		escHTML(string(det.Type)), escHTML(det.ServiceName), det.IsActive, det.IsEnabled, escHTML(det.ConfigPath)))
	b.WriteString(`</div></div>`)
	if len(det.Other) > 0 {
		b.WriteString(`<div class="text-sm text-base-content/60 mt-1">` + i18n.T(r.Context(), "Also present: ") + escHTML(strings.Join(det.Other, ", ")) + `</div>`)
	}
	b.WriteString(`<div class="py-2 text-sm">` + i18n.T(r.Context(), "Takeover will: install sing-box, convert the existing config to sing-box with the same settings, <b>disable (not delete) the old VPN</b>, and start sing-box. Old config is backed up for rollback. If sing-box fails to come up, the old VPN is restored automatically.") + `</div>`)
	b.WriteString(fmt.Sprintf(`<div class="flex gap-2"><button class="btn btn-primary btn-sm" hx-post="/ui/nodes/%s/takeover" hx-target="#main-content" hx-swap="outerHTML" hx-confirm="`+i18n.T(r.Context(), "Take over this server? The old VPN will be disabled.")+`">`+i18n.T(r.Context(), "Take over")+`</button> <button class="btn btn-ghost btn-sm" hx-get="/ui/nodes" hx-target="#main-content" hx-push-url="true">`+i18n.T(r.Context(), "Cancel")+`</button></div>`, id))
	s.renderContent(w, r, i18n.T(r.Context(), "Takeover"), &simpleHTML{html: b.String()})
}

func (s *Server) handleTakeover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "node not found: %s")+`</div>`, escHTML(err.Error()))})
		return
	}
	// Re-detect (the detection from the modal isn't POSTed; re-probe to be safe).
	det, err := takeover.DetectVPN(r.Context(), info.Host, info.UseSudo)
	if err != nil {
		chain.WriteAudit(st, "takeover", "node", id, chain.AuditPayload{"phase": "detect", "error": err.Error()}, "operator")
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Detect failed: %s")+`</div>`, escHTML(err.Error()))})
		return
	}
	res, err := takeover.Takeover(r.Context(), st, s.factory, info.Host, info.UseSudo, det)

	// Render the result.
	var b strings.Builder
	if err != nil && res != nil && res.Status != "taken" {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-error"><b>`+i18n.T(r.Context(), "Takeover %s")+`</b><br>%s</div>`, escHTML(res.Status), escHTML(res.Message)))
	} else if err != nil {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Takeover failed: %s")+`</div>`, escHTML(err.Error())))
	} else {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-success"><b>`+i18n.T(r.Context(), "Takeover successful")+`</b><br>%s</div>`, escHTML(res.Message)))
	}
	if res != nil {
		b.WriteString(`<div class="card bg-base-100 shadow mt-2"><div class="card-body text-sm">`)
		b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "From:")+` <b>%s</b> → sing-box</p>`, escHTML(res.FromType)))
		if res.OldService != "" {
			b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "Old service:")+` <code>%s</code> (`+i18n.T(r.Context(), "disabled, config backed up at")+` <code>%s</code>)</p>`, escHTML(res.OldService), escHTML(res.OldConfigBackup)))
		}
		b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "Converted inbounds: %d")+`</p>`, res.ConvertedInbounds))
		if res.RollbackOccurred {
			b.WriteString(`<p><b>`+i18n.T(r.Context(), "Rollback occurred")+`</b> — `+i18n.T(r.Context(), "old VPN was restored.")+`</p>`)
		}
		b.WriteString(`</div></div>`)
	}
	s.renderContent(w, r, i18n.T(r.Context(), "Takeover result"), &simpleHTML{html: b.String()})
}

// ─── Profiles + ClientAssignments ───────────────────────────────────────────

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	profiles, _ := st.ListProfiles()
	if profiles == nil {
		profiles = []*model.Profile{}
	}
	var b strings.Builder
	b.WriteString(`<div class="space-y-4"><h2 class="text-2xl font-semibold">` + i18n.T(r.Context(), "Profiles") + `</h2>`)
	b.WriteString(`<button class="btn btn-primary btn-sm" hx-get="/ui/profiles/new" hx-target="#modal-container">` + i18n.T(r.Context(), "+ New Profile") + `</button>`)
	b.WriteString(`<div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>` + i18n.T(r.Context(), "Name") + `</th><th>` + i18n.T(r.Context(), "Client type") + `</th><th>` + i18n.T(r.Context(), "Server role") + `</th><th>` + i18n.T(r.Context(), "Auto-apply") + `</th><th>` + i18n.T(r.Context(), "Servers") + `</th><th></th></tr></thead><tbody>`)
	for _, p := range profiles {
		auto := i18n.T(r.Context(), "no")
		if p.AutoApply {
			auto = i18n.T(r.Context(), "yes")
		}
		b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td><button class="btn btn-ghost btn-xs" hx-get="/ui/profiles/%s/edit" hx-target="#modal-container">`+i18n.T(r.Context(), "Edit")+`</button> <button class="btn btn-ghost btn-xs text-error" hx-delete="/ui/profiles/%s" hx-confirm="`+i18n.T(r.Context(), "Delete profile %s?")+`" hx-target="closest tr" hx-swap="outerHTML">`+i18n.T(r.Context(), "Delete")+`</button></td></tr>`,
			escHTML(p.Name), escHTML(p.ClientType), escHTML(p.ServerRole), auto, len(p.ServerIDs), escHTML(p.ID), escHTML(p.ID), escHTML(p.Name)))
	}
	b.WriteString(`</tbody></table></div><div id="modal-container"></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Profiles"), &simpleHTML{html: b.String()})
}

func (s *Server) handleNewProfileForm(w http.ResponseWriter, r *http.Request) {
	html := `<dialog open class="modal modal-open"><div class="modal-box"><h3 class="font-semibold mb-2">` + i18n.T(r.Context(), "New Profile") + `</h3><form hx-post="/ui/profiles" hx-target="#main-content" hx-swap="outerHTML" class="space-y-2"><input name="name" class="input input-bordered w-full" placeholder="` + i18n.T(r.Context(), "Profile name") + `" required><input name="description" class="input input-bordered w-full" placeholder="` + i18n.T(r.Context(), "Description") + `"><select name="client_type" class="select select-bordered w-full"><option value="user">user</option><option value="mtproxy">mtproxy</option><option value="awg-peer">awg-peer</option><option value="exit-node">exit-node</option></select><select name="server_role" class="select select-bordered w-full"><option value="any">any</option><option value="proxy_node">proxy_node</option><option value="awg_balancer">awg_balancer</option><option value="mtproxy_server">mtproxy_server</option></select><label class="label cursor-pointer"><span class="label-text">` + i18n.T(r.Context(), "Auto-apply") + `</span><input type="checkbox" name="auto_apply" class="checkbox" checked></label><div class="modal-action"><button type="submit" class="btn btn-primary btn-sm">` + i18n.T(r.Context(), "Create") + `</button></div></form></div></dialog>`
	s.render(w, r, &simpleHTML{html: html})
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	p := &model.Profile{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		ClientType:  r.FormValue("client_type"),
		ServerRole:  r.FormValue("server_role"),
		AutoApply:   r.FormValue("auto_apply") == "on",
	}
	if p.ServerRole == "" {
		p.ServerRole = "any"
	}
	if p.Name == "" {
		http.Error(w, i18n.T(r.Context(), "name required"), http.StatusBadRequest)
		return
	}
	if err := st.SaveProfile(p); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(st, "create", "profile", p.ID, chain.AuditPayload{"name": p.Name, "client_type": p.ClientType}, "operator")
	http.Redirect(w, r, "/ui/profiles", http.StatusSeeOther)
}

func (s *Server) handleEditProfileForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.store().GetProfile(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	checked := ""
	if p.AutoApply {
		checked = "checked"
	}
	html := fmt.Sprintf(`<dialog open class="modal modal-open"><div class="modal-box"><h3 class="font-semibold mb-2">`+i18n.T(r.Context(), "Edit Profile")+`</h3><form hx-post="/ui/profiles/%s/edit" hx-target="#main-content" hx-swap="outerHTML" class="space-y-2"><input name="name" class="input input-bordered w-full" value="%s" required><input name="description" class="input input-bordered w-full" value="%s"><input name="client_type" class="input input-bordered w-full" value="%s"><input name="server_role" class="input input-bordered w-full" value="%s"><label class="label cursor-pointer"><span class="label-text">`+i18n.T(r.Context(), "Auto-apply")+`</span><input type="checkbox" name="auto_apply" class="checkbox" %s></label><div class="modal-action"><button type="submit" class="btn btn-primary btn-sm">`+i18n.T(r.Context(), "Save")+`</button></div></form></div></dialog>`,
		escHTML(p.ID), escHTML(p.Name), escHTML(p.Description), escHTML(p.ClientType), escHTML(p.ServerRole), checked)
	s.render(w, r, &simpleHTML{html: html})
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	p, err := st.GetProfile(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	p.Name = strings.TrimSpace(r.FormValue("name"))
	p.Description = strings.TrimSpace(r.FormValue("description"))
	p.ClientType = r.FormValue("client_type")
	p.ServerRole = r.FormValue("server_role")
	p.AutoApply = r.FormValue("auto_apply") == "on"
	if err := st.SaveProfile(p); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(st, "update", "profile", p.ID, chain.AuditPayload{"name": p.Name}, "operator")
	http.Redirect(w, r, "/ui/profiles", http.StatusSeeOther)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if p, err := st.GetProfile(id); err == nil {
		chain.WriteAudit(st, "delete", "profile", id, chain.AuditPayload{"name": p.Name}, "operator")
	}
	if err := st.DeleteProfile(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *Server) handleCreateAssignment(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	a := &model.ClientAssignment{
		ProfileID:  pid,
		ClientType: r.FormValue("client_type"),
		ClientID:   r.FormValue("client_id"),
	}
	if err := s.store().SaveAssignment(a); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "assign", "client_assignment", a.ID, chain.AuditPayload{"profile_id": pid, "client_type": a.ClientType, "client_id": a.ClientID}, "operator")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleDeleteAssignment(w http.ResponseWriter, r *http.Request) {
	aid := r.PathValue("aid")
	chain.WriteAudit(s.store(), "unassign", "client_assignment", aid, chain.AuditPayload{"id": aid}, "operator")
	if err := s.store().DeleteAssignment(aid); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// ─── Unified clients page ───────────────────────────────────────────────────

// unifiedClientRow is one row of the unified clients view.
type unifiedClientRow struct {
	ClientType string `json:"client_type"`
	ClientID   string `json:"client_id"`
	Name       string `json:"name"`
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name"`
	Enabled    bool   `json:"enabled"`
	Link       string `json:"link"`
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListUsers()
	mtp, _ := st.ListMtproxyUsers()
	infos, _ := st.ListNodeInfos()
	infoByID := map[string]*model.NodeInfo{}
	for _, info := range infos {
		infoByID[info.ID] = info
	}

	var rows []unifiedClientRow
	// Users (proxy clients on chains/standalone inbounds).
	for _, u := range users {
		rows = append(rows, unifiedClientRow{ClientType: "user", ClientID: u.ID, Name: u.Name, Enabled: u.Active})
	}
	// MTProxy users.
	for _, m := range mtp {
		nodeName := m.NodeID
		if info, ok := infoByID[m.NodeID]; ok {
			nodeName = info.ID
		}
		rows = append(rows, unifiedClientRow{ClientType: "mtproxy", ClientID: m.ID, Name: m.Name, NodeID: m.NodeID, NodeName: nodeName, Enabled: m.Enabled})
	}

	// Sort by name.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Name < rows[j-1].Name; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="space-y-4"><h2 class="text-2xl font-semibold">` + i18n.T(r.Context(), "Clients") + `</h2>`)
	b.WriteString(`<div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>` + i18n.T(r.Context(), "Type") + `</th><th>` + i18n.T(r.Context(), "Name") + `</th><th>` + i18n.T(r.Context(), "Node") + `</th><th>` + i18n.T(r.Context(), "Enabled") + `</th></tr></thead><tbody>`)
	for _, row := range rows {
		en := `<span class="badge badge-success badge-sm">` + i18n.T(r.Context(), "active") + `</span>`
		if !row.Enabled {
			en = `<span class="badge badge-ghost badge-sm">` + i18n.T(r.Context(), "disabled") + `</span>`
		}
		node := row.NodeName
		if node == "" {
			node = "—"
		}
		b.WriteString(fmt.Sprintf(`<tr><td><span class="badge badge-sm">%s</span></td><td>%s</td><td>%s</td><td>%s</td></tr>`, escHTML(row.ClientType), escHTML(row.Name), escHTML(node), en))
	}
	b.WriteString(`</tbody></table></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Clients"), &simpleHTML{html: b.String()})
}
