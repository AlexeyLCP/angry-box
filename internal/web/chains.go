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

// buildNodeProfiles maps each host to the inbound profiles deployed on it —
// the entry level's per-node inbound select only offers what is actually
// materialized there ("inbounds first, chains second"; no auto-deploy from
// the chain form).
func hostCountries(st *chain.Store, hosts []*model.Host) map[string]string {
	out := make(map[string]string, len(hosts))
	for _, h := range hosts {
		if info, err := st.GetNodeInfo(h.ID); err == nil && info != nil && info.Country != "" {
			out[h.ID] = info.Country
		}
	}
	return out
}

func buildNodeProfiles(st *chain.Store, hosts []*model.Host) map[string][]templates.ChainProfileOption {
	profs, _ := st.ListInboundProfiles()
	out := map[string][]templates.ChainProfileOption{}
	for _, h := range hosts {
		for _, p := range profs {
			if st.ProfileInboundOn(h.ID, p.ID) != nil {
				out[h.ID] = append(out[h.ID], templates.ChainProfileOption{ID: p.ID, Name: p.Name, Protocol: p.Protocol})
			}
		}
	}
	return out
}

func (s *Server) handleNewChainForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	s.render(w, r, templates.ChainLevelsForm(templates.ChainLevelsFormData{
		Hosts:        hosts,
		NodeProfiles: buildNodeProfiles(st, hosts),
		Presets:      chain.ListPresets(),
		PresetGroups: chain.GroupPresets(chain.ListPresetsDetailed()),
		Countries:    hostCountries(st, hosts),
	}))
}

// handleChainLevelRow serves a transit-level fieldset for the "add level"
// button (appended client-side via hx-swap="beforeend").
func (s *Server) handleChainLevelRow(w http.ResponseWriter, r *http.Request) {
	i := 0
	if _, err := fmt.Sscanf(r.URL.Query().Get("i"), "%d", &i); err != nil || i < 1 {
		i = 1
	}
	st := s.store()
	hosts, _ := st.ListHosts()
	s.render(w, r, templates.ChainLevelRow(i, templates.ChainLevelsFormData{Hosts: hosts, Countries: hostCountries(st, hosts)}, false))
}

