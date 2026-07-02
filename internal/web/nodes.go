package web

// nodes.go — node CRUD + capture + inbound management handlers (extracted from
// ui.go as part of the M11 split).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	hosts, _ := st.ListHosts()
	infos, _ := st.ListNodeInfos()
	metrics, _ := st.ListMetrics()
	chains, _ := st.ListChains()
	settings, _ := st.GetSettings()
	if len(settings.CustomPresets) > 0 {
		var customs []chain.ConnectionPreset
		if json.Unmarshal(settings.CustomPresets, &customs) == nil && len(customs) > 0 {
			_ = chain.LoadPresets(customs)
		}
	}
	activeChains := make(map[string]string)
	for _, c := range chains {
		for _, n := range c.Nodes {
			activeChains[n.ID] = c.Name
		}
	}
	s.renderContent(w, r, i18n.T(r.Context(), "Nodes"), templates.Nodes(hosts, infos, metrics, activeChains))
}

func (s *Server) handleNewNodeForm(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store().GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeForm(nil, settings, allKeys))
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	addr := strings.TrimSpace(r.FormValue("addr"))
	user := strings.TrimSpace(r.FormValue("user"))
	if user == "" {
		user = "root"
	}
	keyPath := strings.TrimSpace(r.FormValue("keyPath"))
	country := strings.TrimSpace(r.FormValue("country"))
	bandwidth := strings.TrimSpace(r.FormValue("bandwidth"))

	if id == "" || addr == "" {
		http.Error(w, i18n.T(r.Context(), "id and addr are required"), http.StatusBadRequest)
		return
	}

	st := s.store()
	if err := st.SaveHost(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	st.SaveNodeInfo(&model.NodeInfo{
		Host:      model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath},
		Country:   country,
		Bandwidth: bandwidth,
		Source:    "ssh_key",
	})

	chains, _ := st.ListChains()
	chainName := ""
	for _, c := range chains {
		for _, n := range c.Nodes {
			if n.ID == id {
				chainName = c.Name
			}
		}
	}

	s.render(w, r, templates.NodeRow(&model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath},
		&model.NodeInfo{Country: country, Bandwidth: bandwidth, Source: "ssh_key"}, nil, chainName))
}

