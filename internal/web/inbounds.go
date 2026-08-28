package web

// inbounds.go — first-class InboundProfile CRUD (the /ui/inbounds page).
//
// A profile is a node-independent listener template; deployment is a diff
// over the desired node list applied by chain.ApplyProfileToNodes (per-node
// creds generated once, removals blocked while a chain references the
// profile, users-lost warnings surfaced). The page also hosts the Presets
// tab (obfuscation presets are inbound parameters).

import (
	cryptoRand "crypto/rand"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// autoInboundName returns a unique human-readable profile name for a protocol
// (e.g. "awg-3") so adding an inbound needs no manual typing.
func autoInboundName(st *chain.Store, proto string) string {
	existing := map[string]bool{}
	if profs, err := st.ListInboundProfiles(); err == nil {
		for _, p := range profs {
			existing[p.Name] = true
		}
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s-%d", proto, i)
		if !existing[name] {
			return name
		}
	}
}

// autoInboundPort picks a free port in the 40000-59999 range that is not used
// by any inbound on the selected nodes (all nodes when none selected), not a
// reserved orchestrator/caddy port, and not taken by another profile. Returns 0
// if the range is exhausted (caller rejects).
func autoInboundPort(st *chain.Store, nodeIDs []string) int {
	used := map[int]bool{
		80: true, 443: true, 2080: true, 8080: true, 8443: true, 8900: true, 9080: true, 1080: true,
	}
	sel := map[string]bool{}
	for _, id := range nodeIDs {
		sel[id] = true
	}
	if infos, err := st.ListNodeInfos(); err == nil {
		for _, info := range infos {
			if len(nodeIDs) > 0 && !sel[info.ID] {
				continue
			}
			for _, ib := range info.Inbounds {
				used[ib.Port] = true
			}
		}
	}
	if profs, err := st.ListInboundProfiles(); err == nil {
		for _, p := range profs {
			used[p.Port] = true
		}
	}
	var buf [2]byte
	if _, err := cryptoRand.Read(buf[:]); err != nil {
		return 0
	}
	base := (int(buf[0])<<8 | int(buf[1])) % 20000
	for off := 0; off < 20000; off++ {
		p := 40000 + (base+off)%20000
		if !used[p] {
			return p
		}
	}
	return 0
}

// profileViews pairs every profile with its computed deployment state for the
// list (placement derived from NodeInbound.ProfileID — the source of truth).
func (s *Server) profileViews(st *chain.Store) []templates.InboundProfileView {
	profs, _ := st.ListInboundProfiles()
	chains, _ := st.ListChains()
	infos, _ := st.ListNodeInfos()
	var out []templates.InboundProfileView
	for _, p := range profs {
		v := templates.InboundProfileView{Profile: p, NodeIDs: st.ProfileNodes(p.ID)}
		seen := map[string]bool{}
		for _, c := range chains {
			for _, n := range c.AllNodes() {
				if n.InboundRef == p.ID && !seen[c.Name] {
					seen[c.Name] = true
					v.ChainRefs = append(v.ChainRefs, c.Name)
				}
			}
		}
		for _, ni := range infos {
			for _, ib := range ni.Inbounds {
				if ib.ProfileID == p.ID {
					v.UsersTotal += len(ib.ForUsers)
				}
			}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) handleInbounds(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	infos, _ := st.ListNodeInfos()
	s.renderContent(w, r, i18n.T(r.Context(), "Inbounds"), templates.InboundsPage(s.profileViews(st), infos))
}

func (s *Server) handleNewInboundForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	infos, _ := st.ListNodeInfos()
	s.render(w, r, templates.InboundForm(nil, nil, infos, chain.ListPresets(), chain.GroupPresets(chain.ListPresetsDetailed())))
}

func (s *Server) handleEditInboundForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	p, err := st.GetInboundProfile(r.PathValue("id"))
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "inbound profile not found"), http.StatusNotFound)
		return
	}
	deployed := map[string]bool{}
	for _, id := range st.ProfileNodes(p.ID) {
		deployed[id] = true
	}
	infos, _ := st.ListNodeInfos()
	s.render(w, r, templates.InboundForm(p, deployed, infos, chain.ListPresets(), chain.GroupPresets(chain.ListPresetsDetailed())))
}

