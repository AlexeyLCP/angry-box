package web

// takeover.go — VPN takeover (detect existing VPN → convert → cutover →
// rollback) web handlers (extracted from ui.go as part of the M11 split).

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/internal/takeover"
)

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
		b.WriteString(panelSectionHTML(r, id, det.Panel))
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
	b.WriteString(panelSectionHTML(r, id, det.Panel))
	s.renderContent(w, r, i18n.T(r.Context(), "Takeover"), &simpleHTML{html: b.String()})
}

// panelSectionHTML renders the 3x-ui/lucx-ui panel card (import / wipe / leave)
// when a panel install was detected; empty string otherwise.
func panelSectionHTML(r *http.Request, nodeID string, panel *takeover.PanelInfo) string {
	if panel == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="divider-x my-3"></div>`)
	b.WriteString(`<div class="alert alert-warning"><div>`)
	b.WriteString(fmt.Sprintf(`<div><b>`+i18n.T(r.Context(), "Management panel detected: %s")+`</b><br>`+i18n.T(r.Context(), "DB:")+` <code>%s</code> (`+i18n.T(r.Context(), "service active:")+` %v)</div>`,
		escHTML(panel.Kind), escHTML(panel.DBPath), panel.Active))
	b.WriteString(`</div></div>`)
	b.WriteString(`<div class="py-2 text-sm">` + i18n.T(r.Context(), "Panel import reads the panel's SQLite DB and brings over its users, inbounds and routing rules, then stops the panel service. The raw DB is backed up first. Wipe stops the panel without importing.") + `</div>`)
	b.WriteString(fmt.Sprintf(`<div class="flex gap-2">`+
		`<button class="btn btn-primary btn-sm" hx-post="/ui/nodes/%s/panel-import" hx-target="#main-content" hx-swap="outerHTML" hx-confirm="`+i18n.T(r.Context(), "Import this panel's data? The panel service will be stopped.")+`">`+i18n.T(r.Context(), "Import panel")+`</button>`+
		`<button class="btn btn-outline btn-sm" hx-post="/ui/nodes/%s/panel-wipe" hx-target="#main-content" hx-swap="outerHTML" hx-confirm="`+i18n.T(r.Context(), "Stop the panel without importing?")+`">`+i18n.T(r.Context(), "Wipe panel")+`</button>`+
		`</div>`, nodeID, nodeID))
	return b.String()
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
	switch {
	case res != nil && res.Status == "nothing":
		// Empty scaffold / no foreign VPN — not an error, not a success.
		b.WriteString(fmt.Sprintf(`<div class="alert alert-info"><b>`+i18n.T(r.Context(), "Nothing to take over")+`</b><br>%s</div>`, escHTML(res.Message)))
	case err != nil && res != nil && res.Status != "taken":
		b.WriteString(fmt.Sprintf(`<div class="alert alert-error"><b>`+i18n.T(r.Context(), "Takeover %s")+`</b><br>%s</div>`, escHTML(res.Status), escHTML(res.Message)))
	case err != nil:
		b.WriteString(fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Takeover failed: %s")+`</div>`, escHTML(err.Error())))
	default:
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
// registerTakeoverRoutes wires the takeover flow (detect existing VPN →
// convert → cutover). Node-path routes whose handlers live in takeover.go.
// CTO-review §4: split out of server.go Register.
func (s *Server) registerTakeoverRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/nodes/{id}/detect-vpn", s.auth(s.handleDetectVPN))
	mux.HandleFunc("POST /ui/nodes/{id}/takeover", s.auth(s.handleTakeover))
	mux.HandleFunc("POST /ui/nodes/{id}/panel-import", s.auth(s.handlePanelImport))
	mux.HandleFunc("POST /ui/nodes/{id}/panel-wipe", s.auth(s.handlePanelWipe))
}
