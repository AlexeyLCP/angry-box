package web

// chains.go — chain CRUD + apply handlers (extracted from ui.go as part of the
// M11 split).

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	chains, _ := st.ListChains()
	hosts, _ := st.ListHosts()
	s.renderContent(w, r, i18n.T(r.Context(), "Chains"), templates.Chains(chains, hosts))
}

func (s *Server) handleNewChainForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	profiles := chain.ListPresets()
	s.render(w, r, templates.NewChainForm(hosts, profiles))
}

func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	strategy := strings.TrimSpace(r.FormValue("strategy"))
	if strategy == "" {
		strategy = "urltest"
	}
	transport := model.TransportType(strings.TrimSpace(r.FormValue("transport")))
	if transport == "" {
		transport = model.TransportXHTTP
	}
	userProto := model.UserProtocol(strings.TrimSpace(r.FormValue("user_protocol")))
	if userProto == "" {
		userProto = model.UserProtocolAWG
	}
	profile := strings.TrimSpace(r.FormValue("profile"))

	nodeIDs := r.Form["nodes"]
	if len(nodeIDs) == 0 {
		nodeIDs = r.PostForm["nodes"]
	}
	seen := map[string]bool{}
	uniqueNodes := []string{}
	for _, id := range nodeIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			uniqueNodes = append(uniqueNodes, id)
		}
	}
	nodeIDs = uniqueNodes

	if name == "" || len(nodeIDs) < 1 {
		http.Error(w, i18n.T(r.Context(), "name and at least one node are required"), http.StatusBadRequest)
		return
	}

	// entry_nodes are the user-designated entry (user-facing) nodes. When at
	// least one is selected, those nodes get Role=entry and the rest transit.
	// When none are selected, all roles stay empty -> legacy "index 0 is entry".
	entrySet := map[string]bool{}
	for _, id := range r.Form["entry_nodes"] {
		entrySet[strings.TrimSpace(id)] = true
	}

	st := s.store()
	nodes := make([]model.ChainNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		h, err := st.GetHost(id)
		if err != nil {
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), id), http.StatusBadRequest)
			return
		}
		n := model.ChainNode{ID: h.ID, Addr: h.Addr, User: h.User, KeyPath: h.KeyPath}
		if entrySet[id] {
			n.Role = model.NodeRoleEntry
		}
		nodes = append(nodes, n)
	}

	c := &model.Chain{
		Name:               name,
		Nodes:              nodes,
		Strategy:           model.Strategy(strategy),
		Transport:          transport,
		UserProtocol:       userProto,
		ObfuscationProfile: profile,
	}

	// Generate stable AWG/TUIC creds at creation time
	if userProto == model.UserProtocol("awg") {
		priv, pub, err := chain.GenerateWireGuardKeypair()
		if err == nil {
			c.AWGEntryServerPriv = priv
			c.AWGEntryServerPub = pub
		}
	}
	if userProto == model.UserProtocol("tuic") {
		uuid, password := chain.GenerateStableTUICUserCreds()
		c.TUICEntryUserUUID = uuid
		c.TUICEntryUserPassword = password
	}

	if err := st.SaveChain(c); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	s.render(w, r, templates.ChainRow(c))
}

func (s *Server) handleEditChainForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "chain not found"), http.StatusNotFound)
		return
	}
	hosts, _ := st.ListHosts()
	profiles := chain.ListPresets()
	s.render(w, r, templates.EditChainForm(c, hosts, profiles))
}

