package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/internal/takeover"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	webassets "github.com/alexeylcp/angry-box/web"
	"github.com/alexeylcp/angry-box/web/templates"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

// Server provides the HTMX web UI.
type Server struct {
	storePath        string
	stopCh           chan struct{}
	devMode          bool
	cfg              *config.Config
	ActiveListenAddr string
}

// NewServer creates a web UI server.
// If devMode is true, static files are served from web/static/ instead of the embedded filesystem.
func NewServer(storePath string, devMode bool, cfg *config.Config, activeListenAddr string) *Server {
	if devMode {
		log.Println("[dev] Loading UI from filesystem (web/static/)")
	} else {
		log.Println("[prod] Loading embedded UI")
	}
	return &Server{storePath: storePath, stopCh: make(chan struct{}), devMode: devMode, cfg: cfg, ActiveListenAddr: activeListenAddr}
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
	f := factory.New()
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

	// API endpoints for dashboard
	mux.HandleFunc("GET /ui/api/stats", s.auth(s.handleStats))
	mux.HandleFunc("GET /ui/api/metrics", s.auth(s.handleMetricsJSON))
	mux.HandleFunc("GET /ui/dashboard/stats", s.auth(s.handleDashboardStatsHTML))

	// Hosts (kept for backward compat, redirect to nodes)
	mux.HandleFunc("GET /ui/hosts", s.auth(s.handleNodes))
	mux.HandleFunc("POST /ui/hosts", s.auth(s.handleCreateHost))
	mux.HandleFunc("DELETE /ui/hosts/{id}", s.auth(s.handleDeleteHost))
	mux.HandleFunc("GET /ui/hosts/new", s.auth(s.handleNewHostForm))
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
	mux.HandleFunc("GET /ui/qr-image", s.handleQRImage)

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
	mux.HandleFunc("GET /ui/api/deploy-status", s.auth(s.handleDeployStatusJSON))

	// Protocol presets catalog + credential generators (for UI "Generate" buttons)
	mux.HandleFunc("GET /ui/api/protocol-presets", s.auth(s.handleProtocolPresetsJSON))
	mux.HandleFunc("GET /ui/api/crypto/wg-keypair", s.auth(s.handleCryptoWGKeypair))
	mux.HandleFunc("GET /ui/api/crypto/mtproxy-secret", s.auth(s.handleCryptoMTProxySecret))
	mux.HandleFunc("GET /ui/api/crypto/proxy-credentials", s.auth(s.handleCryptoProxyCreds))
	mux.HandleFunc("GET /ui/api/protocols/generate", s.auth(s.handleProtocolsGenerate))
	mux.HandleFunc("GET /ui/api/awg/preset", s.auth(s.handleAWGPresetJSON))
	mux.HandleFunc("GET /ui/api/awg/cps", s.auth(s.handleAWGCPSJSON))
	mux.HandleFunc("GET /ui/api/awg/capture", s.auth(s.handleAWGCaptureJSON))

	// Config preview (render without pushing)
	mux.HandleFunc("GET /ui/nodes/{id}/config/preview", s.auth(s.handleNodeConfigPreview))

	// AWG config SSH import (take over an existing AWG balancer)
	mux.HandleFunc("POST /ui/nodes/{id}/import-awg", s.auth(s.handleImportAWG))

	// Profiles + ClientAssignments
	mux.HandleFunc("GET /ui/profiles", s.auth(s.handleProfiles))
	mux.HandleFunc("POST /ui/profiles", s.auth(s.handleCreateProfile))
	mux.HandleFunc("GET /ui/profiles/new", s.auth(s.handleNewProfileForm))
	mux.HandleFunc("GET /ui/profiles/{id}/edit", s.auth(s.handleEditProfileForm))
	mux.HandleFunc("POST /ui/profiles/{id}/edit", s.auth(s.handleUpdateProfile))
	mux.HandleFunc("DELETE /ui/profiles/{id}", s.auth(s.handleDeleteProfile))
	mux.HandleFunc("POST /ui/profiles/{id}/assignments", s.auth(s.handleCreateAssignment))
	mux.HandleFunc("DELETE /ui/profiles/{id}/assignments/{aid}", s.auth(s.handleDeleteAssignment))
	mux.HandleFunc("GET /ui/api/profiles", s.auth(s.handleProfilesJSON))

	// Per-node route rules
	mux.HandleFunc("GET /ui/nodes/{id}/route-rules", s.auth(s.handleRouteRules))
	mux.HandleFunc("POST /ui/nodes/{id}/route-rules", s.auth(s.handleCreateRouteRule))
	mux.HandleFunc("DELETE /ui/nodes/{id}/route-rules/{rid}", s.auth(s.handleDeleteRouteRule))
	mux.HandleFunc("GET /ui/api/routing-presets", s.auth(s.handleRoutingPresetsJSON))

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
		"total_hosts":  len(hosts),
		"online_hosts": online,
		"total_chains": len(chains),
		"total_users":  len(users),
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
	s.render(w, r, templates.StatsCards(stats))
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
	chains, _ := st.ListChains()
	settings, _ := st.GetSettings()
	if len(settings.CustomPresets) > 0 {
		var customs []chain.ConnectionPreset
		if json.Unmarshal(settings.CustomPresets, &customs) == nil && len(customs) > 0 {
			_ = chain.LoadPresets(customs)
		}
	}
	activeChains := make(map[string]string)
	for _, c := range chains {
		for _, n := range c.Nodes {
			activeChains[n.ID] = c.Name
		}
	}
	s.renderContent(w, r, i18n.T(r.Context(), "Nodes"), templates.Nodes(hosts, infos, metrics, activeChains))
}

func (s *Server) handleNewHostForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, templates.NewHostForm())
}

func (s *Server) handleNewNodeForm(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store().GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeForm(nil, settings, allKeys))
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
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
		http.Error(w, i18n.T(r.Context(), "id and addr are required"), http.StatusBadRequest)
		return
	}

	st := s.store()
	if err := st.SaveHost(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	st.SaveNodeInfo(&model.NodeInfo{
		Host:      model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath},
		Country:   country,
		Bandwidth: bandwidth,
		Source:    "ssh_key",
	})

	chains, _ := st.ListChains()
	chainName := ""
	for _, c := range chains {
		for _, n := range c.Nodes {
			if n.ID == id {
				chainName = c.Name
			}
		}
	}

	s.render(w, r, templates.NodeRow(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath},
		&model.NodeInfo{Country: country, Bandwidth: bandwidth, Source: "ssh_key"}, nil, chainName))
}