// parseLevelsForm reads the levels editor wire format (level_<i>_nodes[],
// level_<i>_strategy, inboundref_<nodeID>) and builds the chain's Levels.
// Per-node transit material is preserved from `existing` (Rule 5 — keys
// never rotate on edit). The returned chain has UserProtocol derived from the
// entry profiles (all entry nodes must share one protocol).
func parseLevelsForm(r *http.Request, st *chain.Store, existing *model.Chain) (*model.Chain, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	if existing != nil {
		name = existing.Name
	}
	if name == "" {
		return nil, fmt.Errorf("%s", i18n.T(r.Context(), "Chain name is required"))
	}
	transport := model.TransportType(strings.TrimSpace(r.FormValue("transport")))
	if transport == "" {
		if existing != nil && existing.Transport != "" {
			transport = existing.Transport
		} else {
			transport = model.TransportXHTTP
		}
	}
	// Frozen guard: switching TO a frozen transport is rejected; preserving an
	// already-frozen one (unchanged submit) is allowed (AGENTS.md #11 nuance).
	if existing == nil || transport != existing.Transport {
		if err := chain.ValidateChainTransport(transport); err != nil {
			return nil, err
		}
	}

	c := &model.Chain{Name: name}
	if existing != nil {
		*c = *existing // carry chain-level material (legacy CPS fields, Strategy)
		c.Levels = nil // rebuilt below
	}
	// Form values win over the carried-over copy (the copy exists to preserve
	// material, not to override the edit).
	c.Transport = transport
	c.ObfuscationProfile = strings.TrimSpace(r.FormValue("profile"))

	entryProto := ""
	for i := 0; ; i++ {
		key := fmt.Sprintf("level_%d_nodes", i)
		if _, ok := r.Form[key]; !ok {
			break
		}
		seen := map[string]bool{}
		var ids []string
		for _, id := range r.Form[key] {
			if id = strings.TrimSpace(id); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "level %d has no nodes selected"), i))
		}
		strategy := model.Strategy(strings.TrimSpace(r.FormValue(fmt.Sprintf("level_%d_strategy", i))))
		switch strategy {
		case "", model.StrategyFallback, model.StrategyURLTest, model.StrategyFailover, model.StrategySelector:
		default:
			return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "level %d: invalid strategy %q"), i, strategy))
		}
		lv := model.ChainLevel{ID: fmt.Sprintf("l%d", i), Strategy: strategy}
		for _, id := range ids {
			h, err := st.GetHost(id)
			if err != nil {
				if errors.Is(err, chain.ErrHostNotFound) {
					return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "host %q not found"), id))
				}
				return nil, fmt.Errorf("store: %w", err)
			}
			n := model.ChainNode{ID: h.ID, Addr: h.Addr, User: h.User, KeyPath: h.KeyPath}
			if existing != nil {
				if old := existing.NodeByID(id); old != nil {
					// Preserve ALL persisted material (transit keys, exit
					// links, ports) — re-editing the chain must never rotate.
					n = *old
					n.Addr, n.User, n.KeyPath = h.Addr, h.User, h.KeyPath
				}
			}
			if i == 0 {
				ref := strings.TrimSpace(r.FormValue("inboundref_" + id))
				if ref == "" {
					return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "entry node %q has no inbound selected — deploy one on the Inbounds page first"), id))
				}
				prof, err := st.GetInboundProfile(ref)
				if err != nil {
					return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "entry node %q: inbound profile %q not found"), id, ref))
				}
				if st.ProfileInboundOn(id, ref) == nil {
					return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "inbound %q is not deployed on node %q — deploy it on the Inbounds page first"), prof.Name, id))
				}
				if entryProto == "" {
					entryProto = prof.Protocol
				} else if entryProto != prof.Protocol {
					return nil, fmt.Errorf("%s", fmt.Sprintf(i18n.T(r.Context(), "entry nodes must use inbounds of one protocol (%s vs %s)"), entryProto, prof.Protocol))
				}
				n.InboundRef = ref
				n.Role = "" // level position rules
			} else if n.Role == model.NodeRoleExit && i < lastLevelIndex(r) {
				// Role=exit is only meaningful on the last level (kernel AWG
				// exit balancer); a node moved to a mid level loses the marker.
				n.Role = ""
			}
			lv.Nodes = append(lv.Nodes, n)
		}
		c.Levels = append(c.Levels, lv)
	}
	if len(c.Levels) == 0 {
		return nil, fmt.Errorf("%s", i18n.T(r.Context(), "at least one level is required"))
	}
	if entryProto == "" {
		return nil, fmt.Errorf("%s", i18n.T(r.Context(), "entry level is required"))
	}
	c.UserProtocol = model.UserProtocol(entryProto)
	// Frozen user-protocol guard: new selection rejected; preserving allowed.
	if existing == nil || c.UserProtocol != existing.UserProtocol {
		if err := chain.ValidateChainUserProtocol(c.UserProtocol); err != nil {
			return nil, err
		}
	}
	if err := chain.ValidateChainTopology(c); err != nil {
		return nil, err
	}
	return c, nil
}

// lastLevelIndex returns the highest level index present in the form.
func lastLevelIndex(r *http.Request) int {
	last := 0
	for i := 0; ; i++ {
		if _, ok := r.Form[fmt.Sprintf("level_%d_nodes", i)]; !ok {
			return last
		}
		last = i
	}
}

// saveChainFromForm persists the parsed chain and materializes its entry
// inbounds (per-node creds + AWG obfs material) so client links work before
// the first apply.
func (s *Server) saveChainFromForm(st *chain.Store, c *model.Chain) error {
	if err := st.SaveChain(c); err != nil {
		return err
	}
	preset := chain.GetEffectivePreset(c)
	if c.ObfuscationProfile != "" {
		if p, ok := chain.GetPreset(c.ObfuscationProfile); ok {
			preset = p
		}
	}
	return chain.EnsureChainEntryMaterialization(st, c, preset)
}

func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	if _, err := st.GetChain(strings.TrimSpace(r.FormValue("name"))); err == nil {
		http.Error(w, i18n.T(r.Context(), "chain already exists"), http.StatusConflict)
		return
	}
	c, err := parseLevelsForm(r, st, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.saveChainFromForm(st, c); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "create", "chain", c.Name, chain.AuditPayload{"levels": len(c.Levels), "transport": c.Transport}, "operator")
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
	s.render(w, r, templates.ChainLevelsForm(templates.ChainLevelsFormData{
		Chain:        c,
		Hosts:        hosts,
		NodeProfiles: buildNodeProfiles(st, hosts),
		Presets:      chain.ListPresets(),
		PresetGroups: chain.GroupPresets(chain.ListPresetsDetailed()),
		Countries:    hostCountries(st, hosts),
	}))
}

