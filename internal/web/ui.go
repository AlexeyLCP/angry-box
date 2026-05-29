package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	webassets "github.com/alexeylcp/angry-box/web"
	"github.com/alexeylcp/angry-box/web/templates"
)

// Server provides the HTMX web UI.
type Server struct {
	storePath string
	stopCh    chan struct{}
	devMode   bool
}

// NewServer creates a web UI server.
// If devMode is true, static files are served from web/static/ instead of the embedded filesystem.
func NewServer(storePath string, devMode bool) *Server {
	if devMode {
		log.Println("[dev] Loading UI from filesystem (web/static/)")
	} else {
		log.Println("[prod] Loading embedded UI")
	}
	return &Server{storePath: storePath, stopCh: make(chan struct{}), devMode: devMode}
}

// isDev returns true if the server is in development mode.
func (s *Server) isDev() bool { return s.devMode }

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
		intervalMinutes = 240 // default 4 hours
	}
	go func() {
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
	f := factory.New()
	b := f.Create()
	ctx := context.Background()

	for _, h := range hosts {
		status, err := b.GetStatus(ctx, *h)
		if err != nil {
			st.SaveMetrics(&model.NodeMetrics{HostID: h.ID, Online: false})
			continue
		}
		st.SaveMetrics(&model.NodeMetrics{
			HostID:  h.ID,
			Online:  status.Running,
			Version: status.Version,
		})
	}
}

func (s *Server) Register(mux *http.ServeMux) {
	// Static files (CSS, JS, images) — from disk in dev, from embed in prod
	staticFS, err := s.staticFS()
	if err != nil {
		log.Printf("WARNING: static files unavailable: %v", err)
	} else {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	mux.HandleFunc("GET /ui", s.handleDashboard)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
	})

	// API endpoints for dashboard
	mux.HandleFunc("GET /ui/api/stats", s.handleStats)
	mux.HandleFunc("GET /ui/api/metrics", s.handleMetricsJSON)
	mux.HandleFunc("GET /ui/dashboard/stats", s.handleDashboardStatsHTML)

	// Hosts (kept for backward compat, redirect to nodes)
	mux.HandleFunc("GET /ui/hosts", s.handleNodes)
	mux.HandleFunc("POST /ui/hosts", s.handleCreateHost)
	mux.HandleFunc("DELETE /ui/hosts/{id}", s.handleDeleteHost)
	mux.HandleFunc("GET /ui/hosts/new", s.handleNewHostForm)
	mux.HandleFunc("GET /ui/hosts/{id}/status", s.handleHostStatus)

	// Nodes (new CRUD)
	mux.HandleFunc("GET /ui/nodes", s.handleNodes)
	mux.HandleFunc("POST /ui/nodes", s.handleCreateNode)
	mux.HandleFunc("GET /ui/nodes/new", s.handleNewNodeForm)
	mux.HandleFunc("GET /ui/nodes/{id}/edit", s.handleEditNodeForm)
	mux.HandleFunc("POST /ui/nodes/{id}/edit", s.handleUpdateNode)
	mux.HandleFunc("DELETE /ui/nodes/{id}", s.handleDeleteNode)
	mux.HandleFunc("POST /ui/nodes/{id}/capture", s.handleCaptureNode)
	mux.HandleFunc("GET /ui/nodes/{id}/capture", s.handleNodeCaptureForm)
	mux.HandleFunc("GET /ui/nodes/{id}/inbounds", s.handleNodeInboundsForm)
	mux.HandleFunc("POST /ui/nodes/{id}/inbounds", s.handleSaveNodeInbounds)

	// Chains (existing)
	mux.HandleFunc("GET /ui/chains", s.handleChains)
	mux.HandleFunc("POST /ui/chains", s.handleCreateChain)
	mux.HandleFunc("DELETE /ui/chains/{name}", s.handleDeleteChain)
	mux.HandleFunc("POST /ui/chains/{name}/apply", s.handleApplyChain)
	mux.HandleFunc("GET /ui/chains/new", s.handleNewChainForm)

	// Spider Web (visual chain editor)
	mux.HandleFunc("GET /ui/spider", s.handleSpiderWeb)
	mux.HandleFunc("POST /ui/spider/links", s.handleCreateSpiderLink)
	mux.HandleFunc("DELETE /ui/spider/links/{id}", s.handleDeleteSpiderLink)
	mux.HandleFunc("POST /ui/spider/apply/{name}", s.handleApplyChain)

	// Users
	mux.HandleFunc("GET /ui/users", s.handleUsers)
	mux.HandleFunc("POST /ui/users", s.handleCreateUser)
	mux.HandleFunc("GET /ui/users/new", s.handleNewUserForm)
	mux.HandleFunc("GET /ui/users/{id}/edit", s.handleEditUserForm)
	mux.HandleFunc("POST /ui/users/{id}/edit", s.handleUpdateUser)
	mux.HandleFunc("DELETE /ui/users/{id}", s.handleDeleteUser)
	mux.HandleFunc("GET /ui/users/{id}/config", s.handleUserConfig)
	mux.HandleFunc("GET /ui/users/{id}/qr", s.handleUserQR)

	// Settings
	mux.HandleFunc("GET /ui/settings", s.handleSettings)
	mux.HandleFunc("POST /ui/settings", s.handleSaveSettings)
	// SSH Keys management
	mux.HandleFunc("POST /ui/settings/ssh-keys", s.handleAddSSHKey)
	mux.HandleFunc("DELETE /ui/settings/ssh-keys/{id}", s.handleDeleteSSHKey)

	// Status
	mux.HandleFunc("GET /ui/status", s.handleStatus)
}

