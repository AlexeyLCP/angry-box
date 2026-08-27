package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/internal/takeover"
)

// panelDownload is implemented by the concrete SSH client. The ports.SSHClient
// interface stays minimal; the panel import type-asserts to fetch the DB.
type panelDownload interface {
	DownloadFile(ctx context.Context, remotePath string) ([]byte, error)
}

// panelCommand is implemented by the concrete SSH client for the wipe step
// (stop/disable the panel service). RunWithOutput is already on ports.SSHClient.

// handlePanelImport pulls the 3x-ui/lucx-ui SQLite DB over SSH, parses it, and
// converts users/inbounds/routing into angry-box store entities. It then stops
// the panel service (the panel is being taken over — its xray is replaced by
// angry-box-managed sing-box on a subsequent Apply). The raw DB is backed up to
// the store dir before anything is mutated (rule #7 rollback discipline).
func (s *Server) handlePanelImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "node not found"), http.StatusNotFound)
		return
	}

	resolved := chain.ResolveHostKey(st, &info.Host)
	client, err := s.SSHConnector().Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error">` + escHTML(i18n.T(r.Context(), "SSH connect failed: ")+err.Error()) + `</div>`})
		return
	}
	defer client.Close()

	dl, ok := client.(panelDownload)
	if !ok {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error">` + i18n.T(r.Context(), "Panel import needs the built-in SSH client (download support).") + `</div>`})
		return
	}

	raw, err := dl.DownloadFile(r.Context(), takeover.PanelDBPathDefault)
	if err != nil {
		chain.WriteAudit(st, "panel-import", "node", id, chain.AuditPayload{"phase": "download", "error": err.Error()}, "operator")
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error">` + escHTML(i18n.T(r.Context(), "Panel DB download failed: ")+err.Error()) + `</div>`})
		return
	}
	// Backup the raw DB next to the store before mutating anything.
	if err := s.backupPanelDB(id, raw); err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-warning">` + escHTML(i18n.T(r.Context(), "Panel DB backed up failed (continuing): ")+err.Error()) + `</div>`})
	}

	db, err := takeover.ParsePanelDB(raw)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error">` + escHTML(i18n.T(r.Context(), "Panel DB parse failed: ")+err.Error()) + `</div>`})
		return
	}
	imp := takeover.ConvertPanelImport(id, db)

	// Commit: inbounds onto the node, users + route rules into the store.
	addedInbounds := 0
	for _, ib := range imp.NodeInbounds {
		if panelInboundExists(info, ib) {
			continue
		}
		info.Inbounds = append(info.Inbounds, ib)
		addedInbounds++
	}
	if err := st.SaveNodeInfo(info); err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error">` + escHTML(i18n.T(r.Context(), "save node: ")+err.Error()) + `</div>`})
		return
	}
	savedUsers := 0
	for _, u := range imp.Users {
		if u.ID == "" {
			u.ID = panelUserID(u.Name)
		}
		chain.EnsureUserCreds(u)
		if u.SubscriptionToken == "" {
			if tok, terr := chain.GenerateSubscriptionToken(); terr == nil {
				u.SubscriptionToken = tok
			}
		}
		u.Status = u.ComputeStatus()
		if err := st.SaveUser(u); err == nil {
			savedUsers++
		}
	}
	savedRules := 0
	for _, rr := range imp.RouteRules {
		if err := st.SaveRouteRule(rr); err == nil {
			savedRules++
		}
	}

	// Stop the panel service (taken over). Best-effort but reported.
	stopNote := stopPanelService(r.Context(), client, info.UseSudo)

	chain.WriteAudit(st, "panel-import", "node", id, chain.AuditPayload{
		"inbounds": addedInbounds, "users": savedUsers, "route_rules": savedRules,
	}, "operator")

	var b strings.Builder
	b.WriteString(`<div class="alert alert-success"><b>` + i18n.T(r.Context(), "Panel import done") + `</b><br>`)
	b.WriteString(fmt.Sprintf(i18n.T(r.Context(), "Imported: %d inbound(s), %d user(s), %d routing rule(s)."), addedInbounds, savedUsers, savedRules))
	b.WriteString(`</div>`)
	if stopNote != "" {
		b.WriteString(`<div class="alert alert-info mt-2 text-sm">` + escHTML(stopNote) + `</div>`)
	}
	if len(imp.Report) > 0 {
		b.WriteString(`<div class="card bg-base-100 shadow mt-2"><div class="card-body text-sm"><b>` + i18n.T(r.Context(), "Import details") + `</b><ul class="list-disc ml-5 mt-1">`)
		for _, line := range imp.Report {
			b.WriteString(`<li>` + escHTML(line) + `</li>`)
		}
		b.WriteString(`</ul></div></div>`)
	}
	b.WriteString(`<div class="mt-2 text-sm" style="color: var(--mut)">` + i18n.T(r.Context(), "Next: review the imported inbounds and Apply the node to deploy sing-box.") + `</div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Panel import result"), &simpleHTML{html: b.String()})
}

// handlePanelWipe stops + disables the panel service and backs up its DB, but
// imports nothing. For a node whose panel should just be retired.
func (s *Server) handlePanelWipe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "node not found"), http.StatusNotFound)
		return
	}
	resolved := chain.ResolveHostKey(st, &info.Host)
	client, err := s.SSHConnector().Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error">` + escHTML(i18n.T(r.Context(), "SSH connect failed: ")+err.Error()) + `</div>`})
		return
	}
	defer client.Close()

	// Back up the DB first (so a later change of heart can re-import).
	if dl, ok := client.(panelDownload); ok {
		if raw, derr := dl.DownloadFile(r.Context(), takeover.PanelDBPathDefault); derr == nil {
			_ = s.backupPanelDB(id, raw)
		}
	}
	note := stopPanelService(r.Context(), client, info.UseSudo)
	chain.WriteAudit(st, "panel-wipe", "node", id, chain.AuditPayload{}, "operator")
	s.renderContent(w, r, i18n.T(r.Context(), "Panel wipe"), &simpleHTML{html: `<div class="alert alert-info">` + escHTML(note) + `</div>`})
}