func (s *Server) handleEditNodeForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeForm(host, settings, allKeys, info))
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
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
		chains, _ := st.ListChains()
		chainName := ""
		for _, c := range chains {
			for _, n := range c.Nodes {
				if n.ID == id {
					chainName = c.Name
				}
			}
		}
		s.render(w, r, templates.NodeRow(host, info, nil, chainName))
	} else {
		http.Redirect(w, r, "/ui/nodes", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if err := st.DeleteHost(id); err != nil {
		msg := fmt.Sprintf(`<tr class="bg-error/10"><td colspan="6" class="p-4 text-error font-medium">`+i18n.T(r.Context(), "Failed to delete: %v")+`. <button class="btn btn-xs btn-outline ml-4" onclick="location.reload()">Dismiss</button></td></tr>`, err)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(msg))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *Server) handleCaptureNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}

	selectedKey := strings.TrimSpace(r.FormValue("ssh_key"))
	loginUser := strings.TrimSpace(r.FormValue("login_user"))
	loginPass := strings.TrimSpace(r.FormValue("login_pass"))
	autoInstallKey := r.FormValue("auto_install_key") == "on"
	manualKeyData := strings.TrimSpace(r.FormValue("ssh_key_manual"))

	// If the user pasted a manual key, save it persistently to the database
	if selectedKey == "manual" && manualKeyData != "" {
		settings, _ := st.GetSettings()
		keyName := fmt.Sprintf("manual-%s", host.Addr)
		if strings.Contains(host.Addr, ":") {
			keyName = fmt.Sprintf("manual-%s", strings.Split(host.Addr, ":")[0])
		}
		keyID := fmt.Sprintf("key-manual-%d", time.Now().Unix())

		settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
			ID:      keyID,
			Name:    keyName,
			KeyData: manualKeyData,
		})
		st.SaveSettings(settings)
		selectedKey = keyID
	}

	authMethod := selectedKey

	if loginUser != "" {
		host.User = loginUser
	}

	if loginPass != "" {
		authMethod = "password:" + loginPass
	}

	hostCopy := *host
	hostCopy.KeyPath = authMethod

	// Try SSH connection
	f := factory.New()
	b := f.Create()
	ctx := context.Background()
	status, sshErr := b.GetStatus(ctx, hostCopy)

	if sshErr != nil {
		var hkErr *sshclient.HostKeyError
		if errors.As(sshErr, &hkErr) {
			s.render(w, r, templates.HostKeyWarning(*host, hkErr.RemoteFingerprint, hkErr.Changed))
			return
		}
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(
			`<div class="alert alert-error"><span>`+i18n.T(r.Context(), "Capture failed: %v")+`</span></div>`, sshErr,
		)})
		return
	}

	// Connection successful. Handle SSH key auto-install.
	installMsg := ""
	if autoInstallKey && loginPass != "" {
		if selectedKey == "" || strings.HasPrefix(selectedKey, "system-") {
			// Auto-generate a new keypair
			privPEM, _, err := sshclient.GenerateSSHKeypair()
			if err != nil {
				installMsg = fmt.Sprintf(" <b>"+i18n.T(r.Context(), "Note:")+"</b> "+i18n.T(r.Context(), "SSH key auto-generation failed: %v"), err)
				host.KeyPath = "password:" + loginPass
			} else {
				// Save it to settings so we can use it
				keyName := fmt.Sprintf("auto-%s", host.Addr)
				if strings.Contains(host.Addr, ":") {
					keyName = fmt.Sprintf("auto-%s", strings.Split(host.Addr, ":")[0])
				}
				keyID := fmt.Sprintf("key-auto-%d", time.Now().Unix())

				settings, _ := st.GetSettings()
				settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
					ID:      keyID,
					Name:    keyName,
					KeyData: privPEM,
				})
				st.SaveSettings(settings)

				selectedKey = keyID
				hostCopy.KeyPath = keyID // update for install
			}
		}

		if selectedKey != "" {
			if err := sshclient.InstallPublicKey(hostCopy.Addr, hostCopy.User, loginPass, selectedKey); err != nil {
				installMsg = fmt.Sprintf(" <b>"+i18n.T(r.Context(), "Note:")+"</b> "+i18n.T(r.Context(), "SSH key installation failed: %v"), err)
				host.KeyPath = "password:" + loginPass
			} else {
				// Key installed successfully! Use the key instead of password.
				host.KeyPath = selectedKey
			}
		}
	} else if loginPass != "" {
		host.KeyPath = "password:" + loginPass
	} else if selectedKey != "" {
		host.KeyPath = selectedKey
	}

	st.SaveHost(host)

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

	s.render(w, r, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>`+i18n.T(r.Context(), "Node %s captured! Running: %v, Version: %s.")+`%s</span>
		<button class="btn btn-sm btn-ghost" hx-get="/ui/nodes" hx-target="#main-content" hx-push-url="true">`+i18n.T(r.Context(), "Refresh Nodes")+`</button></div>`,
		id, status.Running, status.Version, installMsg,
	)})
}

func (s *Server) handleNodeCaptureForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeCaptureForm(host, settings, allKeys))
}

func (s *Server) handleNodeInboundsForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.store().GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{Host: model.Host{ID: id}}
	}
	settings, _ := s.store().GetSettings()
	if len(settings.CustomPresets) > 0 {
		var customs []chain.ConnectionPreset
		if json.Unmarshal(settings.CustomPresets, &customs) == nil && len(customs) > 0 {
			_ = chain.LoadPresets(customs)
		}
	}
	users, _ := s.store().ListUsers()
	presets := chain.ListPresets()

	// Build protocol→presets JSON for client-side filtering (embedded in dialog data attribute)
	protocolPresets := map[string][]string{
		"awg":           chain.ListPresetsForProtocol("awg"),
		"tuic":          chain.ListPresetsForProtocol("tuic"),
		"vless-reality": chain.ListPresetsForProtocol("vless-reality"),
		"shadowsocks":   chain.ListPresetsForProtocol("shadowsocks"),
		"trojan":        chain.ListPresetsForProtocol("trojan"),
		"vmess":         chain.ListPresetsForProtocol("vmess"),
		"hysteria2":     chain.ListPresetsForProtocol("hysteria2"),
		"telemt":        chain.ListPresetsForProtocol("telemt"),
	}
	presetsJSON, _ := json.Marshal(protocolPresets)

	s.render(w, r, templates.NodeInboundsForm(info, users, presets, string(presetsJSON)))
}

func (s *Server) handleSaveNodeInbounds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{Host: model.Host{ID: id}}
	}

	protocols := r.Form["proto"]
	ports := r.Form["port"]
	indexes := r.Form["inbound_index"]
	obfuscations := r.Form["obfuscation"]

	// Guard: refuse to save zero inbounds if no chain-managed ones exist either.
	chainInbounds := 0
	for _, ib := range info.Inbounds {
		if ib.Source != "" {
			chainInbounds++
		}
	}
	if len(protocols) == 0 && chainInbounds == 0 {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-warning"><svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"/></svg><span>` + i18n.T(r.Context(), "Cannot save zero inbounds. Add at least one inbound or delete the node instead.") + `</span></div>`})
		return
	}

	// Port conflict check against all chains containing this node
	chainsForNode, _ := st.GetChainsForNode(id)
	chainPorts := make(map[int]string)
	for _, c := range chainsForNode {
		for i, n := range c.Nodes {
			if n.ID != id {
				continue
			}
			port := n.Port
			if port == 0 {
				if i == 0 {
					port = 8443
				} else {
					port = 443
				}
			}
			chainPorts[port] = c.Name
		}
	}

	for _, pStr := range ports {
		port, _ := strconv.Atoi(pStr)
		if cName, ok := chainPorts[port]; ok {
			s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Port %d is reserved for chain %q on this node and cannot be used for standalone inbounds.")+`</div>`, port, cName)})
			return
		}
	}

	// Start with existing chain-managed inbounds (preserved, not editable in this form)
	inbounds := make([]model.NodeInbound, 0, len(protocols)+chainInbounds)
	for _, oldIb := range info.Inbounds {
		if oldIb.Source != "" {
			inbounds = append(inbounds, oldIb)
		}
	}
	for i := range protocols {
		if i >= len(indexes) {
			continue
		}
		port, _ := strconv.Atoi(ports[i])
		idx := indexes[i]

		forUsers := r.Form["for_users_"+idx]

		obf := ""
		if i < len(obfuscations) {
			obf = obfuscations[i]
		}

		newIb := model.NodeInbound{
			Protocol:    protocols[i],
			Port:        port,
			ForUsers:    forUsers,
			Obfuscation: obf,
		}

		// Preserve existing generated credentials if port and protocol match
		for _, oldIb := range info.Inbounds {
			if oldIb.Protocol == newIb.Protocol && oldIb.Port == newIb.Port {
				newIb.UUID = oldIb.UUID
				newIb.ServerPrivKey = oldIb.ServerPrivKey
				newIb.ServerPubKey = oldIb.ServerPubKey
				newIb.ShortID = oldIb.ShortID
				newIb.TLSCertificate = oldIb.TLSCertificate
				newIb.TLSPrivateKey = oldIb.TLSPrivateKey
				newIb.AWGClientPub = oldIb.AWGClientPub
				newIb.AWGClientPriv = oldIb.AWGClientPriv
				break
			}
		}

		// Generate self-signed TLS certificate for protocols that need it (TUIC, Hysteria2, etc.)
		// if not already present. This ensures the inbound can be applied without "missing certificate" errors.
		if (newIb.Protocol == "tuic" || newIb.Protocol == "hysteria2") &&
			(newIb.TLSCertificate == "" || newIb.TLSPrivateKey == "") {

			preset := chain.GetDefaultPreset()
			serverName := chain.ResolveServerName(&preset)

			if cert, key, cerr := chain.GenerateSelfSignedCert(serverName); cerr == nil {
				newIb.TLSCertificate = cert
				newIb.TLSPrivateKey = key
			}
		}

		if newIb.Protocol == "awg" && newIb.AWGClientPub == "" {
			if priv, pub, cerr := chain.GenerateWireGuardKeypair(); cerr == nil {
				newIb.AWGClientPub = pub
				newIb.AWGClientPriv = priv
			}
		}

		inbounds = append(inbounds, newIb)
	}
	info.Inbounds = inbounds
	st.SaveNodeInfo(info)
	chain.WriteAudit(st, "update", "node", info.ID, chain.AuditPayload{"inbounds": len(inbounds)}, "operator")
	chain.ScheduleAutoApply(info.ID, "inbounds update")
	s.render(w, r, &simpleHTML{html: `<div class="alert alert-success">` + i18n.T(r.Context(), "Inbounds saved.") + `</div>`})
}