func (s *Server) store() *chain.Store { return chain.NewStore(s.storePath) }

func isHTMXRequest(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *Server) render(w http.ResponseWriter, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(context.Background(), w); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) renderContent(w http.ResponseWriter, r *http.Request, title string, content templ.Component) {
	if isHTMXRequest(r) {
		s.render(w, content)
		return
	}
	s.render(w, templates.Base(title, content))
}

func (s *Server) renderJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, jsonMarshal(data))
}

// ─── Dashboard ─────────────────────────────────────────────────────────────────

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

	s.renderContent(w, r, "Dashboard", templates.Dashboard(stats, hosts, metrics, infos, chains))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
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
	s.renderJSON(w, map[string]any{
		"total_hosts":   len(hosts),
		"online_hosts":  online,
		"total_chains":  len(chains),
		"total_users":   len(users),
	})
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
	s.render(w, templates.StatsCards(stats))
}

func (s *Server) handleMetricsJSON(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	metrics, _ := st.ListMetrics()
	s.renderJSON(w, metrics)
}

// ─── Nodes ─────────────────────────────────────────────────────────────────────

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	infos, _ := st.ListNodeInfos()
	metrics, _ := st.ListMetrics()
	s.renderContent(w, r, "Nodes", templates.Nodes(hosts, infos, metrics))
}

func (s *Server) handleNewHostForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, templates.NewHostForm())
}

func (s *Server) handleNewNodeForm(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store().GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, templates.NodeForm(nil, settings, allKeys))
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
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
	country := strings.TrimSpace(r.FormValue("country"))
	bandwidth := strings.TrimSpace(r.FormValue("bandwidth"))

	if id == "" || addr == "" {
		http.Error(w, "id and addr are required", http.StatusBadRequest)
		return
	}

	st := s.store()
	if err := st.SaveHost(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}); err != nil {
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	st.SaveNodeInfo(&model.NodeInfo{
		Host:      model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath},
		Country:   country,
		Bandwidth: bandwidth,
		Source:    "ssh_key",
	})

	s.render(w, templates.NodeRow(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath},
		&model.NodeInfo{Country: country, Bandwidth: bandwidth, Source: "ssh_key"}, nil))
}