// inboundFromForm parses + validates the profile form. Returns the profile
// (without ID) and the desired node list.
func inboundFromForm(r *http.Request) (*model.InboundProfile, []string, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	// name may be empty: create auto-generates one, update preserves the existing.
	proto := strings.TrimSpace(r.FormValue("protocol"))
	switch proto {
	case "awg", "vless-reality", "mtproxy", "naive", "mieru", "trusttunnel":
	default:
		return nil, nil, fmt.Errorf("%s", i18n.T(r.Context(), "unsupported protocol"))
	}
	if err := chain.ValidateStandaloneProtocol(proto); err != nil {
		return nil, nil, err
	}
	port := atoi(r.FormValue("port"))
	if proto == "mtproxy" && port == 0 {
		port = 443 // MTProxy's canonical FakeTLS port
	}
	// port 0 = "auto-assign" (filled by the caller); only an explicit
	// out-of-range value is rejected.
	if port != 0 && (port < 1 || port > 65535) {
		return nil, nil, fmt.Errorf("%s", i18n.T(r.Context(), "invalid port"))
	}
	var nodeIDs []string
	for _, id := range r.Form["node_ids"] {
		if id = strings.TrimSpace(id); id != "" {
			nodeIDs = append(nodeIDs, id)
		}
	}
	p := &model.InboundProfile{
		Name:        name,
		Protocol:    proto,
		Port:        port,
		Obfuscation: strings.TrimSpace(r.FormValue("obfuscation")),
	}
	// AWG live-capture fields (moved from the chain form in v0.8 — the entry
	// profile owns the obfuscation). Mimicry "quic-live" requires a valid
	// capture domain; non-AWG profiles ignore both silently.
	if proto == "awg" {
		mimicry := strings.TrimSpace(r.FormValue("awg_cps_mimicry"))
		switch mimicry {
		case "", "quic-live", "quic", "sip", "dns", "none":
		default:
			return nil, nil, fmt.Errorf("%s", i18n.T(r.Context(), "Invalid CPS mimicry mode"))
		}
		domain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
		if mimicry == "quic-live" {
			if domain == "" {
				return nil, nil, fmt.Errorf("%s", i18n.T(r.Context(), "Invalid capture domain"))
			}
			domain = chain.NormalizeDomain(domain)
			if !chain.IsValidDomain(domain) {
				return nil, nil, fmt.Errorf("%s", i18n.T(r.Context(), "Invalid capture domain"))
			}
		} else {
			domain = ""
		}
		p.AWGCPSMimicry = mimicry
		p.AWGCPSCaptureDomain = domain
		// AWG protocol version selector (AGENTS #5, revision). The new canonical
		// picker is awg_version ("1.5" | "2" | "3"); the legacy awg3_mode=="1"
		// checkbox is kept as a backward-compat fallback so an older rendered
		// form still works. AWGVersion drives everything via EffectiveAWGVersion;
		// AWG3Mode is mirrored as a synonym so old read-sites keep working.
		version := strings.TrimSpace(r.FormValue("awg_version"))
		switch version {
		case model.AWGVersion1x, model.AWGVersion2, model.AWGVersion3, model.AWGVersion31:
			p.AWGVersion = version
		case "":
			// Legacy form (pre-version dropdown) — honor the AWG3 checkbox.
			if r.FormValue("awg3_mode") == "1" {
				p.AWGVersion = model.AWGVersion3
			}
		default:
			return nil, nil, fmt.Errorf("%s", i18n.T(r.Context(), "Invalid AWG version"))
		}
		p.AWG3Mode = p.AWGVersion == model.AWGVersion3
	}
	if proto == "mieru" {
		t := strings.ToUpper(strings.TrimSpace(r.FormValue("mieru_transport")))
		if t != "UDP" {
			t = "TCP"
		}
		p.MieruTransport = t
	}
	if dest := strings.TrimSpace(r.FormValue("server_name")); dest != "" {
		p.ServerName = destHost(dest)
	}
	return p, nodeIDs, nil
}

