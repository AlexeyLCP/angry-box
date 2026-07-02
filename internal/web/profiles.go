package web

// profiles.go — profiles + client assignments + unified clients page
// (extracted from ui.go as part of the M11 split).

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
)

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
			escHTML(p.Name), escHTML(p.ClientType), escHTML(p.ServerRole), auto, len(p.ServerIDs), escHTML(p.ID), escHTML(p.ID), escHTML(p.Name)))
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
		escHTML(p.ID), escHTML(p.Name), escHTML(p.Description), escHTML(p.ClientType), escHTML(p.ServerRole), checked)
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
		b.WriteString(fmt.Sprintf(`<tr><td><span class="badge badge-sm">%s</span></td><td>%s</td><td>%s</td><td>%s</td></tr>`, escHTML(row.ClientType), escHTML(row.Name), escHTML(node), en))
	}
	b.WriteString(`</tbody></table></div></div>`)
	s.renderContent(w, r, i18n.T(r.Context(), "Clients"), &simpleHTML{html: b.String()})
}