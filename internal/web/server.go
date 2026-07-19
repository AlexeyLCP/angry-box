package web

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	"github.com/alexeylcp/angry-box/internal/i18n"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
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
	// connector is the SSH connector shared by all deploy/apply handlers.
	// nil means "use the production connector"; tests inject a fake to avoid
	// real network connections (CTO-review C3).
	connector ports.SSHConnector
	// relocateMu + relocating guard the P2b auto-relocate path: a node whose
	// health transitions to down/unreachable is relocated asynchronously, and
	// the guard prevents a second relocation starting for the same node while
	// one is in flight (RelocateNode does sequential SSH deploys and may take
	// minutes — several metrics ticks can observe the same down state).
	relocateMu sync.Mutex
	relocating map[string]bool
}

// SSHConnector returns the connector used by deploy/apply handlers. Exposed so
// handler tests can build an Applier that talks to a fake SSH client.
func (s *Server) SSHConnector() ports.SSHConnector {
	if s.connector == nil {
		return sshclient.DefaultConnector
	}
	return s.connector
}

// SetSSHConnector overrides the SSH connector (e.g. a connection POOL wired at
// the composition root). Used by serveCmd to share a cross-deploy pool across
// the deploy/apply handlers + auto-apply (CTO-review §8 pool follow-up).
func (s *Server) SetSSHConnector(c ports.SSHConnector) { s.connector = c }

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
		f = factory.New(nil)
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