// ─── Spider Web ────────────────────────────────────────────────────────────────

func (s *Server) handleSpiderWeb(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	infos, _ := st.ListNodeInfos()
	links, _ := st.ListLinks()
	s.renderContent(w, r, i18n.T(r.Context(), "Spider Web"), templates.SpiderWeb(hosts, chains, infos, links))
}

func (s *Server) handleCreateSpiderLink(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	fromNode := strings.TrimSpace(r.FormValue("from_node"))
	toNode := strings.TrimSpace(r.FormValue("to_node"))
	transport := strings.TrimSpace(r.FormValue("transport"))
	chainName := strings.TrimSpace(r.FormValue("chain_name"))

	if fromNode == "" || toNode == "" || chainName == "" {
		http.Error(w, i18n.T(r.Context(), "from_node, to_node, and chain_name are required"), http.StatusBadRequest)
		return
	}
	if fromNode == toNode {
		http.Error(w, i18n.T(r.Context(), "from_node and to_node must differ"), http.StatusBadRequest)
		return
	}
	if transport == "" {
		transport = "xhttp"
	}

	st := s.store()

	// Resolve hosts (validates both ends exist).
		fromHost, err := st.GetHost(fromNode)
		if err != nil {
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), fromNode), http.StatusBadRequest)
			return
		}
		toHost, err := st.GetHost(toNode)
		if err != nil {
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), toNode), http.StatusBadRequest)
		return
	}

	// 1. Persist the graph edge (source of truth for topology).
	link := &model.ConnectionLink{
		FromNodeID: fromNode,
		ToNodeID:   toNode,
		Transport:  model.TransportType(transport),
		ChainName:  chainName,
	}
	if err := st.SaveLink(link); err != nil {
		// Duplicate edge — not fatal, just continue to sync the chain list.
		_ = err
	}

	// 2. Sync Chain.Nodes (materialized deploy path): insert toNode right after
	// fromNode if not already present in the chain's ordered list.
	existing, err := st.GetChain(chainName)
	var nodes []model.ChainNode
	if err == nil {
		nodes = existing.Nodes
	} else {
		nodes = []model.ChainNode{}
	}

	fromCN := model.ChainNode{ID: fromHost.ID, Addr: fromHost.Addr, User: fromHost.User, KeyPath: fromHost.KeyPath}
	toCN := model.ChainNode{ID: toHost.ID, Addr: toHost.Addr, User: toHost.User, KeyPath: toHost.KeyPath}

	// Ensure fromNode is in the list.
	fromIdx := indexOfChainNode(nodes, fromNode)
	if fromIdx < 0 {
		nodes = append(nodes, fromCN)
		fromIdx = len(nodes) - 1
	}
	// Ensure toNode is present; if missing, insert right after fromNode so the
	// deploy order reflects the edge direction.
	if indexOfChainNode(nodes, toNode) < 0 {
		insertAt := fromIdx + 1
		if insertAt > len(nodes) {
			insertAt = len(nodes)
		}
		nodes = append(nodes, model.ChainNode{})
		copy(nodes[insertAt+1:], nodes[insertAt:])
		nodes[insertAt] = toCN
	}

	ch := &model.Chain{
		Name:      chainName,
		Nodes:     nodes,
		Strategy:  model.StrategyURLTest,
		Transport: model.TransportType(transport),
	}
	st.SaveChain(ch)
	chain.WriteAudit(st, "create", "link", link.ID, chain.AuditPayload{"from": fromNode, "to": toNode, "chain": chainName, "transport": transport}, "operator")

	// Re-render with full data from store (including links now).
	s.renderSpider(w, r, st)
}

