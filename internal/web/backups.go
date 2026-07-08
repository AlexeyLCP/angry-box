package web

// backups.go — HTTP handlers for full-panel + per-node backup export/import.
// Routes:
//   GET  /ui/backup/store          — download the whole panel as a JSON backup
//   GET  /ui/backup/nodes/{id}     — download one node's portable identity
//   POST /ui/backup/import         — import a store or node backup (auto-detect)
//
// The handlers live here (resource-scoped, registered via registerBackupRoutes
// from Server.Register) and call the chain.Store backup helpers. Exports use
// Content-Disposition: attachment (the same pattern as handleExportKeys), so
// the browser downloads the file; imports accept the JSON in a textarea and
// auto-detect the backup kind via the envelope so a single endpoint handles
// both store and node backups.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/i18n"
)

// registerBackupRoutes wires the backup export/import endpoints. All under
// s.auth (CSRF middleware applies to the POST).
func (s *Server) registerBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/backup/store", s.auth(s.handleExportStoreBackup))
	mux.HandleFunc("GET /ui/backup/nodes/{id}", s.auth(s.handleExportNodeBackup))
	mux.HandleFunc("POST /ui/backup/import", s.auth(s.handleImportBackup))
}

// handleExportStoreBackup streams the entire panel as a plaintext JSON backup
// (Content-Disposition: attachment so the browser downloads it). The filename
// carries a timestamp so multiple exports do not clobber each other.
func (s *Server) handleExportStoreBackup(w http.ResponseWriter, r *http.Request) {
	data, err := s.store().ExportStore()
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "backup export failed")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	fname := fmt.Sprintf("angry-box-store-%s.json", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

// handleExportNodeBackup streams one node's portable identity as a JSON
// download. 404 if the node ID is not in the store.
func (s *Server) handleExportNodeBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, i18n.T(r.Context(), "missing name"), http.StatusBadRequest)
		return
	}
	b, err := s.store().ExportNode(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found")+": "+err.Error(), http.StatusNotFound)
		return
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "backup export failed")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="angry-box-node-`+id+`.json"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

// handleImportBackup accepts a store OR node backup in a single form field
// (backup_json) and auto-detects which kind via the envelope. force=on allows
// overwriting a non-empty store (store backup) or rerouting a live node (node
// backup). Renders an alert — success or the per-helper error (e.g. skipped
// missing chains is reported but not fatal for the node case).
func (s *Server) handleImportBackup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	payload := strings.TrimSpace(r.FormValue("backup_json"))
	if payload == "" {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "backup json is required") + `</span></div>`})
		return
	}
	force := r.FormValue("force") == "on"
	data := []byte(payload)

	format, err := chain.DetectBackupFormat(data)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid backup JSON") + `: ` + err.Error() + `</span></div>`})
		return
	}

	switch format {
	case chain.BackupFormatStore:
		if err := s.store().ImportStore(data, force); err != nil {
			s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "import failed") + `: ` + err.Error() + `</span></div>`})
			return
		}
		// A store import replaces the whole panel; reload the page so the UI
		// reflects the new state (hosts/chains/settings).
		w.Header().Set("HX-Refresh", "true")
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-success"><span>` + i18n.T(r.Context(), "Store imported") + `</span></div>`})
	case chain.BackupFormatNode:
		var b chain.NodeBackup
		if err := json.Unmarshal(data, &b); err != nil {
			s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid backup JSON") + `: ` + err.Error() + `</span></div>`})
			return
		}
		if err := s.store().ImportNode(&b, force); err != nil {
			// ImportNode returns a wrapped error when chains were skipped (the
			// node itself was restored). Surface it as a warning, not a failure.
			msg := err.Error()
			alert := "alert-error"
			text := i18n.T(r.Context(), "import failed")
			if strings.Contains(msg, "skipped missing chains") {
				alert = "alert-warning"
				text = i18n.T(r.Context(), "Node imported")
			}
			s.render(w, r, &simpleHTML{html: `<div class="alert ` + alert + `"><span>` + text + `: ` + msg + `</span></div>`})
			return
		}
		w.Header().Set("HX-Refresh", "true")
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-success"><span>` + i18n.T(r.Context(), "Node imported") + `</span></div>`})
	default:
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid backup JSON") + `</span></div>`})
	}
}