// StartOffsiteBackupLoop starts the periodic encrypted offsite-backup ticker
// (P2a). No-op if OffsiteBackup is disabled/unconfigured. Reads a fresh
// OffsiteBackupConfig every tick (so operator edits in settings take effect
// without a restart). Interval from cfg.IntervalMin (default 360 min = 6h).
// Shares s.stopCh with the metrics loop for clean shutdown. Errors are logged
// + audited by runOffsiteBackup; a failing tick never stops the loop.
func (s *Server) StartOffsiteBackupLoop() {
	go func() {
		interval := time.Duration(chain.DefaultOffsiteBackupInterval) * time.Minute
		// Do NOT back up immediately on startup — give the metrics loop a head
		// start and let an operator fix a misconfigured target before the first
		// push. First tick after one interval.
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runOffsiteBackup()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// runOffsiteBackup runs one offsite backup cycle if enabled. Reads the fresh
// config, computes the interval (so a changed IntervalMin applies next tick),
// and delegates to chain.PushOffsiteBackup. Logs + audits the outcome.
func (s *Server) runOffsiteBackup() {
	st := s.store()
	settings, err := st.GetSettings()
	if err != nil || settings.OffsiteBackup == nil || !settings.OffsiteBackup.Enabled {
		return
	}
	cfg := settings.OffsiteBackup
	if cfg.Host == "" || cfg.RemotePath == "" || cfg.Passphrase == "" {
		log.Printf("offsite backup: enabled but incomplete config (host/path/passphrase missing), skipping")
		return
	}
	if err := chain.PushOffsiteBackup(context.Background(), st, cfg, s.SSHConnector()); err != nil {
		log.Printf("offsite backup: push to %s failed: %v", cfg.Host, err)
		chain.WriteAudit(st, "backup", "offsite", "", chain.AuditPayload{"target": cfg.Host, "error": err.Error()}, "system")
		return
	}
	chain.WriteAudit(st, "backup", "offsite", "", chain.AuditPayload{"target": cfg.Host, "path": cfg.RemotePath, "success": true}, "system")
}

// collectAllMetrics checks all hosts, classifies each probe via the node
// health state machine, and records the resulting state + metrics. An audit
// event is written ONLY on a state transition (not every tick) so a stable
// node does not spam the audit log; hysteresis (chain.NextState) dampens
// transient blips before they can transition.
//
// Lock discipline: GetMetrics + SaveMetrics each take their own lock; the
// read-modify-write gap between them is safe because collectAllMetrics is the
// only metrics-writer goroutine (the ticker at StartBackgroundMetrics). The
// operator block/unblock handler is a second writer but goes through the same
// per-call-locked SaveMetrics; a lost update here just reclassifies next tick.
// Acceptable for a 15-min loop — if hard safety is ever needed, add a
// store.UpdateMetrics(hostID, func(*NodeMetrics)) locked helper.
func (s *Server) collectAllMetrics() {
	st := s.store()
	hosts, _ := st.ListHosts()
	b := s.factory.Create()
	ctx := context.Background()
	cfg := model.DefaultHysteresis

	for _, h := range hosts {
		start := time.Now()
		status, err := b.GetStatus(ctx, *h)
		latency := time.Since(start).Milliseconds()

		probe := chain.ClassifyProbe(err, status)

		m, _ := st.GetMetrics(h.ID) // durable — survives restart (store.go GetMetrics)
		if m == nil {
			m = &model.NodeMetrics{HostID: h.ID, State: model.NodeStateUnknown}
		}
		m.LastChecked = time.Now()
		m.LatencyMs = latency
		if err == nil && status != nil {
			m.Version = status.Version
			m.OS = status.OS
			m.SingBoxInstalled = status.SingBoxInstalled
			m.AWGModuleInstalled = status.AWGModuleInstalled
		}

		oldState := m.State
		chain.NextState(m, probe, cfg) // mutates m in place (State/Reason/counters)
		m.Online = m.State == model.NodeStateHealthy
		st.SaveMetrics(m)

		// Audit only on a real, incident-class transition. Skip the very first
		// classification of a fresh node (oldState == NodeStateUnknown) so the
		// initial "unknown → healthy" is not logged as an incident — the node
		// was never down, we just observed it for the first time.
		if m.State != oldState && oldState != "" && oldState != model.NodeStateUnknown {
			chain.WriteAudit(st, "health", "node", h.ID,
				chain.AuditPayload{"from": oldState, "to": m.State, "reason": m.StateReason},
				"system")
		}

		// P2b auto-relocate: a node that just entered an incident-class state
		// (down = sing-box dead, unreachable = SSH dead) may be moved onto a
		// warm-pool spare. The decision is pure (chain.AutoRelocateDecision);
		// the relocation itself runs async — it does sequential SSH deploys
		// and must not stall the metrics loop. blocked is deliberately NOT a
		// trigger: it is an operator-set sticky state (a manual relocate is one
		// click away and the operator may be diagnosing).
		if m.State != oldState &&
			(m.State == model.NodeStateDown || m.State == model.NodeStateUnreachable) {
			if spare, reason, ok := chain.AutoRelocateDecision(st, h.ID, time.Now()); ok {
				s.startAutoRelocate(h.ID, spare)
			} else if reason != "disabled-global" && reason != "disabled-node" && reason != "is-spare" {
				// Interesting skips only (cooldown / no-spare / errors) — the
				// double-opt-in disabled states are the normal case and would
				// just spam the audit log on every incident.
				chain.WriteAudit(st, "auto-relocate", "node", h.ID,
					chain.AuditPayload{"state": m.State, "skipped": reason}, "system")
			}
		}

		// v0.7 background AWG maintenance on healthy nodes: fold per-peer
		// kernel counters into per-user totals (CollectAWGTrafficForNode) and
		// re-assert vanished iptables rules (SelfHealAWGRules — fail2ban/docker
		// flushes silently kill egress). One extra SSH dial per healthy node
		// per tick; failures are silent (the health probe already recorded
		// reachability).
		if err == nil && m.State == model.NodeStateHealthy {
			s.maintainAWGNode(st, h.ID)
		}
	}
}

// maintainAWGNode runs the per-tick AWG maintenance (traffic fold + NAT
// self-heal) on one healthy node. Best-effort, silent — a node without AWG is
// the common case and every sub-step skips cleanly.
func (s *Server) maintainAWGNode(st *chain.Store, nodeID string) {
	host, err := st.GetHost(nodeID)
	if err != nil {
		return
	}
	resolved := chain.ResolveHostKey(st, host)
	client, err := s.SSHConnector().Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		return
	}
	defer client.Close()
	ni, _ := st.GetNodeInfo(nodeID)
	useSudo := ni != nil && ni.UseSudo
	ctx := context.Background()
	chain.CollectAWGTrafficForNode(ctx, client, st, nodeID, useSudo)
	if healed, herr := chain.SelfHealAWGRules(ctx, client, "awg0", useSudo); herr == nil && healed {
		chain.WriteAudit(st, "self-heal", "node", nodeID,
			chain.AuditPayload{"what": "re-asserted awg0 PostUp iptables rules (FORWARD/rp_filter/ip_forward)"}, "system")
	}
}

// startAutoRelocate launches an async relocation of nodeID onto the spare,
// guarded so only one relocation per node runs at a time (P2b).
func (s *Server) startAutoRelocate(nodeID string, spare *model.NodeInfo) {
	s.relocateMu.Lock()
	if s.relocating == nil {
		s.relocating = map[string]bool{}
	}
	if s.relocating[nodeID] {
		s.relocateMu.Unlock()
		return
	}
	s.relocating[nodeID] = true
	s.relocateMu.Unlock()

	st := s.store()
	chain.WriteAudit(st, "auto-relocate", "node", nodeID,
		chain.AuditPayload{"spare": spare.ID, "spare_addr": spare.Addr, "phase": "start"}, "system")

	go func() {
		defer func() {
			s.relocateMu.Lock()
			delete(s.relocating, nodeID)
			s.relocateMu.Unlock()
		}()
		ctx := context.Background()
		applier := chain.NewApplier(s.factory, s.SSHConnector())
		report, err := chain.RelocateNode(ctx, st, applier, nodeID, spare.Addr, spare.User, spare.KeyPath, "")
		if err != nil {
			chain.WriteAudit(st, "auto-relocate", "node", nodeID,
				chain.AuditPayload{"spare": spare.ID, "phase": "failed", "error": err.Error()}, "system")
			return
		}
		// Relocation succeeded — the spare's address now belongs to the node;
		// remove the spare's own identity and stamp the cooldown.
		if cerr := chain.ConsumeSpare(st, spare.ID); cerr != nil {
			chain.WriteAudit(st, "auto-relocate", "node", nodeID,
				chain.AuditPayload{"spare": spare.ID, "phase": "consume-warning", "error": cerr.Error()}, "system")
		}
		if ni, nerr := st.GetNodeInfo(nodeID); nerr == nil {
			ni.LastAutoRelocateAt = time.Now()
			_ = st.SaveNodeInfo(ni)
		}
		chain.WriteAudit(st, "auto-relocate", "node", nodeID,
			chain.AuditPayload{"spare": spare.ID, "phase": "done", "old_addr": report.OldAddr, "new_addr": report.NewAddr}, "system")
	}()
}

// handleNDMHook receives interface events from Keenetic NDMS hook scripts
// (/opt/etc/ndm/{iflayerchanged,ifcreated,ifdestroyed,ifipchanged}.d/
// 50-angry-box.sh). Loopback-only: the hook runs on the router itself, so any
// non-loopback source is a forgery (the panel may bind a LAN address for the
// UI — the hook endpoint must stay local). v1 behavior: validate + log;
// future use is triggering a re-probe when a WAN event suggests an outage.
func (s *Server) handleNDMHook(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || (host != "127.0.0.1" && host != "::1") {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	typ := r.FormValue("type")
	switch typ {
	case "iflayerchanged", "ifcreated", "ifdestroyed", "ifipchanged":
	default:
		http.Error(w, "unknown hook type", http.StatusBadRequest)
		return
	}
	log.Printf("ndm hook: %s id=%s system_name=%s layer=%s level=%s address=%s up=%s connected=%s",
		typ, r.FormValue("id"), r.FormValue("system_name"), r.FormValue("layer"),
		r.FormValue("level"), r.FormValue("address"), r.FormValue("up"), r.FormValue("connected"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {	return BasicAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Recover from any panic in the handler chain so a single malformed
		// request/deploy (e.g. a generator panic in cryptogen/roles) cannot
		// crash the whole orchestrator process. The error is logged with a
		// stack trace and a 500 is returned to the client (best-effort: if the
		// handler already started writing the response body, the 500 status is
		// not sent and the client sees a truncated response — but the process
		// survives, which is the point). Without this, Go's net/http aborts
		// only the serving goroutine, but the panic propagates and can take
		// down the process under some logger configurations.
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic in handler %s: %v\n%s", r.URL.Path, rec, debug.Stack())
				http.Error(w, i18n.T(r.Context(), "internal error"), http.StatusInternalServerError)
			}
		}()
		settings, _ := s.store().GetSettings()
		lang := settings.Language
		if lang == "" {
			lang = "en"
		}
		// UI pages must not be cached by the browser — the language can change
		// at runtime (settings → language → HX-Refresh reloads the page), and a
		// cached page would show the old language after the reload. no-store
		// (stronger than no-cache) forbids the browser from keeping a copy.
		w.Header().Set("Cache-Control", "no-store")
		ctx := context.WithValue(r.Context(), i18n.LangKey, lang)
		h(w, r.WithContext(ctx))
	}), s.cfg)
}

