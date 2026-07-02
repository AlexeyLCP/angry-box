package web

// settings.go — panel settings + SSH key management handlers (extracted from
// ui.go as part of the M11 split).

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/config"
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