func (s *Server) handleDeleteSpiderLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	link, err := st.GetLink(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "link not found"), http.StatusNotFound)
		return
	}
	chainName := link.ChainName
	fromNode := link.FromNodeID
	toNode := link.ToNodeID

	if err := st.DeleteLink(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	chain.WriteAudit(st, "delete", "link", id, chain.AuditPayload{"chain": chainName, "from": fromNode, "to": toNode}, "operator")

	// Sync Chain.Nodes: remove nodes that no longer have ANY edge in this chain.
	remainingLinks, _ := st.ListLinksForChain(chainName)
	used := map[string]bool{}
	for _, l := range remainingLinks {
		used[l.FromNodeID] = true
		used[l.ToNodeID] = true
	}
	c, err := st.GetChain(chainName)
	if err == nil {
		filtered := c.Nodes[:0]
		for _, n := range c.Nodes {
			if used[n.ID] {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) == 0 {
			st.DeleteChain(chainName)
		} else {
			c.Nodes = filtered
			st.SaveChain(c)
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// handleSaveNodePosition persists a node's spider-web drag coordinates. Called
// on mouseup after dragging a node. Body params: x, y (float).
func (s *Server) handleSaveNodePosition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	x, _ := strconv.ParseFloat(r.FormValue("x"), 64)
	y, _ := strconv.ParseFloat(r.FormValue("y"), 64)
	if err := s.store().SaveNodePosition(id, x, y); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// renderSpider loads all spider data (hosts/chains/infos/links) and renders the
// SpiderWeb template. Shared by handleSpiderWeb and the link create/delete flows.
func (s *Server) renderSpider(w http.ResponseWriter, r *http.Request, st *chain.Store) {
	allHosts, _ := st.ListHosts()
	allChains, _ := st.ListChains()
	allInfos, _ := st.ListNodeInfos()
	allLinks, _ := st.ListLinks()
	s.render(w, r, templates.SpiderWeb(allHosts, allChains, allInfos, allLinks))
}

// indexOfChainNode returns the index of nodeID in nodes, or -1.
func indexOfChainNode(nodes []model.ChainNode, nodeID string) int {
	for i, n := range nodes {
		if n.ID == nodeID {
			return i
		}
	}
	return -1
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

	s.renderContent(w, r, i18n.T(r.Context(), "Users"), templates.Users(users, chains))
}

func (s *Server) handleNewUserForm(w http.ResponseWriter, r *http.Request) {
	chains, _ := s.store().ListChains()
	s.render(w, r, templates.UserForm(nil, chains))
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
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
		http.Error(w, i18n.T(r.Context(), "id and name are required"), http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "create", "user", u.ID, chain.AuditPayload{"name": u.Name, "protocols": u.Protocols}, "operator")
	s.scheduleAutoApplyForUser(st, u, "user create")
	s.render(w, r, templates.UserRow(u))
}

func (s *Server) handleEditUserForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, err := s.store().GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	chains, _ := s.store().ListChains()
	s.render(w, r, templates.UserForm(u, chains))
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
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
	chain.WriteAudit(st, "update", "user", u.ID, chain.AuditPayload{"name": u.Name, "protocols": u.Protocols}, "operator")
	s.scheduleAutoApplyForUser(st, u, "user update")
	if isHTMXRequest(r) {
		s.render(w, r, templates.UserRow(u))
	} else {
		http.Redirect(w, r, "/ui/users", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	// Load the user first so we can audit + schedule auto-apply on the chains it
	// belonged to before deletion.
	if u, err := st.GetUser(id); err == nil {
		chain.WriteAudit(st, "delete", "user", id, chain.AuditPayload{"name": u.Name}, "operator")
		s.scheduleAutoApplyForUser(st, u, "user delete")
	}
	if err := st.DeleteUser(id); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "delete: %v"), err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// scheduleAutoApplyForUser triggers background deploys on every node of every
// chain the user belongs to. Best-effort: missing chains/nodes are skipped.
func (s *Server) scheduleAutoApplyForUser(st *chain.Store, u *model.User, reason string) {
	if u == nil {
		return
	}
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		for _, n := range c.Nodes {
			chain.ScheduleAutoApply(n.ID, reason+":"+chainName)
		}
	}
}

func (s *Server) handleUserConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "user not found"), http.StatusNotFound)
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
			Description: fmt.Sprintf(i18n.T(r.Context(), "%s chain — %d hops, strategy: %s"), chainName, len(c.Nodes), c.Strategy),
		})
	}

	// Generate configs for standalone inbounds assigned to this user
	nodes, _ := st.ListNodeInfos()
	for _, node := range nodes {
		for _, ib := range node.Inbounds {
			if contains(ib.ForUsers, u.ID) {
				link := buildStandaloneLink(node.Addr, ib, u)
				configs = append(configs, templates.UserChainConfig{
					ChainName:   "node: " + node.ID,
					Protocol:    ib.Protocol,
					ConfigLink:  link,
					Description: fmt.Sprintf(i18n.T(r.Context(), "Standalone inbound on %s (port %d)"), node.ID, ib.Port),
				})
			}
		}
	}

	// If no configs assigned, list available chains for the user to choose from
	if len(configs) == 0 {
		allChains, _ := st.ListChains()
		if len(allChains) > 0 {
			configs = append(configs, templates.UserChainConfig{
				ChainName:   "unassigned",
				Protocol:    "any",
				ConfigLink:  "# Assign chains or node inbounds to this user to generate configs.",
				Description: fmt.Sprintf(i18n.T(r.Context(), "User has no chains assigned. %d chain(s) available — edit user to assign."), len(allChains)),
			})
		} else {
			configs = append(configs, templates.UserChainConfig{
				ChainName:   "no-chains",
				Protocol:    "any",
				ConfigLink:  "# Create a chain or node inbound first, then assign it to this user.",
				Description: i18n.T(r.Context(), "No chains or standalone inbounds exist yet."),
			})
		}
	}

	s.render(w, r, templates.UserConfigView(u, configs))
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

	ip := strings.Split(entry.Addr, ":")[0]
	return buildClientURI(proto, ip, 8443, c.TUICEntryUserUUID, c.TUICEntryUserPassword, c.AWGEntryServerPub, "", c.Name, u)
}

func buildStandaloneLink(addr string, ib model.NodeInbound, u *model.User) string {
	ip := strings.Split(addr, ":")[0]
	if ib.Protocol == "awg" && u.ImportedSecret == "" && ib.AWGClientPriv != "" {
		// Full AWG client .conf with Amnezia obfuscation params from the active
		// preset (Jc/Jmin/Jmax/S1-S4/H1-H4 + I1-I5 when CPS is enabled). This
		// matches the server-side amnezia block so the client connects.
		return buildAWGClientConf(ip, ib.Port, ib.AWGClientPriv, ib.ServerPubKey, ib.AWGClientPub, "")
	}
	if ib.Protocol == "awg" && u.ImportedSecret != "" && u.SecretType == "awg" {
		// Imported AWG private key — build a .conf using it.
		return buildAWGClientConf(ip, ib.Port, u.ImportedSecret, ib.ServerPubKey, "", "")
	}
	if ib.Protocol == "mtproxy" {
		full, err := chain.MTProxyFullSecret(ib.UUID, defaultFakeTLSDomain(ib))
		if err != nil {
			full = "ee" + ib.UUID
		}
		return fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", ip, ib.Port, full)
	}
	return buildClientURI(ib.Protocol, ip, ib.Port, ib.UUID, ib.ServerPrivKey, ib.ServerPubKey, ib.ShortID, ib.Protocol, u)
}

// defaultFakeTLSDomain returns the MTProxy FakeTLS domain for an inbound (the
// Obfuscation field if set, else the canonical "disk.yandex.ru").
func defaultFakeTLSDomain(ib model.NodeInbound) string {
	if ib.Obfuscation != "" {
		return ib.Obfuscation
	}
	return "disk.yandex.ru"
}

