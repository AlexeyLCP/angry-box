package web

// utilities.go — the per-node "spinal cord" utility bundle UI (caddy / acme /
// fakesite / sub). The orchestrator is the only writer: install, cert issue,
// Caddyfile push and uninstall all run over SSH from here; the node keeps no
// local config state. See internal/chain/utilities*.go for the mechanics.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// registerUtilityRoutes wires the node utilities endpoints.
func (s *Server) registerUtilityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/nodes/{id}/utilities", s.auth(s.handleNodeUtilities))
	mux.HandleFunc("POST /ui/nodes/{id}/utilities/domain", s.auth(s.handleSaveTLSDomain))
	mux.HandleFunc("POST /ui/nodes/{id}/utilities/install", s.auth(s.handleInstallUtilities))
	mux.HandleFunc("POST /ui/nodes/{id}/utilities/cert", s.auth(s.handleIssueNodeCert))
	mux.HandleFunc("POST /ui/nodes/{id}/utilities/sub-sync", s.auth(s.handleSyncSubs))
	mux.HandleFunc("POST /ui/nodes/{id}/utilities/relay", s.auth(s.handleToggleRelay))
	mux.HandleFunc("POST /ui/nodes/{id}/utilities/{name}/uninstall", s.auth(s.handleUninstallUtility))
}

func validTLSDomain(d string) bool {
	return chain.ValidTLSDomain(d)
}

// utilityStaleMap computes per-utility staleness for the panel badge.
func (s *Server) utilityStaleMap(nodeID string, info *model.NodeInfo) map[string]bool {
	stale := map[string]bool{}
	if info == nil {
		return stale
	}
	st := s.store()
	for _, name := range model.AllUtilities() {
		if s, err := st.UtilityIsStale(nodeID, name); err == nil {
			stale[name] = s
		}
	}
	return stale
}

func (s *Server) renderUtilitiesPanel(w http.ResponseWriter, r *http.Request, h *model.Host, info *model.NodeInfo, report *chain.UtilityReport, errMsg string) {
	s.render(w, r, templates.NodeUtilitiesPanel(h, info, s.utilityStaleMap(h.ID, info), report, errMsg))
}

func (s *Server) handleNodeUtilities(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	s.renderUtilitiesPanel(w, r, host, info, nil, "")
}

func (s *Server) handleSaveTLSDomain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	domain := strings.ToLower(strings.TrimSpace(r.FormValue("tls_domain")))
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.Trim(domain, "/")
	if domain != "" && !validTLSDomain(domain) {
		info, _ := st.GetNodeInfo(id)
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "Invalid TLS domain — use a bare hostname like node1.example.com"))
		return
	}
	info, err := st.GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{}
	}
	info.ID = id
	info.TLSDomain = domain
	if err := st.SaveNodeInfo(info); err != nil {
		s.renderUtilitiesPanel(w, r, host, info, nil, err.Error())
		return
	}
	s.renderUtilitiesPanel(w, r, host, info, nil, "")
}

// connectForUtilities opens an SSH client to the node (resolving the SSH key)
// and returns it with its useSudo flag. The caller owns Close.
func (s *Server) connectForUtilities(r *http.Request, host *model.Host, info *model.NodeInfo) (ports.SSHClient, bool, error) {
	resolved := chain.ResolveHostKey(s.store(), host)
	client, err := s.SSHConnector().Connect(resolved.Addr, resolved.User, resolved.KeyPath)
	if err != nil {
		return nil, false, err
	}
	useSudo := info != nil && info.UseSudo
	return client, useSudo, nil
}

// setUtility records a utility state on the node (creating NodeInfo if needed).
func (s *Server) setUtility(nodeID, name string, installed bool, version, status string, rev int64) {
	st := s.store()
	u := &model.UtilityState{
		Name: name, Installed: installed, Version: version,
		Status: status, Revision: rev,
	}
	if installed {
		u.InstalledAt = time.Now()
	}
	_ = st.SetUtilityState(nodeID, u)
}

func (s *Server) handleInstallUtilities(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	if info == nil || strings.TrimSpace(info.TLSDomain) == "" {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "Set a TLS domain before installing utilities"))
		return
	}

	want := r.FormValue("name")
	var names []string
	if want == "all" || want == "" {
		names = model.AllUtilities()
	} else {
		names = []string{want}
	}

	client, useSudo, err := s.connectForUtilities(r, host, info)
	if err != nil {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "SSH connect failed: ")+err.Error())
		return
	}
	defer client.Close()

	rep := &chain.UtilityReport{}
	ctx := r.Context()
	rev := st.GetRevision()

	// Canonical order regardless of request order: webroot first, then the
	// router, then the cert machinery, then the subscription mount.
	for _, name := range model.AllUtilities() {
		if !contains(names, name) {
			continue
		}
		if info.UtilityInstalled(name) && name != model.UtilityACME {
			rep.AddSkip(name)
			continue
		}
		if err := s.installOne(ctx, client, useSudo, info, name, rep, rev); err != nil {
			s.setUtility(id, name, false, "", "error: "+err.Error(), 0)
			s.renderUtilitiesPanel(w, r, host, info, rep, err.Error())
			return
		}
		s.setUtility(id, name, true, rep.LastVersion(), "", rev)
	}

	if info2, err := st.GetNodeInfo(id); err == nil {
		info = info2
	}
	s.renderUtilitiesPanel(w, r, host, info, rep, "")
}

