package web

// settings.go — panel settings + SSH key management handlers (extracted from
// ui.go as part of the M11 split).

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
	"golang.org/x/crypto/bcrypt"
)

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
			// Toggling auth_enabled is a privileged, security-sensitive change.
			// Require the current admin password so that a forged POST (e.g. a
			// CSRF attempt that slipped past the Origin check, or a hijacked
			// session) cannot disable authentication for the whole panel and
			// expose the fleet. When auth is currently enabled we verify the
			// old password regardless of the toggle direction; when it is
			// currently disabled there is no password to check yet.
			if s.cfg.AuthEnabled {
				oldPassword := strings.TrimSpace(r.FormValue("auth_old_password"))
				if err := bcrypt.CompareHashAndPassword([]byte(s.cfg.AuthPasswordHash), []byte(oldPassword)); err != nil {
					s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Failed to change auth settings: current password is incorrect.") + `</span></div>`})
					return
				}
			}
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
			if err := s.cfg.SavePath(); err != nil {
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
	// Default REALITY/TUIC SNI (global fallback when no preset specifies one).
	settings.DefaultRealitySNI = strings.TrimSpace(r.FormValue("reality_sni"))
	// Apply immediately so subsequent renders use the new default without a restart.
	chain.SetDefaultSNI(settings.DefaultRealitySNI)

	if dp := strings.TrimSpace(r.FormValue("default_protocol")); dp != "" {
		if err := chain.ValidateChainUserProtocol(model.UserProtocol(dp)); err != nil && settings.DefaultProtocol != dp {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		settings.DefaultProtocol = dp
	}

	// SSH keys are managed through their own endpoints (/ui/settings/ssh-keys,
	// /ui/settings/ssh-keys/{id}, import-system, import) which carry the PEM
	// key_data — NOT through the main settings form. The main form used to emit
	// ssh_key_name/ssh_key_path pairs (the pre-v0.2.5 KeyPath-based schema), and
	// this block rebuilt settings.SSHKeys from those fields. After the redesign
	// the main form no longer carries them, so this would clobber the
	// PEM-stored keys to an empty slice on every Save Settings (data-loss:
	// saving the language wiped all imported keys). Removed.

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
	// Compute fingerprint (last 8 of SHA256 pubkey) so the dropdown and
	// Settings render without re-parsing PEM per render.
	fp, err := chain.DeriveKeyFingerprint(keyData)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid key format. Expected a private key (BEGIN ... PRIVATE KEY).") + `</span></div>`})
		return
	}
	st := s.store()
	settings, _ := st.GetSettings()
	id := fmt.Sprintf("key-%d", len(settings.SSHKeys)+1)
	settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
		ID:          id,
		Name:        name,
		KeyData:     keyData,
		Source:      model.SourceStored,
		Fingerprint: fp,
	})
	st.SaveSettings(settings)
	// Return updated key list
	sysKeys := detectSystemKeys()
	hosts, _ := st.ListHosts()
	s.render(w, r, templates.SSHKeyList(settings, sysKeys, hosts))
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

// looksLikePrivateKey checks that data has a matching PEM BEGIN/END PRIVATE KEY
// header/footer, so the SSH-key form rejects non-key pastes early.
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

// ─── Default key, Test, Import-from-~/.ssh, Export/Import registry ─────────────

// handleSetDefaultKey persists the panel default SSH key ID. An empty ssh_key_id
// clears the default. A non-empty value must resolve in the registry.
func (s *Server) handleSetDefaultKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	keyID := strings.TrimSpace(r.FormValue("ssh_key_id"))
	st := s.store()
	settings, _ := st.GetSettings()
	if keyID != "" {
		if _, ok := st.ResolveKey(keyID); !ok {
			http.Error(w, i18n.T(r.Context(), "Selected key not found in registry"), http.StatusBadRequest)
			return
		}
	}
	settings.DefaultSSHKeyID = keyID
	st.SaveSettings(settings)
	sysKeys := detectSystemKeys()
	hosts, _ := st.ListHosts()
	s.render(w, r, templates.SSHKeyList(settings, sysKeys, hosts))
}

