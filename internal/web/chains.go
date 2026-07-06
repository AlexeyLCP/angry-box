package web

// chains.go — chain CRUD + apply handlers (extracted from ui.go as part of the
// M11 split).

import (
	"context"
	"errors"
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
	if err := chain.ValidateChainTransport(transport); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userProto := model.UserProtocol(strings.TrimSpace(r.FormValue("user_protocol")))
	if userProto == "" {
		userProto = model.UserProtocolAWG
	}
	if err := chain.ValidateChainUserProtocol(userProto); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// AWG CPS mimicry + QUIC capture domain (chain-level override; preset only
	// ever sets "quic", so "quic-live" must be chosen explicitly here).
	awgCPSMimicry := strings.TrimSpace(r.FormValue("awg_cps_mimicry"))
	awgCPSCaptureDomain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
	if userProto == model.UserProtocol("awg") {
		switch awgCPSMimicry {
		case "", "quic-live", "quic", "sip", "dns", "none":
			// ok
		default:
			http.Error(w, i18n.T(r.Context(), "Invalid CPS mimicry mode"), http.StatusBadRequest)
			return
		}
		if awgCPSMimicry == "quic-live" && awgCPSCaptureDomain != "" {
			awgCPSCaptureDomain = chain.NormalizeDomain(awgCPSCaptureDomain)
			if !chain.IsValidDomain(awgCPSCaptureDomain) {
				http.Error(w, i18n.T(r.Context(), "Invalid capture domain"), http.StatusBadRequest)
				return
			}
		}
	} else {
		// Non-AWG: ignore any CPS fields silently (they shouldn't be sent).
		awgCPSMimicry = ""
		awgCPSCaptureDomain = ""
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
			// CTO-review §2: distinguish not-found (400 — user input error)
			// from store I/O failure (500 — server error). The sentinel is
			// set by the store on a missing host; any other error (corrupt
			// store, permission) surfaces as 500 with the real cause.
			if errors.Is(err, chain.ErrHostNotFound) {
				http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), id), http.StatusBadRequest)
				return
			}
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "store error: %v"), err), http.StatusInternalServerError)
			return
		}
		n := model.ChainNode{ID: h.ID, Addr: h.Addr, User: h.User, KeyPath: h.KeyPath}
		if entrySet[id] {
			n.Role = model.NodeRoleEntry
		}
		nodes = append(nodes, n)
	}

	c := &model.Chain{
		Name:                name,
		Nodes:               nodes,
		Strategy:            model.Strategy(strategy),
		Transport:           transport,
		UserProtocol:        userProto,
		ObfuscationProfile:  profile,
		AWGCPSMimicry:       awgCPSMimicry,
		AWGCPSCaptureDomain: awgCPSCaptureDomain,
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
	// Frozen-protocol guard: a chain that already uses a paused transport/user
	// protocol (TUIC, Hysteria2) may be re-saved as-is (display/edit permitted
	// per AGENTS.md — only NEW selection is blocked). So we only validate when
	// the value actually CHANGES; submitting the unchanged frozen value (the
	// `selected disabled` option) is preserved, while switching a non-frozen
	// chain TO a frozen one is rejected. Mirrors the settings.go default_protocol
	// guard (settings.DefaultProtocol != dp).
	transport := model.TransportType(strings.TrimSpace(r.FormValue("transport")))
	if transport != "" && transport != c.Transport {
		if err := chain.ValidateChainTransport(transport); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.Transport = transport
	}
	userProto := model.UserProtocol(strings.TrimSpace(r.FormValue("user_protocol")))
	if userProto != "" && userProto != c.UserProtocol {
		if err := chain.ValidateChainUserProtocol(userProto); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.UserProtocol = userProto
	}
	// AWG CPS mimicry + QUIC capture domain.
	awgCPSMimicry := strings.TrimSpace(r.FormValue("awg_cps_mimicry"))
	awgCPSCaptureDomain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
	if c.UserProtocol == model.UserProtocol("awg") {
		switch awgCPSMimicry {
		case "", "quic-live", "quic", "sip", "dns", "none":
			// ok
		default:
			http.Error(w, i18n.T(r.Context(), "Invalid CPS mimicry mode"), http.StatusBadRequest)
			return
		}
		if awgCPSMimicry == "quic-live" && awgCPSCaptureDomain != "" {
			awgCPSCaptureDomain = chain.NormalizeDomain(awgCPSCaptureDomain)
			if !chain.IsValidDomain(awgCPSCaptureDomain) {
				http.Error(w, i18n.T(r.Context(), "Invalid capture domain"), http.StatusBadRequest)
				return
			}
		}
		// Cache reset: changing the capture domain invalidates the prior capture.
		if awgCPSCaptureDomain != c.AWGCPSCaptureDomain {
			c.AWGCPSCapturedDomain = ""
			c.AWGCPSCaptureFailedDomain = ""
		}
		// Leaving quic-live: capture is irrelevant, drop all capture fields.
		if awgCPSMimicry != "quic-live" {
			c.AWGCPSCaptureDomain = ""
			c.AWGCPSCapturedDomain = ""
			c.AWGCPSCaptureFailedDomain = ""
		} else {
			c.AWGCPSCaptureDomain = awgCPSCaptureDomain
		}
		c.AWGCPSMimicry = awgCPSMimicry
	} else {
		// Non-AWG chain: clear any stale CPS fields (e.g. protocol switched away
		// from AWG in this same edit).
		c.AWGCPSMimicry = ""
		c.AWGCPSCaptureDomain = ""
		c.AWGCPSCapturedDomain = ""
		c.AWGCPSCaptureFailedDomain = ""
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

// handleCaptureQUICPreview runs chain.CaptureQUICSignature against the
// domain entered in the chain form and returns an inline I1-I5 preview (or a
// failure warning). It does NOT save anything — pure preview. The actual
// persist happens on ApplyChain via EnsureChainAWGMaterial.
func (s *Server) handleCaptureQUICPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	domain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
	if domain == "" {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid capture domain") + `</span></div>`})
		return
	}
	domain = chain.NormalizeDomain(domain)
	if !chain.IsValidDomain(domain) {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid capture domain") + `</span></div>`})
		return
	}
	res := chain.CaptureQUICSignature(domain, 0)
	if res.OK && len(res.Packets) >= 5 {
		var b strings.Builder
		b.WriteString(`<div class="alert alert-success"><div class="text-xs space-y-1"><div><strong>` + i18n.T(r.Context(), "Capture OK") + `</strong> — ` + escHTML(res.Source) + `</div>`)
		for i, p := range res.Packets {
			b.WriteString(fmt.Sprintf(`<div>I%d: <code>%s</code></div>`, i+1, escHTML(shortHex(p, 40))))
		}
		if res.Warning != "" {
			b.WriteString(`<div class="text-warning">` + escHTML(res.Warning) + `</div>`)
		}
		b.WriteString(`</div></div>`)
		s.render(w, r, &simpleHTML{html: b.String()})
		return
	}
	s.render(w, r, &simpleHTML{html: `<div class="alert alert-warning"><div class="text-xs space-y-1"><div><strong>` + i18n.T(r.Context(), "Capture failed, fell back to synthesized QUIC packets") + `</strong></div><div>` + escHTML(res.Warning) + `</div></div></div>`})
}