func (s *Server) handleEditNodeForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, templates.NodeForm(host, settings, allKeys, info))
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	host.Addr = strings.TrimSpace(r.FormValue("addr"))
	host.User = strings.TrimSpace(r.FormValue("user"))
	if keyPath := strings.TrimSpace(r.FormValue("keyPath")); keyPath != "" {
		host.KeyPath = keyPath
	}
	st.SaveHost(host)

	info := &model.NodeInfo{
		Host:      *host,
		Country:   strings.TrimSpace(r.FormValue("country")),
		Bandwidth: strings.TrimSpace(r.FormValue("bandwidth")),
		Source:    strings.TrimSpace(r.FormValue("source")),
	}
	st.SaveNodeInfo(info)

	if isHTMXRequest(r) {
		s.render(w, templates.NodeRow(host, info, nil))
	} else {
		http.Redirect(w, r, "/ui/nodes", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if err := st.DeleteHost(id); err != nil {
		http.Error(w, fmt.Sprintf("delete: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCaptureNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	selectedKey := strings.TrimSpace(r.FormValue("ssh_key"))
	loginUser := strings.TrimSpace(r.FormValue("login_user"))
	loginPass := strings.TrimSpace(r.FormValue("login_pass"))
	autoInstallKey := r.FormValue("auto_install_key") == "on"

	if selectedKey != "" {
		host.KeyPath = selectedKey
	}
	if loginUser != "" {
		host.User = loginUser
	}
	if loginPass != "" {
		// Store password-based auth for capture
		host.KeyPath = "password:" + loginPass
	}

	// Try SSH connection
	f := factory.New()
	b := f.Create()
	ctx := context.Background()
	status, sshErr := b.GetStatus(ctx, *host)

	if sshErr != nil {
		s.render(w, &simpleHTML{html: fmt.Sprintf(
			`<div class="alert alert-error"><span>Capture failed: %v</span></div>`, sshErr,
		)})
		return
	}

	host.KeyPath = strings.TrimSpace(r.FormValue("keyPath"))
	if autoInstallKey && host.KeyPath != "" {
		st.SaveHost(host)
	}

	info := &model.NodeInfo{
		Host:   *host,
		Source: "captured",
	}
	st.SaveNodeInfo(info)
	st.SaveMetrics(&model.NodeMetrics{
		HostID:  id,
		Online:  status.Running,
		Version: status.Version,
	})

	s.render(w, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>Node %s captured! Running: %v, Version: %s</span>
		<button class="btn btn-sm btn-ghost" hx-get="/ui/nodes" hx-target="#main-content" hx-push-url="true">Refresh Nodes</button></div>`,
		id, status.Running, status.Version,
	)})
}

func (s *Server) handleNodeCaptureForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	settings, _ := st.GetSettings()
	s.render(w, templates.NodeCaptureForm(host, settings))
}

func (s *Server) handleNodeInboundsForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.store().GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{Host: model.Host{ID: id}}
	}
	users, _ := s.store().ListUsers()
	s.render(w, templates.NodeInboundsForm(info, users))
}

func (s *Server) handleSaveNodeInbounds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{Host: model.Host{ID: id}}
	}

	protocols := r.Form["proto"]
	ports := r.Form["port"]
	usersPerInbound := r.Form["for_users"]

	inbounds := make([]model.NodeInbound, len(protocols))
	for i := range protocols {
		port, _ := strconv.Atoi(ports[i])
		forUsers := []string{}
		if i < len(usersPerInbound) {
			forUsers = strings.Split(usersPerInbound[i], ",")
		}
		inbounds[i] = model.NodeInbound{
			Protocol: protocols[i],
			Port:     port,
			ForUsers: forUsers,
		}
	}
	info.Inbounds = inbounds
	st.SaveNodeInfo(info)
	s.render(w, &simpleHTML{html: `<div class="alert alert-success">Inbounds saved.</div>`})
}

// ─── Spider Web ────────────────────────────────────────────────────────────────

func (s *Server) handleSpiderWeb(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	infos, _ := st.ListNodeInfos()
	s.renderContent(w, r, "Spider Web", templates.SpiderWeb(hosts, chains, infos))
}

func (s *Server) handleCreateSpiderLink(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	fromNode := strings.TrimSpace(r.FormValue("from_node"))
	toNode := strings.TrimSpace(r.FormValue("to_node"))
	transport := strings.TrimSpace(r.FormValue("transport"))
	chainName := strings.TrimSpace(r.FormValue("chain_name"))

	if fromNode == "" || toNode == "" || chainName == "" {
		http.Error(w, "from_node, to_node, and chain_name are required", http.StatusBadRequest)
		return
	}
	if transport == "" {
		transport = "xhttp"
	}

	st := s.store()

	// Check if chain exists, if not create it
	existing, err := st.GetChain(chainName)
	var nodes []model.ChainNode
	if err == nil {
		// Chain exists, add new edge
		nodes = existing.Nodes
	} else {
		// New chain
		nodes = []model.ChainNode{}
	}

	// Build ordered nodes (add fromNode if not present, then toNode after it)
	fromIdx, toIdx := -1, -1
	for i, n := range nodes {
		if n.ID == fromNode {
			fromIdx = i
		}
		if n.ID == toNode {
			toIdx = i
		}
	}

	// Resolve hosts
	fromHost, err := st.GetHost(fromNode)
	if err != nil {
		http.Error(w, fmt.Sprintf("host %q not found", fromNode), http.StatusBadRequest)
		return
	}
	toHost, err := st.GetHost(toNode)
	if err != nil {
		http.Error(w, fmt.Sprintf("host %q not found", toNode), http.StatusBadRequest)
		return
	}

	if fromIdx < 0 {
		nodes = append(nodes, model.ChainNode{ID: fromHost.ID, Addr: fromHost.Addr, User: fromHost.User, KeyPath: fromHost.KeyPath})
	}
	if toIdx < 0 {
		nodes = append(nodes, model.ChainNode{ID: toHost.ID, Addr: toHost.Addr, User: toHost.User, KeyPath: toHost.KeyPath})
	}

	chain := &model.Chain{
		Name:      chainName,
		Nodes:     nodes,
		Strategy:  model.StrategyURLTest,
		Transport: model.TransportType(transport),
	}
	st.SaveChain(chain)

	// Re-render with full data from store
	allHosts, _ := st.ListHosts()
	allChains, _ := st.ListChains()
	allInfos, _ := st.ListNodeInfos()
	s.render(w, templates.SpiderWeb(allHosts, allChains, allInfos))
}

func (s *Server) handleDeleteSpiderLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid link id format", http.StatusBadRequest)
		return
	}
	chainName, nodeID := parts[0], parts[1]
	st := s.store()
	c, err := st.GetChain(chainName)
	if err != nil {
		http.Error(w, "chain not found", http.StatusNotFound)
		return
	}
	filtered := c.Nodes[:0]
	for _, n := range c.Nodes {
		if n.ID != nodeID {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		st.DeleteChain(chainName)
	} else {
		c.Nodes = filtered
		st.SaveChain(c)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Users ─────────────────────────────────────────────────────────────────────

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListUsers()
	chains, _ := st.ListChains()

	// Auto-deactivate expired users on every view
	now := time.Now()
	for _, u := range users {
		if u.Active && !u.ExpiresAt.IsZero() && now.After(u.ExpiresAt) {
			u.Active = false
			st.SaveUser(u)
		}
	}

	s.renderContent(w, r, "Users", templates.Users(users, chains))
}

func (s *Server) handleNewUserForm(w http.ResponseWriter, r *http.Request) {
	chains, _ := s.store().ListChains()
	s.render(w, templates.UserForm(nil, chains))
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	telegram := strings.TrimSpace(r.FormValue("telegram"))
	email := strings.TrimSpace(r.FormValue("email"))
	expiryStr := strings.TrimSpace(r.FormValue("expires_at"))
	protocols := r.Form["protocols"]
	chainNames := r.Form["chains"]
	importedSecret := strings.TrimSpace(r.FormValue("imported_secret"))
	secretType := strings.TrimSpace(r.FormValue("secret_type"))

	if id == "" || name == "" {
		http.Error(w, "id and name are required", http.StatusBadRequest)
		return
	}

	var expiresAt time.Time
	if expiryStr != "" {
		expiresAt, _ = time.Parse("2006-01-02", expiryStr)
	}

	u := &model.User{
		ID:             id,
		Name:           name,
		Telegram:       telegram,
		Email:          email,
		ExpiresAt:      expiresAt,
		Active:         true,
		Protocols:      protocols,
		ChainNames:     chainNames,
		ImportedSecret: importedSecret,
		SecretType:     secretType,
		CreatedAt:      time.Now(),
	}

	if len(u.Protocols) == 0 {
		u.Protocols = []string{"awg"}
	}

	st := s.store()
	if err := st.SaveUser(u); err != nil {
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.render(w, templates.UserRow(u))
}

func (s *Server) handleEditUserForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, err := s.store().GetUser(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	chains, _ := s.store().ListChains()
	s.render(w, templates.UserForm(u, chains))
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	u.Name = strings.TrimSpace(r.FormValue("name"))
	u.Telegram = strings.TrimSpace(r.FormValue("telegram"))
	u.Email = strings.TrimSpace(r.FormValue("email"))
	if expiryStr := strings.TrimSpace(r.FormValue("expires_at")); expiryStr != "" {
		u.ExpiresAt, _ = time.Parse("2006-01-02", expiryStr)
	} else {
		u.ExpiresAt = time.Time{}
	}
	u.Protocols = r.Form["protocols"]
	u.ChainNames = r.Form["chains"]
	u.ImportedSecret = strings.TrimSpace(r.FormValue("imported_secret"))
	u.SecretType = strings.TrimSpace(r.FormValue("secret_type"))
	u.Active = r.FormValue("active") == "on"

	if len(u.Protocols) == 0 {
		u.Protocols = []string{"awg"}
	}

	st.SaveUser(u)
	if isHTMXRequest(r) {
		s.render(w, templates.UserRow(u))
	} else {
		http.Redirect(w, r, "/ui/users", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store().DeleteUser(id); err != nil {
		http.Error(w, fmt.Sprintf("delete: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUserConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Generate configs for user's assigned chains
	var configs []templates.UserChainConfig
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		// Build a config link for this chain
		link := buildConnectionLink(c, u)
		configs = append(configs, templates.UserChainConfig{
			ChainName:   chainName,
			Protocol:    string(c.UserProtocol),
			ConfigLink:  link,
			Description: fmt.Sprintf("%s chain — %d hops, strategy: %s", chainName, len(c.Nodes), c.Strategy),
		})
	}

	// If no chains assigned, generate a generic config for each protocol
	if len(configs) == 0 {
		for _, proto := range u.Protocols {
			configs = append(configs, templates.UserChainConfig{
				ChainName:   "standalone",
				Protocol:    proto,
				ConfigLink:  fmt.Sprintf("# generate via CLI: angry-box config -type user -protocol %s", proto),
				Description: "No chains assigned. Use CLI to generate config.",
			})
		}
	}

	s.render(w, templates.UserConfigView(u, configs))
}

func buildConnectionLink(c *model.Chain, u *model.User) string {
	if len(c.Nodes) == 0 {
		return "# no nodes in chain"
	}
	entry := c.Nodes[0]
	proto := string(c.UserProtocol)
	if proto == "" {
		proto = "awg"
	}

	switch proto {
	case "awg":
		return fmt.Sprintf("awg://%s:%d?pub=%s&psk=&mtu=1420",
			entry.Addr, 8443, c.AWGEntryServerPub)
	case "tuic":
		return fmt.Sprintf("tuic://%s:%s@%s:%d?congestion_control=bbr&alpn=h3",
			c.TUICEntryUserUUID, c.TUICEntryUserPassword, entry.Addr, 8443)
	default:
		return fmt.Sprintf("# %s config for chain %s — see CLI", proto, c.Name)
	}
}

func (s *Server) handleUserQR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	var links []string
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		link := buildConnectionLink(c, u)
		links = append(links, link)
	}

	s.render(w, templates.UserQRView(u, links))
}

// ─── Settings ──────────────────────────────────────────────────────────────────

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	settings, _ := st.GetSettings()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	sysKeys := detectSystemKeys()
	s.renderContent(w, r, "Settings", templates.Settings(settings, hosts, chains, sysKeys))
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	st := s.store()
	settings, _ := st.GetSettings()

	newPassword := strings.TrimSpace(r.FormValue("admin_password"))
	if newPassword != "" {
		h := sha256.Sum256([]byte(newPassword))
		settings.AdminPasswordHash = hex.EncodeToString(h[:])
	}
	settings.PanelCountry = strings.TrimSpace(r.FormValue("panel_country"))
	if intervalStr := strings.TrimSpace(r.FormValue("metrics_interval")); intervalStr != "" {
		settings.MetricsInterval, _ = strconv.Atoi(intervalStr)
	}
	settings.DefaultProtocol = strings.TrimSpace(r.FormValue("default_protocol"))

	// SSH keys
	keyNames := r.Form["ssh_key_name"]
	keyPaths := r.Form["ssh_key_path"]
	keys := make([]model.SSHKeyEntry, 0, len(keyNames))
	for i := range keyNames {
		if keyNames[i] != "" && keyPaths[i] != "" {
			keys = append(keys, model.SSHKeyEntry{Name: keyNames[i], KeyPath: keyPaths[i]})
		}
	}
	settings.SSHKeys = keys

	st.SaveSettings(settings)
	s.render(w, &simpleHTML{html: `<div class="alert alert-success"><span>Settings saved.</span></div>`})
}

// ─── SSH Keys ──────────────────────────────────────────────────────────────────

func (s *Server) handleAddSSHKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	keyData := strings.TrimSpace(r.FormValue("key_data"))
	if name == "" || keyData == "" {
		s.render(w, &simpleHTML{html: `<div class="alert alert-error"><span>Name and key data are required.</span></div>`})
		return
	}
	// Validate key format
	if !looksLikePrivateKey(keyData) {
		s.render(w, &simpleHTML{html: `<div class="alert alert-error"><span>Invalid key format. Expected a private key (BEGIN ... PRIVATE KEY).</span></div>`})
		return
	}
	st := s.store()
	settings, _ := st.GetSettings()
	id := fmt.Sprintf("key-%d", len(settings.SSHKeys)+1)
	settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
		ID:      id,
		Name:    name,
		KeyData: keyData,
		Source:  "stored",
	})
	st.SaveSettings(settings)
	// Return updated key list
	sysKeys := detectSystemKeys()
	s.render(w, templates.SSHKeyList(settings, sysKeys))
}

func (s *Server) handleDeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	settings, _ := st.GetSettings()
	filtered := settings.SSHKeys[:0]
	found := false
	for _, k := range settings.SSHKeys {
		if k.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, k)
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	settings.SSHKeys = filtered
	st.SaveSettings(settings)
	w.WriteHeader(http.StatusNoContent)
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
		// Skip public keys, config, known_hosts, etc.
		if strings.HasSuffix(name, ".pub") || strings.HasPrefix(name, "known_hosts") ||
			name == "config" || name == "authorized_keys" || strings.HasSuffix(name, ".swp") {
			continue
		}
		// Only include common private key names
		base := name
		isPrivateKey := strings.HasPrefix(name, "id_") ||
			strings.Contains(name, "ed25519") || strings.Contains(name, "rsa") ||
			strings.Contains(name, "ecdsa") || strings.Contains(name, "dsa")
		if !isPrivateKey {
			continue
		}
		if seen[base] {
			continue
		}
		seen[base] = true
		keys = append(keys, model.SSHKeyEntry{
			ID:      "system-" + base,
			Name:    base + " (system)",
			KeyPath: sshDir + "/" + name,
			Source:  "system",
		})
	}
	return keys
}

// looksLikePrivateKey does a quick check for PEM private key header.
func looksLikePrivateKey(data string) bool {
	return strings.Contains(data, "BEGIN") && strings.Contains(data, "PRIVATE KEY")
}

// mergeSSHKeys combines stored and system keys into one list.
func mergeSSHKeys(stored, system []model.SSHKeyEntry) []model.SSHKeyEntry {
	all := make([]model.SSHKeyEntry, 0, len(stored)+len(system))
	all = append(all, stored...)
	all = append(all, system...)
	return all
}

// ─── Existing handlers (kept for backward compatibility) ───────────────────────

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
	st := s.store()
	if err := st.SaveHost(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}); err != nil {
		http.Error(w, fmt.Sprintf("save failed: %v", err), http.StatusInternalServerError)
		return
	}
	s.render(w, templates.HostRow(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}))
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := s.store().DeleteHost(id); err != nil {
		http.Error(w, fmt.Sprintf("failed: %v", err), http.StatusInternalServerError)
		return
	}
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
	metrics, _ := st.ListMetrics()

	var content templ.Component
	if len(hosts) == 0 {
		content = &simpleHTML{html: `<div class="text-base-content/70 py-8 text-center">No hosts registered yet. <a href="/ui/nodes" class="link link-primary">Add nodes first</a>.</div>`}
	} else {
		content = templates.StatusPage(hosts, metrics)
	}
	s.renderContent(w, r, "Status", content)
}

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
		// Record offline metric
		st.SaveMetrics(&model.NodeMetrics{HostID: id, Online: false})
		s.render(w, &simpleHTML{html: `<span class="badge badge-error badge-sm">Error</span>`})
		return
	}
	st.SaveMetrics(&model.NodeMetrics{
		HostID:  id,
		Online:  status.Running,
		Version: status.Version,
	})
	s.render(w, templates.HostStatus(status))
}

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
		http.Error(w, "name and at least one node are required", http.StatusBadRequest)
		return
	}

	st := s.store()
	nodes := make([]model.ChainNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		h, err := st.GetHost(id)
		if err != nil {
			http.Error(w, fmt.Sprintf("host %q not found", id), http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.render(w, templates.ChainRow(c))
}

func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	if err := s.store().DeleteChain(name); err != nil {
		http.Error(w, fmt.Sprintf("failed: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	resolved, err := st.ResolveNodes(c)
	if err != nil {
		s.render(w, templates.ApplyResult(name, false, nil, err.Error()))
		return
	}
	c.Nodes = resolved

	f := factory.New()
	applier := chain.NewApplier(f)
	ctx := context.Background()
	report, err := applier.ApplyChain(ctx, c, "")
	if err != nil {
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
	s.render(w, templates.ApplyResult(name, true, report, ""))
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