func destHost(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// applyProfileAndDeploy saves the profile, applies the node diff, and
// schedules background deploys on every affected node.
func (s *Server) applyProfileAndDeploy(w http.ResponseWriter, r *http.Request, prof *model.InboundProfile, nodeIDs []string, action string) {
	st := s.store()
	// Utility gating: on caddy-mode nodes (TLSDomain set) TLS-terminating
	// protocols are refused until the cert utilities are installed, and
	// MTProxy may not squat the caddy-owned ports (chain.ValidateUtilityDeps).
	for _, nodeID := range nodeIDs {
		info, err := st.GetNodeInfo(nodeID)
		if err != nil {
			http.Error(w, nodeID+": "+err.Error(), http.StatusNotFound)
			return
		}
		if err := chain.ValidateUtilityDeps(info, prof.Protocol, prof.Port); err != nil {
			http.Error(w, nodeID+": "+err.Error(), http.StatusConflict)
			return
		}
	}
	// Live QUIC capture: run it (once per profile+domain) at save time so the
	// UI reflects the outcome immediately; the captured material is shared by
	// every materialized inbound. Best-effort — a capture failure falls back
	// to synthesized packets (the profile still saves).
	if prof.AWGCPSCaptureDomain != "" {
		preset := chain.GetDefaultPreset()
		if prof.Obfuscation != "" {
			if p, ok := chain.GetPreset(prof.Obfuscation); ok {
				preset = p
			}
		}
		_ = chain.EnsureProfileAWGMaterial(prof, preset)
	}
	if err := st.SaveInboundProfile(prof); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	res, err := chain.ApplyProfileToNodes(st, prof, nodeIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	for _, nodeID := range res.AffectedNodes() {
		chain.ScheduleAutoApply(nodeID, "inbound-profile:"+prof.ID)
	}
	chain.WriteAudit(st, action, "inbound_profile", prof.ID, chain.AuditPayload{
		"name": prof.Name, "protocol": prof.Protocol, "port": prof.Port,
		"added": res.Added, "removed": res.Removed, "updated": res.Updated, "blocked": res.Blocked,
	}, "operator")
	if len(res.Blocked) > 0 {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "Profile saved, but removal on %s was refused: a chain references it there"), strings.Join(res.Blocked, ", ")), http.StatusConflict)
		return
	}
	w.Header().Set("HX-Redirect", "/ui/inbounds")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	prof, nodeIDs, err := inboundFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st := s.store()
	// Auto-fill (tester UX request): an empty name/port gets a generated
	// default so adding an inbound needs no manual typing.
	if prof.Name == "" {
		prof.Name = autoInboundName(st, prof.Protocol)
	}
	if prof.Port == 0 {
		prof.Port = autoInboundPort(st, nodeIDs)
		if prof.Port == 0 {
			http.Error(w, i18n.T(r.Context(), "no free port available"), http.StatusBadRequest)
			return
		}
	}
	prof.ID = uniqueProfileID(st, slugifyProfileName(prof.Name))
	s.applyProfileAndDeploy(w, r, prof, nodeIDs, "create")
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	existing, err := st.GetInboundProfile(r.PathValue("id"))
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "inbound profile not found"), http.StatusNotFound)
		return
	}
	prof, nodeIDs, err := inboundFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Empty name/port on edit preserves the existing values (the form normally
	// pre-fills them; this guards a cleared field from wiping them).
	if prof.Name == "" {
		prof.Name = existing.Name
	}
	if prof.Port == 0 {
		prof.Port = existing.Port
	}
	// Protocol is immutable while materializations exist — the per-node creds
	// are protocol-specific; changing it would strand them. Recreate instead.
	if prof.Protocol != existing.Protocol && len(st.ProfileNodes(existing.ID)) > 0 {
		http.Error(w, i18n.T(r.Context(), "Protocol change requires recreating the profile (delete + create): deployed credentials are protocol-specific"), http.StatusConflict)
		return
	}
	prof.ID = existing.ID
	prof.CreatedAt = existing.CreatedAt
	// Carry over the capture cache so EnsureProfileAWGMaterial can decide
	// validity; a domain/mimicry change invalidates it (re-capture or reset).
	if prof.AWGCPSCaptureDomain != existing.AWGCPSCaptureDomain || prof.AWGCPSMimicry != existing.AWGCPSMimicry {
		prof.AWGCPSLevel = 0
		prof.AWGCPSI1, prof.AWGCPSI2, prof.AWGCPSI3, prof.AWGCPSI4, prof.AWGCPSI5 = "", "", "", "", ""
		prof.AWGH1, prof.AWGH2, prof.AWGH3, prof.AWGH4 = "", "", "", ""
		prof.AWGCPSCapturedDomain = ""
		prof.AWGCPSCaptureFailedDomain = ""
	} else {
		prof.AWGCPSLevel = existing.AWGCPSLevel
		prof.AWGCPSI1 = existing.AWGCPSI1
		prof.AWGCPSI2 = existing.AWGCPSI2
		prof.AWGCPSI3 = existing.AWGCPSI3
		prof.AWGCPSI4 = existing.AWGCPSI4
		prof.AWGCPSI5 = existing.AWGCPSI5
		prof.AWGH1 = existing.AWGH1
		prof.AWGH2 = existing.AWGH2
		prof.AWGH3 = existing.AWGH3
		prof.AWGH4 = existing.AWGH4
		prof.AWGCPSCapturedDomain = existing.AWGCPSCapturedDomain
		prof.AWGCPSCaptureFailedDomain = existing.AWGCPSCaptureFailedDomain
	}
	// AWG 3.0 material (AGENTS #5): HPK/CPM/RAT are generated once and
	// persisted on the profile. Carry them over on edit so toggling AWG3
	// off→on reuses the same keys (clients don't break) and off keeps them
	// dormant (not emitted, but preserved for a later re-enable). When AWG3
	// is newly enabled and no material exists yet, EnsureProfileAWGMaterial
	// generates it at deploy time.
	prof.AWG3HeaderProtectionKey = existing.AWG3HeaderProtectionKey
	prof.AWG3ContentPaddingAddition = existing.AWG3ContentPaddingAddition
	prof.AWG3RekeyAfterTime = existing.AWG3RekeyAfterTime
	s.applyProfileAndDeploy(w, r, prof, nodeIDs, "update")
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	prof, err := st.GetInboundProfile(r.PathValue("id"))
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "inbound profile not found"), http.StatusNotFound)
		return
	}
	// Remove every materialization first (blocked nodes refuse).
	res, err := chain.ApplyProfileToNodes(st, prof, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if len(res.Blocked) > 0 {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "Profile is in use by a chain on %s — edit the chain first"), strings.Join(res.Blocked, ", ")), http.StatusConflict)
		return
	}
	if err := st.DeleteInboundProfile(prof.ID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	for _, nodeID := range res.AffectedNodes() {
		chain.ScheduleAutoApply(nodeID, "inbound-profile-delete:"+prof.ID)
	}
	chain.WriteAudit(st, "delete", "inbound_profile", prof.ID, chain.AuditPayload{"name": prof.Name}, "operator")
	w.WriteHeader(http.StatusOK)
}

// slugifyProfileName turns a display name into an ID base.
func slugifyProfileName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "inbound"
	}
	return "prof-" + out
}

// uniqueProfileID dedupes the slug against existing profile IDs.
func uniqueProfileID(st *chain.Store, base string) string {
	if _, err := st.GetInboundProfile(base); err != nil {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if _, err := st.GetInboundProfile(cand); err != nil {
			return cand
		}
	}
}

// registerInboundRoutes wires the first-class InboundProfile CRUD.
func (s *Server) registerInboundRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/inbounds", s.auth(s.handleInbounds))
	mux.HandleFunc("POST /ui/inbounds", s.auth(s.handleCreateInbound))
	mux.HandleFunc("GET /ui/inbounds/new", s.auth(s.handleNewInboundForm))
	mux.HandleFunc("GET /ui/inbounds/{id}/edit", s.auth(s.handleEditInboundForm))
	mux.HandleFunc("POST /ui/inbounds/{id}/edit", s.auth(s.handleUpdateInbound))
	mux.HandleFunc("DELETE /ui/inbounds/{id}", s.auth(s.handleDeleteInbound))
}