// buildAWGClientConf renders a full awg-quick client .conf with Amnezia params.
// hostOverride lets callers swap the domain/IP for the Endpoint line.
func buildAWGClientConf(ip string, port int, clientPriv, serverPub, clientPub, hostOverride string) string {
	host := hostOverride
	if host == "" {
		host = ip
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("Address = 10.8.0.2/24\n")
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", clientPriv))
	b.WriteString("MTU = 1420\n\n")
	b.WriteString("[Peer]\n")
	b.WriteString(fmt.Sprintf("PublicKey = %s\n", serverPub))
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	b.WriteString(fmt.Sprintf("Endpoint = %s:%d\n", host, port))
	b.WriteString("PersistentKeepalive = 25\n")
	// Amnezia params from the active preset's AWG section.
	if preset := chain.GetDefaultPreset(); preset.AWG != nil {
		amn := chain.BuildAWGAmnezia(preset.AWG, &preset)
		if amn != nil {
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("Jc = %d\n", amn.JC))
			b.WriteString(fmt.Sprintf("Jmin = %d\n", amn.JMIN))
			b.WriteString(fmt.Sprintf("Jmax = %d\n", amn.JMAX))
			b.WriteString(fmt.Sprintf("S1 = %d\n", amn.S1))
			b.WriteString(fmt.Sprintf("S2 = %d\n", amn.S2))
			b.WriteString(fmt.Sprintf("S3 = %d\n", amn.S3))
			b.WriteString(fmt.Sprintf("S4 = %d\n", amn.S4))
			b.WriteString(fmt.Sprintf("H1 = %s\n", amn.H1))
			b.WriteString(fmt.Sprintf("H2 = %s\n", amn.H2))
			b.WriteString(fmt.Sprintf("H3 = %s\n", amn.H3))
			b.WriteString(fmt.Sprintf("H4 = %s\n", amn.H4))
			if amn.I1 != "" {
				b.WriteString(fmt.Sprintf("I1 = %s\n", amn.I1))
				b.WriteString(fmt.Sprintf("I2 = %s\n", amn.I2))
				b.WriteString(fmt.Sprintf("I3 = %s\n", amn.I3))
				b.WriteString(fmt.Sprintf("I4 = %s\n", amn.I4))
				b.WriteString(fmt.Sprintf("I5 = %s\n", amn.I5))
			}
		}
	}
	_ = clientPub
	return b.String()
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// buildClientURI is a unified helper for generating copy-pasteable client configurations (URIs)
func buildClientURI(proto, ip string, port int, uuid, privKey, pubKey, shortID, name string, u *model.User) string {
	switch proto {
	case "awg":
		if u != nil && u.ImportedSecret != "" && u.SecretType == "awg" {
			return buildAWGClientConf(ip, port, u.ImportedSecret, pubKey, "", "")
		}
		return fmt.Sprintf("awg://%s:%d?pub=%s&psk=&mtu=1420", ip, port, pubKey)
	case "tuic":
		if u != nil && u.ImportedSecret != "" && u.SecretType == "tuic" {
			return fmt.Sprintf("tuic://%s:%s@%s:%d?congestion_control=bbr&alpn=h3",
				u.ImportedSecret, u.ImportedSecret, ip, port)
		}
		// For standalone TUIC, password is in privKey. For chains, it's passed explicitly in privKey argument.
		return fmt.Sprintf("tuic://%s:%s@%s:%d?congestion_control=bbr&alpn=h3",
			uuid, privKey, ip, port)
	case "vless-reality":
		serverName := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			serverName = preset.Reality.ServerNames[0]
		}
		return fmt.Sprintf("vless://%s@%s:%d?type=tcp&security=reality&pbk=%s&sni=%s&sid=%s&fp=chrome&flow=xtls-rprx-vision",
			uuid, ip, port, pubKey, serverName, shortID)
	case "vless-reality-xhttp":
		// Combined REALITY + XHTTP max obfuscation share-link. Values are
		// percent-encoded (per protocol_presets.generate_vless_reality_xhttp_share_link),
		// unlike the plain vless-reality link above.
		sni := "www.microsoft.com"
		path := "/api"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			sni = preset.Reality.ServerNames[0]
		}
		q := url.Values{}
		q.Set("type", "xhttp")
		q.Set("security", "reality")
		q.Set("sni", sni)
		q.Set("fp", "chrome")
		q.Set("pbk", pubKey)
		q.Set("sid", shortID)
		q.Set("spx", "/")
		q.Set("flow", "xtls-rprx-vision")
		q.Set("mode", "packet-up")
		q.Set("path", path)
		q.Set("host", sni)
		return fmt.Sprintf("vless://%s@%s:%d?%s#vless-reality-xhttp", uuid, ip, port, q.Encode())
	case "xhttp":
		serverName := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.XHTTP != nil && len(preset.XHTTP.Hosts) > 0 {
			serverName = preset.XHTTP.Hosts[0]
		}
		return fmt.Sprintf("vless://%s@%s:%d?type=xhttp&security=none&host=%s&path=%%2Fapi",
			uuid, ip, port, serverName)
	case "vmess":
		// vmess:// + base64(JSON{v,ps,add,port,id,aid,net,type,tls})
		obj := map[string]string{
			"v": "2", "ps": name, "add": ip, "port": fmt.Sprintf("%d", port),
			"id": uuid, "aid": "0", "net": "tcp", "type": "none", "tls": "",
		}
		b, _ := json.Marshal(obj)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "trojan":
		sni := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			sni = preset.Reality.ServerNames[0]
		}
		return fmt.Sprintf("trojan://%s@%s:%d?type=tcp&security=tls&sni=%s#%s",
			privKey, ip, port, sni, name)
	case "shadowsocks", "ss":
		cipher := chain.SS_DEFAULT_CIPHER
		password := privKey
		if password == "" {
			password = chain.GenerateSSPassword(cipher)
		}
		userinfo := base64.StdEncoding.EncodeToString([]byte(cipher + ":" + password))
		return fmt.Sprintf("ss://%s@%s:%d#%s", userinfo, ip, port, name)
	case "hysteria2":
		return fmt.Sprintf("hysteria2://%s@%s:%d?obfs=salamander&obfs-password=salamander_pass&insecure=1",
			uuid, ip, port)
	case "mtproxy":
		fakeDomain := "disk.yandex.ru"
		full, err := chain.MTProxyFullSecret(privKey, fakeDomain)
		if err != nil {
			full = "ee" + privKey
		}
		return fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", ip, port, full)
	default:
		return fmt.Sprintf("# %s config for %s:%d", proto, ip, port)
	}
}

func (s *Server) handleUserQR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "user not found"), http.StatusNotFound)
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

	// Also include standalone inbounds assigned to this user.
	nodes, _ := st.ListNodeInfos()
	for _, node := range nodes {
		for _, ib := range node.Inbounds {
			if contains(ib.ForUsers, u.ID) {
				links = append(links, buildStandaloneLink(node.Addr, ib, u))
			}
		}
	}

	s.render(w, r, templates.UserQRView(u, links))
}

// ─── QR Image ───────────────────────────────────────────────────────────────────