func (s *Server) handleUpdateChain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	existing, err := st.GetChain(name)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "chain not found"), http.StatusNotFound)
		return
	}
	c, err := parseLevelsForm(r, st, existing)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.saveChainFromForm(st, c); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "update", "chain", c.Name, chain.AuditPayload{"levels": len(c.Levels), "transport": c.Transport}, "operator")
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
	// Keep the pushed subscription statics in lockstep with the store on every
	// node of the chain that runs the sub utility (no-op elsewhere).
	var subNotes []string
	for _, n := range c.AllNodes() {
		if note := s.maybePushSubsAfterApply(n.ID); note != "" {
			subNotes = append(subNotes, n.ID+": "+note)
		}
	}
	s.render(w, r, templates.ApplyResult(name, true, report, strings.Join(subNotes, " | ")))
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
	// Subscription statics follow the config: re-push the full set so the
	// node's /sub stays in lockstep with the store ("last config wins").
	if note := s.maybePushSubsAfterApply(id); note != "" {
		if resultMsg != "" {
			resultMsg += " | "
		}
		resultMsg += note
	}
	s.render(w, r, templates.ApplyResult(id, true, report, resultMsg))
}

// handleRelocateNode moves a node to a new VPS (new addr + optional user/key)
// and re-deploys every chain containing it so the new IP propagates to
// dependent nodes (previous hop + balancers on this node). The node's ID +
// transit/exit material are preserved, so re-deploy reuses the same
// credentials — other nodes + existing clients are not reconfigured. Renders a
// per-chain RelocateResult summary (success/error for each affected chain).
func (s *Server) handleRelocateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, i18n.T(r.Context(), "missing id"), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	newAddr := strings.TrimSpace(r.FormValue("new_addr"))
	if newAddr == "" {
		s.render(w, r, templates.RelocateResult(id, nil, i18n.T(r.Context(), "new address is required")))
		return
	}
	newUser := strings.TrimSpace(r.FormValue("new_user"))
	// SSH key: read the unified dropdown field. Empty preserves the current
	// KeyPath. Validate a registry id exists before relocating (a stale id
	// would make the re-deploy fail to dial).
	newKeyPath := strings.TrimSpace(r.FormValue("new_ssh_key_id"))
	if newKeyPath != "" && !strings.HasPrefix(newKeyPath, "password:") {
		if _, ok := s.store().ResolveKey(newKeyPath); !ok {
			s.render(w, r, templates.RelocateResult(id, nil, i18n.T(r.Context(), "Selected key not found in registry")))
			return
		}
	}

	st := s.store()
	applier := chain.NewApplier(s.factory, s.SSHConnector())
	ctx := context.Background()
	report, err := chain.RelocateNode(ctx, st, applier, id, newAddr, newUser, newKeyPath, "")
	if err != nil {
		s.render(w, r, templates.RelocateResult(id, nil, err.Error()))
		return
	}
	s.render(w, r, templates.RelocateResult(id, report, ""))
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
// registerChainRoutes wires every chain-scoped route (CRUD + apply + QUIC
// capture-preview) onto the mux. The spider apply path (/ui/spider/apply/{name})
// is registered in spider.go by path; handleApplyChain is shared. CTO-review §4.
func (s *Server) registerChainRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/chains", s.auth(s.handleChains))
	mux.HandleFunc("POST /ui/chains", s.auth(s.handleCreateChain))
	mux.HandleFunc("POST /ui/chains/capture-preview", s.auth(s.handleCaptureQUICPreview))
	mux.HandleFunc("DELETE /ui/chains/{name}", s.auth(s.handleDeleteChain))
	mux.HandleFunc("POST /ui/chains/{name}/apply", s.auth(s.handleApplyChain))
	mux.HandleFunc("GET /ui/chains/new", s.auth(s.handleNewChainForm))
	mux.HandleFunc("GET /ui/chains/level-row", s.auth(s.handleChainLevelRow))
	mux.HandleFunc("GET /ui/chains/{name}/edit", s.auth(s.handleEditChainForm))
	mux.HandleFunc("POST /ui/chains/{name}/edit", s.auth(s.handleUpdateChain))
}
