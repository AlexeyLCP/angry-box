package web

// misc.go — status, audit log, deploy-status, and the simpleHTML/jsonMarshal
// helpers shared across the web handlers (extracted from ui.go as part of the
// M11 split).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// ─── Status / host status ─────────────────────────────────────────────────────

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

	// Reuse the same state-machine path as the metrics loop so a manual "Check"
	// produces a State consistent with the background ticker (and does not wipe
	// hysteresis counters / block state). Read existing metrics, classify, save.
	probe := chain.ClassifyProbe(err, status)
	m, _ := st.GetMetrics(id)
	if m == nil {
		m = &model.NodeMetrics{HostID: id, State: model.NodeStateUnknown}
	}
	m.LastChecked = time.Now()
	if err == nil && status != nil {
		m.Version = status.Version
		m.OS = status.OS
		m.SingBoxInstalled = status.SingBoxInstalled
		m.AWGModuleInstalled = status.AWGModuleInstalled
	}
	chain.NextState(m, probe, model.DefaultHysteresis)
	m.Online = m.State == model.NodeStateHealthy
	st.SaveMetrics(m)

	if err != nil {
		s.render(w, r, &simpleHTML{html: `<span class="badge badge-error badge-sm">` + i18n.T(r.Context(), "Error") + `</span>`})
		return
	}
	s.render(w, r, templates.HostStatus(status))
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
	// Fetch the node's MTProxy users so the preview matches what the deploy path
	// (buildMergedNodeConfig via ApplyMergedNode) actually emits. Without this,
	// a node with enabled MTProxy users would preview WITHOUT the mtproxy inbound
	// → CurrentHash != LastDeployedHash → the UI perpetually shows a pending-
	// changes indicator even right after a successful deploy.
	mtproxyUsers := st.ListMTProxyUsersForNode(info.ID)
	cfg, _, err := chain.RenderMergedNodeConfig(info, nodeChains, mtproxyUsers)
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
// registerMiscRoutes wires the cross-resource status/audit/deploy-status
// endpoints + the per-host liveness probe (host-status). CTO-review §4: split
// out of server.go Register.
func (s *Server) registerMiscRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/hosts/{id}/status", s.auth(s.handleHostStatus))
	mux.HandleFunc("GET /ui/status", s.auth(s.handleStatus))
	mux.HandleFunc("GET /ui/audit", s.auth(s.handleAudit))
	mux.HandleFunc("GET /ui/deploy-status", s.auth(s.handleDeployStatus))
}