func (s *Server) handleEditNodeForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	info, _ := st.GetNodeInfo(id)
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeForm(host, settings, allKeys, info))
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
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
	host.Addr = strings.TrimSpace(r.FormValue("addr"))
	host.User = strings.TrimSpace(r.FormValue("user"))
	if keyPath := strings.TrimSpace(r.FormValue("keyPath")); keyPath != "" {
		host.KeyPath = keyPath
	}
	st.SaveHost(host)

	info := &model.NodeInfo{
		Host:      *host,
		Country:   strings.TrimSpace(r.FormValue("country")),
		Bandwidth: strings.TrimSpace(r.FormValue("bandwidth")),
		Source:    strings.TrimSpace(r.FormValue("source")),
	}
	st.SaveNodeInfo(info)

	if isHTMXRequest(r) {
		chains, _ := st.ListChains()
		chainName := ""
		for _, c := range chains {
			for _, n := range c.Nodes {
				if n.ID == id {
					chainName = c.Name
				}
			}
		}
		s.render(w, r, templates.NodeRow(host, info, nil, chainName))
	} else {
		http.Redirect(w, r, "/ui/nodes", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if err := st.DeleteHost(id); err != nil {
		msg := fmt.Sprintf(`<tr class="bg-error/10"><td colspan="6" class="p-4 text-error font-medium">`+i18n.T(r.Context(), "Failed to delete: %v")+`. <button class="btn btn-xs btn-outline ml-4" onclick="location.reload()">Dismiss</button></td></tr>`, err)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(msg))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *Server) handleCaptureNode(w http.ResponseWriter, r *http.Request) {
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

	selectedKey := strings.TrimSpace(r.FormValue("ssh_key"))
	loginUser := strings.TrimSpace(r.FormValue("login_user"))
	loginPass := strings.TrimSpace(r.FormValue("login_pass"))
	autoInstallKey := r.FormValue("auto_install_key") == "on"
	manualKeyData := strings.TrimSpace(r.FormValue("ssh_key_manual"))

	// If the user pasted a manual key, save it persistently to the database
	if selectedKey == "manual" && manualKeyData != "" {
		settings, _ := st.GetSettings()
		keyName := fmt.Sprintf("manual-%s", host.Addr)
		if strings.Contains(host.Addr, ":") {
			keyName = fmt.Sprintf("manual-%s", strings.Split(host.Addr, ":")[0])
		}
		keyID := fmt.Sprintf("key-manual-%d", time.Now().Unix())

		settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
			ID:      keyID,
			Name:    keyName,
			KeyData: manualKeyData,
		})
		st.SaveSettings(settings)
		selectedKey = keyID
	}

	authMethod := selectedKey

	if loginUser != "" {
		host.User = loginUser
	}

	if loginPass != "" {
		authMethod = "password:" + loginPass
	}

	hostCopy := *host
	hostCopy.KeyPath = authMethod

	// Try SSH connection
	f := s.factory
	b := f.Create()
	ctx := context.Background()
	status, sshErr := b.GetStatus(ctx, hostCopy)

	if sshErr != nil {
		var hkErr *sshclient.HostKeyError
		if errors.As(sshErr, &hkErr) {
			s.render(w, r, templates.HostKeyWarning(*host, hkErr.RemoteFingerprint, hkErr.Changed))
			return
		}
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(
			`<div class="alert alert-error"><span>`+i18n.T(r.Context(), "Capture failed: %v")+`</span></div>`, escHTML(sshErr.Error()),
		)})
		return
	}

	// Connection successful. Handle SSH key auto-install.
	installMsg := ""
	if autoInstallKey && loginPass != "" {
		if selectedKey == "" || strings.HasPrefix(selectedKey, "system-") {
			// Auto-generate a new keypair
			privPEM, _, err := sshclient.GenerateSSHKeypair()
			if err != nil {
				installMsg = fmt.Sprintf(" <b>"+i18n.T(r.Context(), "Note:")+"</b> "+i18n.T(r.Context(), "SSH key auto-generation failed: %v"), escHTML(err.Error()))
				host.KeyPath = "password:" + loginPass
			} else {
				// Save it to settings so we can use it
				keyName := fmt.Sprintf("auto-%s", host.Addr)
				if strings.Contains(host.Addr, ":") {
					keyName = fmt.Sprintf("auto-%s", strings.Split(host.Addr, ":")[0])
				}
				keyID := fmt.Sprintf("key-auto-%d", time.Now().Unix())

				settings, _ := st.GetSettings()
				settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
					ID:      keyID,
					Name:    keyName,
					KeyData: privPEM,
				})
				st.SaveSettings(settings)

				selectedKey = keyID
				hostCopy.KeyPath = keyID // update for install
			}
		}

		if selectedKey != "" {
			if err := sshclient.InstallPublicKey(hostCopy.Addr, hostCopy.User, loginPass, selectedKey); err != nil {
				installMsg = fmt.Sprintf(" <b>"+i18n.T(r.Context(), "Note:")+"</b> "+i18n.T(r.Context(), "SSH key installation failed: %v"), escHTML(err.Error()))
				host.KeyPath = "password:" + loginPass
			} else {
				// Key installed successfully! Use the key instead of password.
				host.KeyPath = selectedKey
			}
		}
	} else if loginPass != "" {
		host.KeyPath = "password:" + loginPass
	} else if selectedKey != "" {
		host.KeyPath = selectedKey
	}

	st.SaveHost(host)

	info := &model.NodeInfo{
		Host:   *host,
		Source: "captured",
	}
	st.SaveNodeInfo(info)
	st.SaveMetrics(&model.NodeMetrics{
		HostID:  id,
		Online:  status.Running,
		Version: status.Version,
	})

	s.render(w, r, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>`+i18n.T(r.Context(), "Node %s captured! Running: %v, Version: %s.")+`%s</span>
		<button class="btn btn-sm btn-ghost" hx-get="/ui/nodes" hx-target="#main-content" hx-push-url="true">`+i18n.T(r.Context(), "Refresh Nodes")+`</button></div>`,
		escHTML(id), status.Running, escHTML(status.Version), installMsg,
	)})
}

func (s *Server) handleNodeCaptureForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeCaptureForm(host, settings, allKeys))
}

func (s *Server) handleNodeInboundsForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.store().GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{Host: model.Host{ID: id}}
	}
	settings, _ := s.store().GetSettings()
	if len(settings.CustomPresets) > 0 {
		var customs []chain.ConnectionPreset
		if json.Unmarshal(settings.CustomPresets, &customs) == nil && len(customs) > 0 {
			_ = chain.LoadPresets(customs)
		}
	}
	users, _ := s.store().ListUsers()
	presets := chain.ListPresets()

	// Build protocol→presets JSON for client-side filtering (embedded in dialog data attribute)
	protocolPresets := map[string][]string{
		"awg":           chain.ListPresetsForProtocol("awg"),
		"tuic":          chain.ListPresetsForProtocol("tuic"),
		"vless-reality": chain.ListPresetsForProtocol("vless-reality"),
		"shadowsocks":   chain.ListPresetsForProtocol("shadowsocks"),
		"trojan":        chain.ListPresetsForProtocol("trojan"),
		"vmess":         chain.ListPresetsForProtocol("vmess"),
		"hysteria2":     chain.ListPresetsForProtocol("hysteria2"),
		"telemt":        chain.ListPresetsForProtocol("telemt"),
	}
	presetsJSON, _ := json.Marshal(protocolPresets)

	s.render(w, r, templates.NodeInboundsForm(info, users, presets, string(presetsJSON)))
}

func (s *Server) handleSaveNodeInbounds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	info, err := st.GetNodeInfo(id)
	if err != nil {
		info = &model.NodeInfo{Host: model.Host{ID: id}}
	}

	protocols := r.Form["proto"]
	ports := r.Form["port"]
	indexes := r.Form["inbound_index"]
	obfuscations := r.Form["obfuscation"]

	// Guard: refuse to save zero inbounds if no chain-managed ones exist either.
	chainInbounds := 0
	for _, ib := range info.Inbounds {
		if ib.Source != "" {
			chainInbounds++
		}
	}
	if len(protocols) == 0 && chainInbounds == 0 {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-warning"><svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"/></svg><span>` + i18n.T(r.Context(), "Cannot save zero inbounds. Add at least one inbound or delete the node instead.") + `</span></div>`})
		return
	}

	// Port conflict check against all chains containing this node
	chainsForNode, _ := st.GetChainsForNode(id)
	chainPorts := make(map[int]string)
	for _, c := range chainsForNode {
		for i, n := range c.Nodes {
			if n.ID != id {
				continue
			}
			port := n.Port
			if port == 0 {
				if i == 0 {
					port = 8443
				} else {
					port = 443
				}
			}
			chainPorts[port] = c.Name
		}
	}

	for _, pStr := range ports {
		port, err := strconv.Atoi(pStr)
		if err != nil {
			s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + fmt.Sprintf(i18n.T(r.Context(), "Invalid port %q: must be a number."), escHTML(pStr)) + `</span></div>`})
			return
		}
		if verr := validatePort(port); verr != nil {
			s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + escHTML(verr.Error()) + `</span></div>`})
			return
		}
		if cName, ok := chainPorts[port]; ok {
			s.render(w, r, &simpleHTML{html: fmt.Sprintf(`<div class="alert alert-error">`+i18n.T(r.Context(), "Port %d is reserved for chain %q on this node and cannot be used for standalone inbounds.")+`</div>`, port, escHTML(cName))})
			return
		}
	}

	// Start with existing chain-managed inbounds (preserved, not editable in this form)
	inbounds := make([]model.NodeInbound, 0, len(protocols)+chainInbounds)
	for _, oldIb := range info.Inbounds {
		if oldIb.Source != "" {
			inbounds = append(inbounds, oldIb)
		}
	}
	for i := range protocols {
		if i >= len(indexes) {
			continue
		}
		port, _ := strconv.Atoi(ports[i])
		idx := indexes[i]

		forUsers := r.Form["for_users_"+idx]

		obf := ""
		if i < len(obfuscations) {
			obf = obfuscations[i]
		}

		newIb := model.NodeInbound{
			Protocol:    protocols[i],
			Port:        port,
			ForUsers:    forUsers,
			Obfuscation: obf,
		}

		// Preserve existing generated credentials if port and protocol match
		for _, oldIb := range info.Inbounds {
			if oldIb.Protocol == newIb.Protocol && oldIb.Port == newIb.Port {
				newIb.UUID = oldIb.UUID
				newIb.ServerPrivKey = oldIb.ServerPrivKey
				newIb.ServerPubKey = oldIb.ServerPubKey
				newIb.ShortID = oldIb.ShortID
				newIb.TLSCertificate = oldIb.TLSCertificate
				newIb.TLSPrivateKey = oldIb.TLSPrivateKey
				newIb.AWGClientPub = oldIb.AWGClientPub
				newIb.AWGClientPriv = oldIb.AWGClientPriv
				newIb.ObfsPassword = oldIb.ObfsPassword
				break
			}
		}

		// Hysteria2: generate a per-node obfs password once and persist it, so
		// the server and the client link share the same secret and the fleet
		// does not use a single predictable obfs password.
		if newIb.Protocol == "hysteria2" && newIb.ObfsPassword == "" {
			newIb.ObfsPassword = chain.GenerateHysteria2ObfsPassword()
		}

		// Generate self-signed TLS certificate for protocols that need it (TUIC, Hysteria2, etc.)
		// if not already present. This ensures the inbound can be applied without "missing certificate" errors.
		if (newIb.Protocol == "tuic" || newIb.Protocol == "hysteria2") &&
			(newIb.TLSCertificate == "" || newIb.TLSPrivateKey == "") {

			preset := chain.GetDefaultPreset()
			serverName := chain.ResolveServerName(&preset)

			if cert, key, cerr := chain.GenerateSelfSignedCert(serverName); cerr == nil {
				newIb.TLSCertificate = cert
				newIb.TLSPrivateKey = key
			}
		}

		if newIb.Protocol == "awg" && newIb.AWGClientPub == "" {
			if priv, pub, cerr := chain.GenerateWireGuardKeypair(); cerr == nil {
				newIb.AWGClientPub = pub
				newIb.AWGClientPriv = priv
			}
		}

		inbounds = append(inbounds, newIb)
	}
	info.Inbounds = inbounds
	st.SaveNodeInfo(info)
	chain.WriteAudit(st, "update", "node", info.ID, chain.AuditPayload{"inbounds": len(inbounds)}, "operator")
	chain.ScheduleAutoApply(info.ID, "inbounds update")
	s.render(w, r, &simpleHTML{html: `<div class="alert alert-success">` + i18n.T(r.Context(), "Inbounds saved.") + `</div>`})
}