// installOne runs the install steps for a single utility. The Caddyfile push
// happens with the caddy step so the router always comes up with a valid,
// current config.
func (s *Server) installOne(ctx context.Context, client ports.SSHClient, useSudo bool, info *model.NodeInfo, name string, rep *chain.UtilityReport, rev int64) error {
	switch name {
	case model.UtilityFakesite:
		return chain.PushFakesite(ctx, client, useSudo, "", rep)

	case model.UtilityCaddy:
		if err := chain.InstallCaddy(ctx, client, useSudo, rep); err != nil {
			return err
		}
		if err := chain.BootstrapCert(ctx, client, useSudo, info.TLSDomain, rep); err != nil {
			return err
		}
		plan, err := chain.BuildCaddyPlan(info, rev)
		if err != nil {
			return err
		}
		cfg, err := chain.RenderCaddyfile(plan)
		if err != nil {
			return err
		}
		return chain.PushCaddyfile(ctx, client, useSudo, cfg, rep)

	case model.UtilityACME:
		if err := chain.InstallAcme(ctx, client, useSudo, rep); err != nil {
			return err
		}
		plan, err := chain.BuildCaddyPlan(info, rev)
		if err != nil {
			return err
		}
		return chain.IssueNodeCert(ctx, client, useSudo, info.TLSDomain, plan.CaddySANs(), rep)

	case model.UtilitySub:
		return chain.EnsureSubDir(ctx, client, useSudo, rep)
	}
	return nil
}

func (s *Server) handleIssueNodeCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	if info == nil || info.TLSDomain == "" || !info.UtilityInstalled(model.UtilityCaddy) || !info.UtilityInstalled(model.UtilityACME) {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "Install caddy + acme and set a TLS domain first"))
		return
	}
	client, useSudo, err := s.connectForUtilities(r, host, info)
	if err != nil {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "SSH connect failed: ")+err.Error())
		return
	}
	defer client.Close()

	rep := &chain.UtilityReport{}
	plan, err := chain.BuildCaddyPlan(info, st.GetRevision())
	if err != nil {
		s.renderUtilitiesPanel(w, r, host, info, nil, err.Error())
		return
	}
	if err := chain.IssueNodeCert(r.Context(), client, useSudo, info.TLSDomain, plan.CaddySANs(), rep); err != nil {
		s.renderUtilitiesPanel(w, r, host, info, rep, err.Error())
		return
	}
	s.setUtility(id, model.UtilityACME, true, "", "", st.GetRevision())
	s.renderUtilitiesPanel(w, r, host, info, rep, "")
}

func (s *Server) handleUninstallUtility(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	client, useSudo, err := s.connectForUtilities(r, host, info)
	if err != nil {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "SSH connect failed: ")+err.Error())
		return
	}
	defer client.Close()

	rep := &chain.UtilityReport{}
	if err := chain.UninstallUtility(r.Context(), client, useSudo, name, rep); err != nil {
		s.renderUtilitiesPanel(w, r, host, info, rep, err.Error())
		return
	}
	s.setUtility(id, name, false, "", "", 0)
	if info2, err := st.GetNodeInfo(id); err == nil {
		info = info2
	}
	s.renderUtilitiesPanel(w, r, host, info, rep, "")
}

// handleSyncSubs re-renders + pushes the full subscription static set to the
// node (manual "sync subscriptions"). Same path the post-apply hook uses.
func (s *Server) handleSyncSubs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	if info == nil || !info.UtilityInstalled(model.UtilitySub) {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "Install the subscription statics utility first"))
		return
	}
	if err := s.PushNodeSubscriptions(r.Context(), id); err != nil {
		if info2, err2 := st.GetNodeInfo(id); err2 == nil {
			info = info2
		}
		s.renderUtilitiesPanel(w, r, host, info, nil, err.Error())
		return
	}
	if info2, err := st.GetNodeInfo(id); err == nil {
		info = info2
	}
	rep := &chain.UtilityReport{}
	rep.AddStep(i18n.T(r.Context(), "Subscription files re-rendered and pushed"))
	s.renderUtilitiesPanel(w, r, host, info, rep, "")
}

// handleToggleRelay flips the per-node panel-relay opt-in and starts/stops the
// ssh -R loop accordingly. Requires the caddy utility (it routes
// panel.<domain> to the relay port).
func (s *Server) handleToggleRelay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	if info == nil {
		info = &model.NodeInfo{}
	}
	info.ID = id
	if !info.UtilityInstalled(model.UtilityCaddy) {
		s.renderUtilitiesPanel(w, r, host, info, nil, i18n.T(r.Context(), "Install caddy before enabling the panel relay"))
		return
	}
	info.PanelRelay = !info.PanelRelay
	if err := st.SaveNodeInfo(info); err != nil {
		s.renderUtilitiesPanel(w, r, host, info, nil, err.Error())
		return
	}
	if info.PanelRelay {
		s.StartNodeRelay(id)
	} else {
		s.StopNodeRelay(id)
	}
	s.renderUtilitiesPanel(w, r, host, info, nil, "")
}
