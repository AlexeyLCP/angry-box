package web

// mtproxy.go — MTProxy-user CRUD handlers (extracted pattern mirrors users.go).
// MTProxy users (model.MtproxyUser) back the sing-box-extended mtproxy inbound
// (FakeTLS, extended "ee"+hex secret). The inbound itself is emitted at the node
// level by buildMergedNodeConfig from ListMtproxyUsersForNode; these handlers
// only manage the user records and schedule a re-deploy on the affected node.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleMtproxyUsers(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListMtproxyUsers()
	nodes, _ := st.ListNodeInfos()
	s.renderContent(w, r, i18n.T(r.Context(), "MTProxy Users"), templates.MtproxyUsers(users, nodes))
}

func (s *Server) handleNewMtproxyUserForm(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.store().ListNodeInfos()
	s.render(w, r, templates.MtproxyUserForm(nil, nodes))
}

func (s *Server) handleCreateMtproxyUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	nodeID := strings.TrimSpace(r.FormValue("node_id"))
	name := strings.TrimSpace(r.FormValue("name"))
	secretHex := strings.TrimSpace(r.FormValue("secret_hex"))
	fakeTLS := strings.TrimSpace(r.FormValue("fake_tls_domain"))

	if nodeID == "" {
		http.Error(w, i18n.T(r.Context(), "node_id is required"), http.StatusBadRequest)
		return
	}
	if name == "" {
		http.Error(w, i18n.T(r.Context(), "id and name are required"), http.StatusBadRequest)
		return
	}
	// Auto-generate a 32-hex secret when the operator left it blank.
	if secretHex == "" {
		secretHex = chain.GenerateMTProxySecret()
	}
	if fakeTLS == "" {
		fakeTLS = "disk.yandex.ru"
	}

	u := &model.MtproxyUser{
		Name:           name,
		NodeID:         nodeID,
		SecretHex:      secretHex,
		FakeTLSDomain:  fakeTLS,
		Enabled:        true,
		CreatedAt:      time.Now(),
	}
	st := s.store()
	if err := st.SaveMtproxyUser(u); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "create", "mtproxy_user", u.ID, chain.AuditPayload{"name": u.Name, "node": u.NodeID}, "operator")
	chain.ScheduleAutoApply(u.NodeID, "mtproxy user create")
	s.render(w, r, templates.MtproxyUserRow(u))
}

func (s *Server) handleEditMtproxyUserForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u := findMtproxyUser(s.store(), id)
	if u == nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	nodes, _ := s.store().ListNodeInfos()
	s.render(w, r, templates.MtproxyUserForm(u, nodes))
}

func (s *Server) handleUpdateMtproxyUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	u := findMtproxyUser(st, id)
	if u == nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	u.Name = strings.TrimSpace(r.FormValue("name"))
	u.NodeID = strings.TrimSpace(r.FormValue("node_id"))
	if secretHex := strings.TrimSpace(r.FormValue("secret_hex")); secretHex != "" {
		u.SecretHex = secretHex
	}
	if fakeTLS := strings.TrimSpace(r.FormValue("fake_tls_domain")); fakeTLS != "" {
		u.FakeTLSDomain = fakeTLS
	}
	u.Enabled = r.FormValue("enabled") == "on"

	if u.NodeID == "" {
		http.Error(w, i18n.T(r.Context(), "node_id is required"), http.StatusBadRequest)
		return
	}
	st.SaveMtproxyUser(u)
	chain.WriteAudit(st, "update", "mtproxy_user", u.ID, chain.AuditPayload{"name": u.Name, "node": u.NodeID}, "operator")
	chain.ScheduleAutoApply(u.NodeID, "mtproxy user update")
	if isHTMXRequest(r) {
		s.render(w, r, templates.MtproxyUserRow(u))
	} else {
		http.Redirect(w, r, "/ui/mtproxy", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteMtproxyUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if u := findMtproxyUser(st, id); u != nil {
		chain.WriteAudit(st, "delete", "mtproxy_user", id, chain.AuditPayload{"name": u.Name, "node": u.NodeID}, "operator")
		chain.ScheduleAutoApply(u.NodeID, "mtproxy user delete")
	}
	if err := st.DeleteMtproxyUser(id); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "delete: %v"), err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// handleGenerateMtproxySecret returns a freshly generated 32-hex MTProxy secret
// wrapped in an <input> element, so the form's "Generate Secret" button
// (hx-target="[name='secret_hex']" hx-swap="outerHTML") replaces the empty
// secret field with one pre-filled with the new secret. The button is a
// sibling of the input inside the .join div, so it survives the swap.
func (s *Server) handleGenerateMtproxySecret(w http.ResponseWriter, r *http.Request) {
	secret := chain.GenerateMTProxySecret()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// hex chars are [0-9a-f] — no HTML-escaping needed, but the value attribute
	// is quoted so it's safe regardless.
	fmt.Fprintf(w, `<input type="text" name="secret_hex" class="input input-bordered join-item font-mono" value="%s" maxlength="32" />`, secret)
}

// findMtproxyUser loads a single MTProxy user by ID. The store has no
// GetMtproxyUser, so we filter ListMtproxyUsers.
func findMtproxyUser(st *chain.Store, id string) *model.MtproxyUser {
	users, _ := st.ListMtproxyUsers()
	for _, u := range users {
		if u.ID == id {
			return u
		}
	}
	return nil
}