func (s *Server) handleUpdateChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "chain not found"), http.StatusNotFound)
		return
	}

	c.Strategy = model.Strategy(strings.TrimSpace(r.FormValue("strategy")))
	if c.Strategy == "" {
		c.Strategy = "urltest"
	}
	transport := model.TransportType(strings.TrimSpace(r.FormValue("transport")))
	if transport != "" {
		c.Transport = transport
	}
	userProto := model.UserProtocol(strings.TrimSpace(r.FormValue("user_protocol")))
	if userProto != "" {
		c.UserProtocol = userProto
	}
	c.ObfuscationProfile = strings.TrimSpace(r.FormValue("profile"))

	// Update nodes if new ones selected
	nodeIDs := r.Form["nodes"]
	if len(nodeIDs) == 0 {
		nodeIDs = r.PostForm["nodes"]
	}
	if len(nodeIDs) > 0 {
		seen := map[string]bool{}
		uniqueNodes := []string{}
		for _, id := range nodeIDs {
			id = strings.TrimSpace(id)
			if id != "" && !seen[id] {
				seen[id] = true
				uniqueNodes = append(uniqueNodes, id)
			}
		}
		// entry_nodes for this edit. Empty set -> clear explicit roles, reverting
		// to the legacy "index 0 is entry" behavior.
		entrySet := map[string]bool{}
		for _, id := range r.Form["entry_nodes"] {
			entrySet[strings.TrimSpace(id)] = true
		}
		nodes := make([]model.ChainNode, 0, len(uniqueNodes))
		for _, id := range uniqueNodes {
			h, err := st.GetHost(id)
			if err != nil {
				continue
			}
			n := model.ChainNode{ID: h.ID, Addr: h.Addr, User: h.User, KeyPath: h.KeyPath}
			if entrySet[id] {
				n.Role = model.NodeRoleEntry
			}
			// Preserve persisted transit key material when the node was already in
			// the chain (re-applying an existing chain must not drop its keys).
			for _, old := range c.Nodes {
				if old.ID == id {
					n.TransitPrivKey = old.TransitPrivKey
					n.TransitShortID = old.TransitShortID
					n.TransitUUID = old.TransitUUID
					n.Port = old.Port
					n.Inbounds = old.Inbounds
					break
				}
			}
			nodes = append(nodes, n)
		}
		if len(nodes) > 0 {
			c.Nodes = nodes
		}
	}

	if err := st.SaveChain(c); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	// Return updated row
	s.render(w, r, templates.ChainRow(c))
}

func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, i18n.T(r.Context(), "missing name"), http.StatusBadRequest)
		return
	}
	if err := s.store().DeleteChain(name); err != nil {
		// A missing chain is a client-side 404 (idempotent delete of something
		// that was never there); other store errors are a 500.
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "failed: %v"), err), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "failed: %v"), err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *Server) handleApplyChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, i18n.T(r.Context(), "missing name"), http.StatusBadRequest)
		return
	}
	st := s.store()
	c, err := st.GetChain(name)
	if err != nil {
		s.render(w, r, templates.ApplyResult(name, false, nil, i18n.T(r.Context(), "chain not found")))
		return
	}
	resolved, err := st.ResolveNodes(c)
	if err != nil {
		s.render(w, r, templates.ApplyResult(name, false, nil, err.Error()))
		return
	}

	c.Nodes = resolved

	applier := chain.NewApplier(s.factory, s.SSHConnector())
	ctx := context.Background()
	report, err := applier.ApplyChain(ctx, st, c, "")
	if err != nil {
		msg := err.Error()
		if report != nil && len(report.Nodes) > 0 {
			for _, n := range report.Nodes {
				if !n.Success && n.Error != "" {
					msg += " | " + n.ID + ": " + n.Error
				}
			}
		}
		s.render(w, r, templates.ApplyResult(name, false, report, msg))
		return
	}
	s.render(w, r, templates.ApplyResult(name, true, report, ""))
}

func (s *Server) handleApplyNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, i18n.T(r.Context(), "missing id"), http.StatusBadRequest)
		return
	}
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		s.render(w, r, templates.ApplyResult(id, false, nil, i18n.T(r.Context(), "node not found")))
		return
	}

	applier := chain.NewApplier(s.factory, s.SSHConnector())
	ctx := context.Background()

	report, mergeReport, err := applier.ApplyMergedNode(ctx, st, info)
	st.SaveNodeInfo(info)

	if err != nil {
		msg := err.Error()
		if report != nil && len(report.Nodes) > 0 {
			for _, n := range report.Nodes {
				if !n.Success && n.Error != "" {
					msg += " | " + n.ID + ": " + n.Error
				}
			}
		}
		s.render(w, r, templates.ApplyResult(id, false, report, msg))
		return
	}

	resultMsg := ""
	if mergeReport != nil {
		parts := []string{fmt.Sprintf(i18n.T(r.Context(), "%d standalone inbounds + chains: %v"),
			mergeReport.StandaloneCount, mergeReport.ChainsIncluded)}
		if len(mergeReport.AddedInbounds) > 0 {
			parts = append(parts, fmt.Sprintf("+%s", strings.Join(mergeReport.AddedInbounds, ", +")))
		}
		if len(mergeReport.RemovedInbounds) > 0 {
			parts = append(parts, fmt.Sprintf("-%s", strings.Join(mergeReport.RemovedInbounds, ", -")))
		}
		resultMsg = strings.Join(parts, " | ")
	}
	s.render(w, r, templates.ApplyResult(id, true, report, resultMsg))
}