// handleTestKey runs a one-off GetStatus against a target node using the key
// identified by the path value {id}. The key may be a stored entry or a
// system-detected entry; the host's KeyPath is set to the entry ID and the
// SSH connector resolves it via the registry (ResolveKey) at connect time.
func (s *Server) handleTestKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	targetID := strings.TrimSpace(r.FormValue("target_node"))
	st := s.store()
	// Resolve the key entry (stored first, then system).
	settings, _ := st.GetSettings()
	var entry *model.SSHKeyEntry
	for i := range settings.SSHKeys {
		if settings.SSHKeys[i].ID == id {
			entry = &settings.SSHKeys[i]
			break
		}
	}
	if entry == nil {
		for _, k := range detectSystemKeys() {
			if k.ID == id {
				k := k
				entry = &k
				break
			}
		}
	}
	if entry == nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	// Resolve target node.
	target, err := st.GetHost(targetID)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	// Build a one-off host (NOT saved) using this key.
	host := *target
	host.KeyPath = entry.ID
	b := s.factory.Create()
	status, err := b.GetStatus(r.Context(), host)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + escHTML(err.Error()) + `</span></div>`})
		return
	}
	s.render(w, r, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>`+i18n.T(r.Context(), "Key works. sing-box: %s")+" "+i18n.T(r.Context(), "OS: %s")+`</span></div>`,
		escHTML(status.Version), escHTML(status.OS))})
}

// handleImportSystemKey copies a system-detected key (from ~/.ssh/) into a new
// stored registry entry so it can be referenced by ID and exported with the
// rest of the registry.
func (s *Server) handleImportSystemKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	keyID := strings.TrimSpace(r.FormValue("system_key_id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if keyID == "" || name == "" {
		http.Error(w, i18n.T(r.Context(), "Name and key are required"), http.StatusBadRequest)
		return
	}
	// Find the system key (detectSystemKeys loads KeyData + Fingerprint).
	var sysEntry *model.SSHKeyEntry
	for _, k := range detectSystemKeys() {
		if k.ID == keyID {
			k := k
			sysEntry = &k
			break
		}
	}
	if sysEntry == nil || sysEntry.KeyData == "" {
		http.Error(w, i18n.T(r.Context(), "System key not readable"), http.StatusBadRequest)
		return
	}
	fp, _ := chain.DeriveKeyFingerprint(sysEntry.KeyData)
	st := s.store()
	settings, _ := st.GetSettings()
	newID := fmt.Sprintf("key-imp-%d", time.Now().Unix())
	settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
		ID:          newID,
		Name:        name,
		KeyData:     sysEntry.KeyData,
		Source:      model.SourceStored,
		Fingerprint: fp,
	})
	st.SaveSettings(settings)
	hosts, _ := st.ListHosts()
	s.render(w, r, templates.SSHKeyList(settings, detectSystemKeys(), hosts))
}

// handleExportKeys streams the full registry (stored + system keys) as a JSON
// attachment so the user can back up or migrate keys between panels.
func (s *Server) handleExportKeys(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	settings, _ := st.GetSettings()
	all := append([]model.SSHKeyEntry{}, settings.SSHKeys...)
	all = append(all, detectSystemKeys()...)
	w.Header().Set("Content-Disposition", `attachment; filename="angry-box-ssh-keys.json"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	_ = json.NewEncoder(w).Encode(all)
}

// handleImportKeys merges a JSON array of SSHKeyEntry (as produced by
// handleExportKeys) into the registry. Existing IDs are skipped unless
// force=on.
func (s *Server) handleImportKeys(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	jsonData := strings.TrimSpace(r.FormValue("keys_json"))
	force := r.FormValue("force") == "on"
	var incoming []model.SSHKeyEntry
	if err := json.Unmarshal([]byte(jsonData), &incoming); err != nil {
		http.Error(w, i18n.T(r.Context(), "Invalid JSON")+": "+err.Error(), http.StatusBadRequest)
		return
	}
	st := s.store()
	settings, _ := st.GetSettings()
	existing := map[string]bool{}
	for _, k := range settings.SSHKeys {
		existing[k.ID] = true
	}
	added := 0
	for _, k := range incoming {
		if existing[k.ID] && !force {
			continue
		}
		settings.SSHKeys = append(settings.SSHKeys, k)
		added++
	}
	st.SaveSettings(settings)
	s.render(w, r, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>`+i18n.T(r.Context(), "Imported %d keys.")+`</span></div>`, added)})
}
// registerSettingsRoutes wires the settings page + the SSH-key management
// sub-routes (add/delete/set-default/test/import-system/export/import).
// CTO-review §4: split out of server.go Register.
func (s *Server) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/settings", s.auth(s.handleSettings))
	mux.HandleFunc("POST /ui/settings", s.auth(s.handleSaveSettings))
	mux.HandleFunc("POST /ui/settings/ssh-keys", s.auth(s.handleAddSSHKey))
	mux.HandleFunc("DELETE /ui/settings/ssh-keys/{id}", s.auth(s.handleDeleteSSHKey))
	mux.HandleFunc("POST /ui/settings/default-key", s.auth(s.handleSetDefaultKey))
	mux.HandleFunc("POST /ui/settings/ssh-keys/{id}/test", s.auth(s.handleTestKey))
	mux.HandleFunc("POST /ui/settings/ssh-keys/import-system", s.auth(s.handleImportSystemKey))
	mux.HandleFunc("GET /ui/settings/ssh-keys/export", s.auth(s.handleExportKeys))
	mux.HandleFunc("POST /ui/settings/ssh-keys/import", s.auth(s.handleImportKeys))
	mux.HandleFunc("POST /ui/settings/auto-relocate", s.auth(s.handleSaveAutoRelocate))
}

// handleSaveAutoRelocate saves the global P2b auto-relocate master switch +
// cooldown (own HTMX endpoint, mirroring handleSaveOffsite, so a partial form
// never blanks unrelated settings). The per-node opt-in + spare flags live on
// the node edit form.
func (s *Server) handleSaveAutoRelocate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	settings, err := st.GetSettings()
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "save: %v"), http.StatusInternalServerError)
		return
	}
	ar := settings.AutoRelocate
	if ar == nil {
		ar = &model.AutoRelocateConfig{}
	}
	ar.Enabled = r.FormValue("auto_relocate_enabled") == "on"
	if cv := strings.TrimSpace(r.FormValue("auto_relocate_cooldown")); cv != "" {
		ar.CooldownHours, _ = strconv.Atoi(cv)
	}
	settings.AutoRelocate = ar
	if err := st.SaveSettings(settings); err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "save: %v") + `: ` + err.Error() + `</span></div>`})
		return
	}
	s.render(w, r, &simpleHTML{html: `<div class="alert alert-success"><span>` + i18n.T(r.Context(), "Auto-relocate settings saved") + `</span></div>`})
}