// stopPanelService stops + disables the panel unit (the DB + files stay on
// disk — deletion is never an import side effect) and reports the outcome.
func stopPanelService(ctx context.Context, client ports.SSHClient, useSudo bool) string {
	cmd := fmt.Sprintf("systemctl stop %s 2>/dev/null; systemctl disable %s 2>/dev/null; echo DONE", takeover.PanelUnit, takeover.PanelUnit)
	if useSudo {
		cmd = fmt.Sprintf("sudo bash -c '%s'", cmd)
	}
	if _, stderr, exit, err := client.RunWithOutput(ctx, cmd, 60*time.Second); err != nil {
		return fmt.Sprintf("panel service stop failed: %s (exit %d) %s", err, exit, stderr)
	}
	return fmt.Sprintf("panel service %s stopped + disabled (files kept on disk)", takeover.PanelUnit)
}

// backupPanelDB stores the raw panel DB next to the orchestrator store so an
// import can be reviewed/replayed (the same backup-always rule as wipe).
func (s *Server) backupPanelDB(nodeID string, raw []byte) error {
	dir := filepath.Join(filepath.Dir(s.storePath), "panel-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s.db", nodeID, time.Now().Format("20060102-150405"))
	return os.WriteFile(filepath.Join(dir, name), raw, 0o600)
}

// panelInboundExists reports whether the node already has an inbound with the
// same tag (idempotent re-import).
func panelInboundExists(info *model.NodeInfo, ib model.NodeInbound) bool {
	for _, ex := range info.Inbounds {
		if ex.Tag != "" && ex.Tag == ib.Tag {
			return true
		}
	}
	return false
}

// panelUserID builds a stable, slug-safe user ID from the imported name.
func panelUserID(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "panel-user"
	}
	return "pu-" + id
}