// handleQRImage generates a QR code PNG for the given data query parameter.
func (s *Server) handleQRImage(w http.ResponseWriter, r *http.Request) {
	data := r.URL.Query().Get("data")
	if data == "" {
		http.Error(w, i18n.T(r.Context(), "missing data parameter"), http.StatusBadRequest)
		return
	}

	png, err := qrcode.Encode(data, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "qr generation failed"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

// ─── Settings ──────────────────────────────────────────────────────────────────

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	settings, _ := st.GetSettings()
	hosts, _ := st.ListHosts()
	chains, _ := st.ListChains()
	sysKeys := detectSystemKeys()

	// Ensure we pass the config properties (safe fallbacks if cfg is nil in some tests)
	authEnabled := false
	authUsername := ""
	listenAddr := ""
	if s.cfg != nil {
		authEnabled = s.cfg.AuthEnabled
		authUsername = s.cfg.AuthUsername
		listenAddr = s.cfg.ListenAddr
	}

	activeListenAddr := s.ActiveListenAddr
	s.renderContent(w, r, i18n.T(r.Context(), "Settings"), templates.Settings(settings, hosts, chains, authEnabled, authUsername, listenAddr, activeListenAddr, sysKeys))
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	settings, _ := st.GetSettings()

	// 1. Config updates (Auth)
	configNeedsSave := false
	portChanged := false
	if s.cfg != nil {
		newUsername := strings.TrimSpace(r.FormValue("auth_username"))
		if newUsername != "" && newUsername != s.cfg.AuthUsername {
			s.cfg.AuthUsername = newUsername
			configNeedsSave = true
		}

		newListenAddr := strings.TrimSpace(r.FormValue("listen_addr"))
		if newListenAddr != "" && newListenAddr != s.cfg.ListenAddr {
			s.cfg.ListenAddr = newListenAddr
			configNeedsSave = true
			portChanged = true
		}

		newAuthEnabled := r.FormValue("auth_enabled") == "on"
		if newAuthEnabled != s.cfg.AuthEnabled {
			s.cfg.AuthEnabled = newAuthEnabled
			configNeedsSave = true
			if newAuthEnabled {
				w.Header().Set("HX-Refresh", "true")
			}
		}

		newPassword := strings.TrimSpace(r.FormValue("auth_new_password"))
		oldPassword := strings.TrimSpace(r.FormValue("auth_old_password"))

		if newPassword != "" {
			if s.cfg.AuthPasswordHash != "" && s.cfg.AuthEnabled {
				// Require valid old password if auth is currently enabled
				err := bcrypt.CompareHashAndPassword([]byte(s.cfg.AuthPasswordHash), []byte(oldPassword))
				if err != nil {
					s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Failed to change password: old password is incorrect.") + `</span></div>`})
					return
				}
			}

			// Hash new password
			hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, i18n.T(r.Context(), "failed to hash password"), http.StatusInternalServerError)
				return
			}
			s.cfg.AuthPasswordHash = string(hash)
			configNeedsSave = true
			w.Header().Set("HX-Refresh", "true")
		}

		if configNeedsSave {
			// Save config to default location
			if err := s.cfg.Save(config.DefaultConfigPath()); err != nil {
				log.Printf("failed to save config: %v", err)
				s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error"><span>`+i18n.T(r.Context(), "Settings saved, but config write failed: %v")+`</span></div>`, err)})
				return
			}
		}
	}

	// 2. PanelSettings updates (store.json)
	settings.PanelCountry = strings.TrimSpace(r.FormValue("panel_country"))
	oldLang := settings.Language
	settings.Language = strings.TrimSpace(r.FormValue("language"))
	if oldLang != settings.Language {
		w.Header().Set("HX-Refresh", "true")
	}

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

	if portChanged {
		msg := fmt.Sprintf(`
		<div class="alert alert-warning shadow-lg mt-2">
			<svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" /></svg>
			<div>
				<h3 class="font-bold">%s</h3>
				<div class="text-xs mt-1">%s</div>
				<div class="mt-2 text-xs font-mono bg-base-300 p-1.5 rounded inline-block">systemctl restart angry-box</div>
			</div>
		</div>`, i18n.T(r.Context(), "Port changed!"), i18n.T(r.Context(), "Please restart the angry-box service manually to apply the new port."))
		s.render(w, r, &simpleHTML{html: msg})
		return
	}

	s.render(w, r, &simpleHTML{html: `<div class="alert alert-success"><span>` + i18n.T(r.Context(), "Settings saved.") + `</span></div>`})
}

// ─── SSH Keys ──────────────────────────────────────────────────────────────────

func (s *Server) handleAddSSHKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	keyData := strings.TrimSpace(r.FormValue("key_data"))
	if name == "" || keyData == "" {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Name and key data are required.") + `</span></div>`})
		return
	}
	// Validate key format
	if !looksLikePrivateKey(keyData) {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid key format. Expected a private key (BEGIN ... PRIVATE KEY).") + `</span></div>`})
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
	s.render(w, r, templates.SSHKeyList(settings, sysKeys))
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
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	settings.SSHKeys = filtered
	st.SaveSettings(settings)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
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
func looksLikePrivateKey(data string) bool {
	data = strings.TrimSpace(data)
	// Must have a proper PEM header and matching footer
	if !strings.HasPrefix(data, "-----BEGIN ") || !strings.Contains(data, " PRIVATE KEY-----") {
		return false
	}
	// Extract the type from header (e.g., "OPENSSH PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY")
	headerEnd := strings.Index(data, "-----")
	if headerEnd < 0 {
		return false
	}
	header := data[:headerEnd+5] // include trailing "-----"
	footer := strings.Replace(header, "BEGIN", "END", 1)
	return strings.Contains(data, footer)
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
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
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
		http.Error(w, i18n.T(r.Context(), "id, addr and keyPath are required"), http.StatusBadRequest)
		return
	}
	st := s.store()
	if err := st.SaveHost(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save failed: %v"), err), http.StatusInternalServerError)
		return
	}
	s.render(w, r, templates.HostRow(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}))
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, i18n.T(r.Context(), "missing id"), http.StatusBadRequest)
		return
	}
	if err := s.store().DeleteHost(id); err != nil {
		msg := fmt.Sprintf(`<tr class="bg-error/10"><td colspan="6" class="p-4 text-error font-medium">`+i18n.T(r.Context(), "Failed to delete: %v")+`. <button class="btn btn-xs btn-outline ml-4" onclick="location.reload()">Dismiss</button></td></tr>`, err)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(msg))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

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
	f := factory.New()
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

	f := factory.New()
	applier := chain.NewApplier(f)
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

	f := factory.New()
	applier := chain.NewApplier(f)
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
		s.renderContent(w, r, i18n.T(r.Context(), "Audit"), &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "failed to load audit log: %s")+`</div>`, err.Error())})
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
			a.TS.Format("2006-01-02 15:04:05"), a.Action, a.TargetType, a.TargetID, a.Actor, truncForDisplay(a.PayloadJSON, 80)))
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
			row.Name, row.Role, status, last, truncForDisplay(row.LastDeployedHash, 12)))
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Deploy Status"), &simpleHTML{html: b.String()})
}

