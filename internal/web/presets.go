package web

// presets.go — custom obfuscation preset CRUD (replaces the dead Profiles
// page). Built-in presets are read-only; custom presets live in
// PanelSettings.CustomPresets and are merged into the chain registry via
// LoadPresets.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	// built-ins: list all, tagged by Protocol
	builtins := []chain.ConnectionPreset{}
	for _, name := range chain.ListPresets() {
		p, ok := chain.GetPreset(name)
		if !ok {
			continue
		}
		// skip custom ones (they're in customs list)
		if isCustom(customs, name) {
			continue
		}
		builtins = append(builtins, p)
	}
	s.render(w, r, templates.PresetsPage(builtins, customs))
}

func isCustom(customs []chain.ConnectionPreset, name string) bool {
	for _, c := range customs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) handleNewPresetForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, templates.PresetForm(nil))
}

func (s *Server) handleCreatePreset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	p := presetFromForm(r)
	if p.Name == "" {
		http.Error(w, i18n.T(r.Context(), "name required"), http.StatusBadRequest)
		return
	}
	if p.Protocol == "" {
		http.Error(w, i18n.T(r.Context(), "protocol required"), http.StatusBadRequest)
		return
	}
	if _, ok := chain.GetPreset(p.Name); ok {
		http.Error(w, i18n.T(r.Context(), "preset already exists"), http.StatusConflict)
		return
	}
	if err := s.saveCustomPreset(p, false); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "create", "preset", p.Name, chain.AuditPayload{"protocol": p.Protocol}, "operator")
	s.render(w, r, templates.PresetsPage(s.builtinsList(), s.customsList()))
}

func (s *Server) handleEditPresetForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := chain.GetPreset(name)
	if !ok {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	if !isCustom(s.customsList(), name) {
		// built-in: read-only, but still show the form (fields disabled)
	}
	s.render(w, r, templates.PresetForm(&p))
}

func (s *Server) handleUpdatePreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	p := presetFromForm(r)
	if p.Name == "" {
		p.Name = name
	}
	if err := s.saveCustomPreset(p, true); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "update", "preset", p.Name, chain.AuditPayload{"protocol": p.Protocol}, "operator")
	s.render(w, r, templates.PresetsPage(s.builtinsList(), s.customsList()))
}

func (s *Server) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st := s.store()
	// Refuse if a chain or inbound references it.
	if usedBy := presetInUse(st, name); usedBy != "" {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "Preset is in use (chain/inbound references it)"), usedBy), http.StatusConflict)
		return
	}
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	filtered := customs[:0]
	for _, c := range customs {
		if c.Name == name {
			continue
		}
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		settings.CustomPresets = nil
	} else {
		b, _ := json.Marshal(filtered)
		settings.CustomPresets = b
	}
	st.SaveSettings(settings)
	_ = chain.LoadPresets(filtered) // reload registry without the deleted one
	chain.WriteAudit(st, "delete", "preset", name, nil, "operator")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// presetFromForm builds a ConnectionPreset from the form fields, scoped to the
// chosen protocol (only the matching sub-struct is populated).
func presetFromForm(r *http.Request) chain.ConnectionPreset {
	protocol := strings.TrimSpace(r.FormValue("protocol"))
	p := chain.ConnectionPreset{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Protocol:    protocol,
		Description: strings.TrimSpace(r.FormValue("description")),
	}
	switch protocol {
	case "awg":
		p.AWG = &chain.AWGPreset{
			JC:       atoi(r.FormValue("awg_jc")),
			JMIN:     atoi(r.FormValue("awg_jmin")),
			JMAX:     atoi(r.FormValue("awg_jmax")),
			S1:       atoi(r.FormValue("awg_s1")),
			S2:       atoi(r.FormValue("awg_s2")),
			S3:       atoi(r.FormValue("awg_s3")),
			S4:       atoi(r.FormValue("awg_s4")),
			ITime:    atoi(r.FormValue("awg_itime")),
			H1:       atoi(r.FormValue("awg_h1")),
			H2:       atoi(r.FormValue("awg_h2")),
			H3:       atoi(r.FormValue("awg_h3")),
			H4:       atoi(r.FormValue("awg_h4")),
			CPSLevel: atoi(r.FormValue("awg_cps_level")),
			Mimicry:  strings.TrimSpace(r.FormValue("awg_mimicry")),
		}
	case "vless-reality":
		p.Reality = &chain.RealityPreset{
			ServerNames:  formList(r, "reality_server_names"),
			Fingerprints: formList(r, "reality_fingerprints"),
			ShortIDLen:   atoi(r.FormValue("reality_short_id_len")),
		}
	case "xhttp":
		p.XHTTP = &chain.XHTTPPreset{
			Methods:        formList(r, "xhttp_methods"),
			Paths:          formList(r, "xhttp_paths"),
			Hosts:          formList(r, "xhttp_hosts"),
			IdleTimeout:    strings.TrimSpace(r.FormValue("xhttp_idle_timeout")),
			PingTimeout:    strings.TrimSpace(r.FormValue("xhttp_ping_timeout")),
			PaddingBytes:   strings.TrimSpace(r.FormValue("xhttp_padding_bytes")),
			MaxConcurrency: strings.TrimSpace(r.FormValue("xhttp_max_concurrency")),
			UpstreamHost:   strings.TrimSpace(r.FormValue("xhttp_upstream_host")),
			DownstreamHost: strings.TrimSpace(r.FormValue("xhttp_downstream_host")),
		}
	}
	return p
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func formList(r *http.Request, field string) []string {
	vals := r.Form[field]
	out := []string{}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) saveCustomPreset(p chain.ConnectionPreset, isUpdate bool) error {
	st := s.store()
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	replaced := false
	for i, c := range customs {
		if c.Name == p.Name {
			if !isUpdate {
				return fmt.Errorf("preset %q already exists", p.Name)
			}
			customs[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		customs = append(customs, p)
	}
	b, err := json.Marshal(customs)
	if err != nil {
		return err
	}
	settings.CustomPresets = b
	st.SaveSettings(settings)
	_ = chain.LoadPresets(customs) // reload registry
	return nil
}

func (s *Server) builtinsList() []chain.ConnectionPreset {
	out := []chain.ConnectionPreset{}
	customs := s.customsList()
	for _, name := range chain.ListPresets() {
		p, ok := chain.GetPreset(name)
		if !ok || isCustom(customs, name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (s *Server) customsList() []chain.ConnectionPreset {
	settings, _ := s.store().GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	return customs
}

// presetInUse returns a non-empty string describing the first chain/inbound
// referencing the preset, or "" if unused.
func presetInUse(st *chain.Store, name string) string {
	chains, _ := st.ListChains()
	for _, c := range chains {
		if c.ObfuscationProfile == name {
			return "chain:" + c.Name
		}
	}
	infos, _ := st.ListNodeInfos()
	for _, info := range infos {
		for _, ib := range info.Inbounds {
			if ib.Obfuscation == name {
				return "inbound:" + info.ID + "/" + ib.Tag
			}
		}
	}
	return ""
}