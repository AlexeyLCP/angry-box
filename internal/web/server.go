package web

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
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