func (s *Server) handleDeployStatusJSON(w http.ResponseWriter, r *http.Request) {
	s.renderJSON(w, s.computeDeployStatusRows(r))
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
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "node not found: %s")+`</div>`, err.Error())})
		return
	}
	det, err := takeover.DetectVPN(r.Context(), info.Host, info.UseSudo)
	if err != nil {
		chain.WriteAudit(st, "takeover", "node", id, chain.AuditPayload{"phase": "detect", "error": err.Error()}, "operator")
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Detect failed: %s")+`</div>`, err.Error())})
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
		det.Type, det.ServiceName, det.IsActive, det.IsEnabled, det.ConfigPath))
	b.WriteString(`</div></div>`)
	if len(det.Other) > 0 {
		b.WriteString(`<div class="text-sm text-base-content/60 mt-1">` + i18n.T(r.Context(), "Also present: ") + strings.Join(det.Other, ", ") + `</div>`)
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
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "node not found: %s")+`</div>`, err.Error())})
		return
	}
	// Re-detect (the detection from the modal isn't POSTed; re-probe to be safe).
	det, err := takeover.DetectVPN(r.Context(), info.Host, info.UseSudo)
	if err != nil {
		chain.WriteAudit(st, "takeover", "node", id, chain.AuditPayload{"phase": "detect", "error": err.Error()}, "operator")
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Detect failed: %s")+`</div>`, err.Error())})
		return
	}
	res, err := takeover.Takeover(r.Context(), st, factory.New(), info.Host, info.UseSudo, det)

	// Render the result.
	var b strings.Builder
	if err != nil && res != nil && res.Status != "taken" {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-error"><b>`+i18n.T(r.Context(), "Takeover %s")+`</b><br>%s</div>`, res.Status, res.Message))
	} else if err != nil {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Takeover failed: %s")+`</div>`, err.Error()))
	} else {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-success"><b>`+i18n.T(r.Context(), "Takeover successful")+`</b><br>%s</div>`, res.Message))
	}
	if res != nil {
		b.WriteString(`<div class="card bg-base-100 shadow mt-2"><div class="card-body text-sm">`)
		b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "From:")+` <b>%s</b> → sing-box</p>`, res.FromType))
		if res.OldService != "" {
			b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "Old service:")+` <code>%s</code> (`+i18n.T(r.Context(), "disabled, config backed up at")+` <code>%s</code>)</p>`, res.OldService, res.OldConfigBackup))
		}
		b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "Converted inbounds: %d")+`</p>`, res.ConvertedInbounds))
		if res.RollbackOccurred {
			b.WriteString(`<p><b>`+i18n.T(r.Context(), "Rollback occurred")+`</b> — `+i18n.T(r.Context(), "old VPN was restored.")+`</p>`)
		}
		b.WriteString(`</div></div>`)
	}
	s.renderContent(w, r, i18n.T(r.Context(), "Takeover result"), &simpleHTML{html: b.String()})
}

// ─── Protocol presets / credential generators ────────────────────────────────

func (s *Server) handleProtocolPresetsJSON(w http.ResponseWriter, r *http.Request) {
	s.renderJSON(w, chain.GetProtocolPresetsCatalog())
}

func (s *Server) handleCryptoWGKeypair(w http.ResponseWriter, r *http.Request) {
	priv, pub, err := chain.GenerateRealityKeypair()
	if err != nil {
		s.renderJSON(w, map[string]string{"error": err.Error()})
		return
	}
	psk, _ := chain.GenerateWGPresharedKey()
	s.renderJSON(w, map[string]string{
		"private_key":    priv,
		"public_key":     pub,
		"preshared_key":  psk,
	})
}

func (s *Server) handleCryptoMTProxySecret(w http.ResponseWriter, r *http.Request) {
	s.renderJSON(w, map[string]string{"secret_hex": chain.GenerateMTProxySecret()})
}

func (s *Server) handleCryptoProxyCreds(w http.ResponseWriter, r *http.Request) {
	s.renderJSON(w, map[string]string{
		"uuid":     chain.GenerateTUICUUID(),
		"password": chain.GenerateProxyPassword(),
	})
}

// handleProtocolsGenerate generates the credentials requested via ?what=...
// (comma-separated tokens). Only requested fields are set in the response.
func (s *Server) handleProtocolsGenerate(w http.ResponseWriter, r *http.Request) {
	what := r.URL.Query().Get("what")
	ssCipher := r.URL.Query().Get("ss_cipher")
	if ssCipher == "" {
		ssCipher = chain.SS_DEFAULT_CIPHER
	}
	out := map[string]string{}
	for _, token := range strings.Split(what, ",") {
		switch strings.TrimSpace(token) {
		case "reality_keypair":
			priv, pub, err := chain.GenerateRealityKeypair()
			if err == nil {
				out["reality_private_key"] = priv
				out["reality_public_key"] = pub
			}
		case "reality_short_id":
			out["reality_short_id"] = chain.GenerateRealityShortID()
		case "trojan_password":
			out["trojan_password"] = chain.GenerateTrojanPassword()
		case "ss_password":
			out["ss_password"] = chain.GenerateSSPassword(ssCipher)
		case "hysteria2_password":
			out["hysteria2_password"] = chain.GenerateHysteria2Password()
			out["hysteria2_obfs_password"] = chain.GenerateHysteria2ObfsPassword()
		case "tuic_uuid":
			out["tuic_uuid"] = chain.GenerateTUICUUID()
			out["tuic_password"] = chain.GenerateTUICPassword()
		case "vmess_ws_path":
			out["vmess_ws_path"] = chain.GenerateVMessWSPath()
		}
	}
	s.renderJSON(w, out)
}

// handleAWGPresetJSON returns an AWG obfuscation preset for ?profile=lite|standard|pro.
func (s *Server) handleAWGPresetJSON(w http.ResponseWriter, r *http.Request) {
	profile := chain.AWGProfileName(r.URL.Query().Get("profile"))
	if profile == "" {
		profile = chain.AWGProfilePro
	}
	params := chain.GenAWGParams(profile)
	s.renderJSON(w, map[string]any{
		"profile":           string(profile),
		"params":            params,
		"singbox_amnezia":   chain.ToSingboxAmnezia(params, 50),
		"awg_conf_lines":    chain.ToAWGConfLines(params),
	})
}

// handleAWGCPSJSON returns synthesized CPS I1-I5 packets for ?profile=tls|dns|sip|quic.
func (s *Server) handleAWGCPSJSON(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		profile = "tls"
	}
	domain := r.URL.Query().Get("domain")
	level := 3
	mimicry := profile
	if mimicry == "quic" || mimicry == "tls" || mimicry == "dns" || mimicry == "sip" {
		// ok
	} else {
		mimicry = "tls"
	}
	mat := chain.GenerateAWGObfsMaterial(level, mimicry)
	strs := chain.CPSMaterialStrings(mat)
	if profile == "dns" && domain != "" {
		// dns profile: first packet uses <r 2><b 0x...> over the requested domain
		strs[0] = chain.CPSDNSString(mat.I1)
	}
	resp := map[string]any{
		"profile": profile,
		"domain":  domain,
		"packets": strs[:],
	}
	s.renderJSON(w, resp)
}

// handleAWGCaptureJSON triggers a live QUIC capture against ?domain=... (Feature 1).
func (s *Server) handleAWGCaptureJSON(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	res := chain.CaptureQUICSignature(domain, 0)
	s.renderJSON(w, res)
}

// handleNodeConfigPreview renders a node's merged config without pushing it.
func (s *Server) handleNodeConfigPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		s.renderJSON(w, map[string]string{"error": err.Error()})
		return
	}
	cfg, _, err := chain.RenderMergedNodeConfig(info, nil)
	if err != nil {
		s.renderJSON(w, map[string]string{"error": "render: " + err.Error()})
		return
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	s.renderJSON(w, map[string]string{"node_id": id, "config": string(b)})
}