func (s *Server) Register(mux *http.ServeMux) {
	// Static files (CSS, JS, images) — from disk in dev, from embed in prod.
	staticFS, err := s.staticFS()
	if err != nil {
		log.Printf("WARNING: static files unavailable: %v", err)
	} else {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	// Root redirect → dashboard. (The only non-resource route kept here.)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
	})

	// Public subscription endpoint — OUTSIDE s.auth (client apps fetch it
	// without Basic-Auth). GET passes CSRF automatically (safe-method bypass).
	s.registerSubscriptionRoute(mux)

	// NDMS hook receiver (Keenetic router installs): /opt/etc/ndm/*.d scripts
	// forward interface events to the panel's loopback API. NOT under s.auth —
	// guarded by a strict loopback check instead (the hook runs on the router
	// itself; anything remote is rejected).
	mux.HandleFunc("POST /api/hooks/ndm", s.handleNDMHook)

	// Resource-scoped route registrations (CTO-review §4: split out of the old
	// ~60-route monolith). Each register*Routes method lives next to its
	// handlers in the resource file.
	s.registerDashboardRoutes(mux)
	s.registerNodeRoutes(mux)
	s.registerTakeoverRoutes(mux)
	s.registerChainRoutes(mux)
	s.registerSpiderRoutes(mux)
	s.registerUserRoutes(mux)
	s.registerQRRoutes(mux)
	s.registerSettingsRoutes(mux)
	s.registerMiscRoutes(mux)
	s.registerClientRoutes(mux)
	s.registerInboundRoutes(mux)
	s.registerPresetRoutes(mux)
	s.registerBackupRoutes(mux)
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
		path := sshDir + "/" + name
		entry := model.SSHKeyEntry{
			ID:      "system-" + name,
			Name:    name,
			KeyPath: path,
			Source:  model.SourceSystem,
		}
		// Populate KeyData + Fingerprint so Test/Export are self-contained
		// (no re-read of disk needed). Error-tolerant: a file that can't be
		// parsed (e.g. encrypted key, binary) just gets an empty fingerprint.
		if privPEM, err := os.ReadFile(path); err == nil {
			entry.KeyData = string(privPEM)
			if fp, err := chain.DeriveKeyFingerprint(string(privPEM)); err == nil {
				entry.Fingerprint = fp
			}
		}
		keys = append(keys, entry)
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