// handleImportAWG SSH-imports existing AWG configs from the node and back-fills
// placeholder inbound fields, then persists the NodeInfo.
func (s *Server) handleImportAWG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "node not found: %s")+`</div>`, err.Error())})
		return
	}
	res, err := chain.ImportAWGConfigs(info.Host, info.UseSudo, info)
	if err != nil {
		chain.WriteAudit(st, "import", "node", id, chain.AuditPayload{"error": err.Error()}, "operator")
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "AWG import failed: %s")+`</div>`, err.Error())})
		return
	}
	_ = st.SaveNodeInfo(info)
	chain.WriteAudit(st, "import", "node", id, chain.AuditPayload{"exit_nodes": len(res.ExitNodes), "peers": len(res.Peers), "db_updated": res.DBUpdated}, "operator")

	var b strings.Builder
	b.WriteString(`<div class="alert alert-success">` + i18n.T(r.Context(), "AWG import complete.") + `</div>`)
	b.WriteString(`<div class="card bg-base-100 shadow"><div class="card-body">`)
	if res.ServerConfig != nil {
		b.WriteString(fmt.Sprintf(`<p>awg0.conf: `+i18n.T(r.Context(), "ListenPort=%d, Jc=%d, S1=%d")+`</p>`, res.ServerConfig.ListenPort, res.ServerConfig.JC, res.ServerConfig.S1))
	}
	b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "Exit nodes: %d, Peers: %d")+`</p>`, len(res.ExitNodes), len(res.Peers)))
	b.WriteString(fmt.Sprintf(`<p>`+i18n.T(r.Context(), "DB update: %s")+`</p>`, res.DBUpdated))
	if res.Log != "" {
		b.WriteString(fmt.Sprintf(`<pre class="text-xs whitespace-pre-wrap">%s</pre>`, res.Log))
	}
	b.WriteString(`</div></div>`)
	s.render(w, r, &simpleHTML{html: b.String()})
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
			p.Name, p.ClientType, p.ServerRole, auto, len(p.ServerIDs), p.ID, p.ID, p.Name))
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
		p.ID, p.Name, p.Description, p.ClientType, p.ServerRole, checked)
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

func (s *Server) handleProfilesJSON(w http.ResponseWriter, r *http.Request) {
	profiles, _ := s.store().ListProfiles()
	s.renderJSON(w, profiles)
}

// ─── Per-node route rules ───────────────────────────────────────────────────

func (s *Server) handleRouteRules(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	rules, _ := st.ListRouteRulesForNode(id)
	if rules == nil {
		rules = []*model.RouteRule{}
	}
	presets := chain.GetRoutingPresets("")
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<div class="space-y-4"><h2 class="text-2xl font-semibold">`+i18n.T(r.Context(), "Route Rules — %s")+`</h2>`, id))
	// Apply-preset shortcut.
	b.WriteString(`<div class="card bg-base-100 shadow"><div class="card-body"><h3 class="font-semibold">` + i18n.T(r.Context(), "Apply routing preset") + `</h3><form hx-post="/ui/nodes/` + id + `/route-rules" class="flex gap-2 items-end flex-wrap"><select name="match_values" class="select select-bordered select-sm">`)
	for _, p := range presets {
		b.WriteString(fmt.Sprintf(`<option value="%s">%s (%s, %d domains)</option>`, strings.Join(p.Domains, "\n"), p.Name, p.Category, len(p.Domains)))
	}
	b.WriteString(`</select><input type="hidden" name="match_type" value="domain_suffix"><input type="hidden" name="action" value="route"><input type="hidden" name="priority" value="100"><input type="hidden" name="enabled" value="on"><input type="hidden" name="preset_name" value="1"><button type="submit" class="btn btn-primary btn-sm">` + i18n.T(r.Context(), "Add rule from preset") + `</button></form></div></div>`)
	// Existing rules.
	b.WriteString(`<div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>` + i18n.T(r.Context(), "Priority") + `</th><th>` + i18n.T(r.Context(), "Match") + `</th><th>` + i18n.T(r.Context(), "Values") + `</th><th>` + i18n.T(r.Context(), "Action") + `</th><th>` + i18n.T(r.Context(), "Outbound") + `</th><th></th></tr></thead><tbody>`)
	for _, rr := range rules {
		en := ""
		if !rr.Enabled {
			en = " opacity-50"
		}
			b.WriteString(fmt.Sprintf(`<tr class="%s"><td>%d</td><td>%s</td><td class="font-mono text-xs">%s</td><td>%s</td><td>%s</td><td><button class="btn btn-ghost btn-xs text-error" hx-delete="/ui/nodes/%s/route-rules/%s" hx-confirm="`+i18n.T(r.Context(), "Delete rule?")+`" hx-target="closest tr" hx-swap="outerHTML">`+i18n.T(r.Context(), "Delete")+`</button></td></tr>`,
			en, rr.Priority, rr.MatchType, truncForDisplay(strings.ReplaceAll(rr.MatchValues, "\n", ", "), 40), rr.Action, rr.OutboundTag, id, rr.ID))
	}
	b.WriteString(`</tbody></table></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Route Rules"), &simpleHTML{html: b.String()})
}

func (s *Server) handleCreateRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	priority, _ := strconv.Atoi(r.FormValue("priority"))
	if priority == 0 {
		priority = 100
	}
	rr := &model.RouteRule{
		NodeID:      id,
		Priority:    priority,
		MatchType:   r.FormValue("match_type"),
		MatchValues: r.FormValue("match_values"),
		Action:      r.FormValue("action"),
		OutboundTag: r.FormValue("outbound_tag"),
		Comment:     r.FormValue("comment"),
		Enabled:     r.FormValue("enabled") == "on",
	}
	if rr.MatchType == "" {
		rr.MatchType = "domain_suffix"
	}
	if rr.Action == "" {
		rr.Action = "route"
	}
	if err := s.store().SaveRouteRule(rr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(s.store(), "create", "route_rule", rr.ID, chain.AuditPayload{"node_id": id, "match_type": rr.MatchType, "action": rr.Action}, "operator")
	chain.ScheduleAutoApply(id, "route rule create")
	http.Redirect(w, r, "/ui/nodes/"+id+"/route-rules", http.StatusSeeOther)
}

func (s *Server) handleDeleteRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rid := r.PathValue("rid")
	chain.WriteAudit(s.store(), "delete", "route_rule", rid, chain.AuditPayload{"node_id": id}, "operator")
	if err := s.store().DeleteRouteRule(rid); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	chain.ScheduleAutoApply(id, "route rule delete")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *Server) handleRoutingPresetsJSON(w http.ResponseWriter, r *http.Request) {
	s.renderJSON(w, chain.GetRoutingPresets(r.URL.Query().Get("category")))
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
		b.WriteString(fmt.Sprintf(`<tr><td><span class="badge badge-sm">%s</span></td><td>%s</td><td>%s</td><td>%s</td></tr>`, row.ClientType, row.Name, node, en))
	}
	b.WriteString(`</tbody></table></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Clients"), &simpleHTML{html: b.String()})
}
