package web

// nodes.go — node CRUD + capture + inbound management handlers (extracted from
// ui.go as part of the M11 split).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
			if err := chain.LoadPresets(customs); err != nil {
				slog.Warn("handleNodes: load custom presets failed", "err", err)
			}
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
	// SSH key: read the unified dropdown field (ssh_key_id). The legacy
	// keyPath textarea is gone — only registry IDs or "password:<pass>" live
	// in Host.KeyPath now. An empty ssh_key_id preserves the current KeyPath
	// so the applier can fall back to the panel default (resolveHostKey).
	sshKeyID := strings.TrimSpace(r.FormValue("ssh_key_id"))
	if sshKeyID != "" {
		// Key-existence validation: reject a stale/typo'd id loudly rather
		// than silently saving an unresolvable KeyPath (the deploy-bug class).
		// "password:"-prefixed values are auth intents, not registry IDs.
		if !strings.HasPrefix(sshKeyID, "password:") {
			if _, ok := st.ResolveKey(sshKeyID); !ok {
				http.Error(w, i18n.T(r.Context(), "Selected key not found in registry"), http.StatusBadRequest)
				return
			}
		}
		host.KeyPath = sshKeyID
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

	// Read the unified wizard form fields.
	addr := strings.TrimSpace(r.FormValue("addr"))
	sshKeyID := strings.TrimSpace(r.FormValue("ssh_key_id"))
	loginUser := strings.TrimSpace(r.FormValue("login_user"))
	loginPass := strings.TrimSpace(r.FormValue("login_pass"))
	autoInstallKey := r.FormValue("auto_install_key") == "on"
	manualKeyData := strings.TrimSpace(r.FormValue("ssh_key_manual"))
	country := strings.TrimSpace(r.FormValue("country"))
	bandwidth := strings.TrimSpace(r.FormValue("bandwidth"))
	if loginUser == "" {
		loginUser = "root"
	}

	// ─── Validation (the deploy-bug root cause) ────────────────────────────
	// A node must not be savable with an empty KeyPath. Require either a key,
	// a manual paste, or a password. Without this, the applier later hits
	// os.ReadFile("") and the deploy fails with an opaque SSH error.
	if sshKeyID == "" && loginPass == "" && manualKeyData == "" {
		http.Error(w, i18n.T(r.Context(), "Choose a key or enter password"), http.StatusBadRequest)
		return
	}
	if addr == "" {
		http.Error(w, i18n.T(r.Context(), "Address is required"), http.StatusBadRequest)
		return
	}
	// Key-existence validation: a stale ssh_key_id (deleted key, typo) must
	// not silently fall through to a password/empty path. "manual" and
	// "password:"-prefixed values are auth intents, not registry IDs.
	if sshKeyID != "" && sshKeyID != "manual" && !strings.HasPrefix(sshKeyID, "password:") {
		if _, ok := st.ResolveKey(sshKeyID); !ok {
			http.Error(w, i18n.T(r.Context(), "Selected key not found in registry"), http.StatusBadRequest)
			return
		}
	}

	// Resolve new vs existing host. New nodes are built in-memory and only
	// saved on success (so a failed probe leaves no orphan record).
	host, err := st.GetHost(id)
	if err != nil {
		host = &model.Host{ID: id}
	}
	host.Addr = addr
	host.User = loginUser

	// Manual paste: persist the PEM as a registry entry so future deploys
	// resolve it via ResolveKey (no raw PEM in Host.KeyPath).
	if sshKeyID == "manual" && manualKeyData != "" {
		keyName := fmt.Sprintf("manual-%s", host.Addr)
		if strings.Contains(host.Addr, ":") {
			keyName = fmt.Sprintf("manual-%s", strings.Split(host.Addr, ":")[0])
		}
		keyID := fmt.Sprintf("key-manual-%d", time.Now().Unix())
		fp, fpErr := chain.DeriveKeyFingerprint(manualKeyData)
		if fpErr != nil {
			http.Error(w, i18n.T(r.Context(), "Invalid key format. Expected a private key (BEGIN ... PRIVATE KEY)."), http.StatusBadRequest)
			return
		}
		settings, _ := st.GetSettings()
		settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
			ID:          keyID,
			Name:        keyName,
			KeyData:     manualKeyData,
			Source:      model.SourceManual,
			Fingerprint: fp,
		})
		st.SaveSettings(settings)
		sshKeyID = keyID
	}

	// Determine the auth material for the probe. loginPass wins (we need it
	// for auto-install anyway); otherwise use the selected key id.
	authMethod := sshKeyID
	if loginPass != "" {
		authMethod = "password:" + loginPass
	}

	hostCopy := *host
	hostCopy.KeyPath = authMethod

	// Probe the remote (this is the SSH connection that can surface a
	// host-key mismatch → HostKeyWarning).
	f := s.factory
	b := f.Create()
	ctx := context.Background()
	status, sshErr := b.GetStatus(ctx, hostCopy)

	if sshErr != nil {
		var hkErr *sshclient.HostKeyError
		if errors.As(sshErr, &hkErr) {
			// Persist the actually-observed fingerprint so the subsequent
			// /trust POST can be verified against it (CTO-review §6: without
			// this, handleTrustHostKey would accept ANY fingerprint from a
			// forged POST, enabling MITM via UI/CSRF).
			if hkErr.RemoteFingerprint != "" {
				if info, err := st.GetNodeInfo(id); err == nil {
					info.PendingHostKeyFingerprint = hkErr.RemoteFingerprint
					if err := st.SaveNodeInfo(info); err != nil {
						slog.Warn("capture: persist pending fingerprint failed (existing node)", "node", id, "err", err)
					}
				} else {
					// New node: create a NodeInfo to carry the pending fp.
					if err := st.SaveNodeInfo(&model.NodeInfo{
						Host:                        model.Host{ID: id},
						PendingHostKeyFingerprint:   hkErr.RemoteFingerprint,
					}); err != nil {
						slog.Warn("capture: persist pending fingerprint failed (new node)", "node", id, "err", err)
					}
				}
			}
			s.render(w, r, templates.HostKeyWarning(*host, hkErr.RemoteFingerprint, hkErr.Changed))
			return
		}
		s.render(w, r, &simpleHTML{html: fmt.Sprintf(
			`<div class="alert alert-error"><span>`+i18n.T(r.Context(), "Capture failed: %v")+`</span></div>`, escHTML(sshErr.Error()),
		)})
		return
	}

	// Connection successful. Handle SSH key auto-install (only meaningful when
	// the user authenticated with a password and asked for a key to be
	// installed for passwordless future deploys).
	installMsg := ""
	if autoInstallKey && loginPass != "" {
		if sshKeyID == "" || strings.HasPrefix(sshKeyID, "system-") {
			// Auto-generate a new keypair and install it.
			privPEM, _, err := sshclient.GenerateSSHKeypair()
			if err != nil {
				installMsg = fmt.Sprintf(" <b>"+i18n.T(r.Context(), "Note:")+"</b> "+i18n.T(r.Context(), "SSH key auto-generation failed: %v"), escHTML(err.Error()))
				host.KeyPath = "password:" + loginPass
			} else {
				keyName := fmt.Sprintf("auto-%s", host.Addr)
				if strings.Contains(host.Addr, ":") {
					keyName = fmt.Sprintf("auto-%s", strings.Split(host.Addr, ":")[0])
				}
				keyID := fmt.Sprintf("key-auto-%d", time.Now().Unix())
				fp, _ := chain.DeriveKeyFingerprint(privPEM)

				settings, _ := st.GetSettings()
				settings.SSHKeys = append(settings.SSHKeys, model.SSHKeyEntry{
					ID:          keyID,
					Name:        keyName,
					KeyData:     privPEM,
					Source:      model.SourceAuto,
					Fingerprint: fp,
				})
				st.SaveSettings(settings)

				sshKeyID = keyID
				hostCopy.KeyPath = keyID // for install
			}
		}

		if sshKeyID != "" {
			if err := sshclient.InstallPublicKey(hostCopy.Addr, hostCopy.User, loginPass, sshKeyID); err != nil {
				installMsg = fmt.Sprintf(" <b>"+i18n.T(r.Context(), "Note:")+"</b> "+i18n.T(r.Context(), "SSH key installation failed: %v"), escHTML(err.Error()))
				host.KeyPath = "password:" + loginPass
			} else {
				// Key installed successfully — use the key instead of the password.
				host.KeyPath = sshKeyID
			}
		}
	} else if loginPass != "" {
		host.KeyPath = "password:" + loginPass
	} else if sshKeyID != "" {
		host.KeyPath = sshKeyID
	}

	st.SaveHost(host)

	info := &model.NodeInfo{
		Host:      *host,
		Country:   country,
		Bandwidth: bandwidth,
		Source:    "captured",
	}
	st.SaveNodeInfo(info)
	st.SaveMetrics(&model.NodeMetrics{
		HostID:            id,
		Online:            status.Running,
		Version:           status.Version,
		OS:                status.OS,
		SingBoxInstalled:  status.SingBoxInstalled,
		AWGModuleInstalled: status.AWGModuleInstalled,
	})

	// Status line: OS / sing-box / AWG module (the new GetStatus probes).
	statusLine := ""
	if status.OS != "" {
		statusLine += fmt.Sprintf(" · %s: %s", i18n.T(r.Context(), "OS"), escHTML(status.OS))
	}
	statusLine += fmt.Sprintf(" · %s: %s", i18n.T(r.Context(), "sing-box"),
		boolLabel(r.Context(), status.SingBoxInstalled))
	statusLine += fmt.Sprintf(" · %s: %s", i18n.T(r.Context(), "AWG kernel module"),
		boolLabel(r.Context(), status.AWGModuleInstalled))

	s.render(w, r, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>`+i18n.T(r.Context(), "Node %s captured! Running: %v, Version: %s.")+`%s%s</span>
		<button class="btn btn-sm btn-ghost" hx-get="/ui/nodes" hx-target="#main-content" hx-push-url="true">`+i18n.T(r.Context(), "Refresh Nodes")+`</button></div>`,
		escHTML(id), status.Running, escHTML(status.Version), statusLine, installMsg,
	)})
}

// boolLabel renders a localized yes/no-style label for a boolean status field.
func boolLabel(ctx context.Context, on bool) string {
	if on {
		return i18n.T(ctx, "installed")
	}
	return i18n.T(ctx, "not installed")
}

func (s *Server) handleNodeCaptureForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	// Render the wizard for BOTH new and existing nodes: a new id (not in the
	// store yet) gets an empty in-memory Host so the form renders pre-fill-free.
	host, err := st.GetHost(id)
	if err != nil {
		host = &model.Host{ID: id}
	}
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.NodeCaptureForm(host, settings, allKeys))
}

// handleRelocateForm renders the "relocate this node to a new VPS" modal for
// the node row's Relocate button. The form posts to /ui/nodes/{id}/relocate.
func (s *Server) handleRelocateForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	host, err := st.GetHost(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	settings, _ := st.GetSettings()
	allKeys := mergeSSHKeys(settings.SSHKeys, detectSystemKeys())
	s.render(w, r, templates.RelocateForm(host, settings, allKeys))
}

// handleTestNodeConnection runs ONLY GetStatus (no save, no install) so the
// user can verify the key/password works before committing to the wizard
// flow. The host is built one-off and never persisted.
func (s *Server) handleTestNodeConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	addr := strings.TrimSpace(r.FormValue("addr"))
	user := strings.TrimSpace(r.FormValue("login_user"))
	if user == "" {
		user = "root"
	}
	sshKeyID := strings.TrimSpace(r.FormValue("ssh_key_id"))
	loginPass := strings.TrimSpace(r.FormValue("login_pass"))
	manualKeyData := strings.TrimSpace(r.FormValue("ssh_key_manual"))

	// Build a one-off host (NOT saved).
	keyPath := sshKeyID
	if sshKeyID == "manual" && manualKeyData != "" {
		// Manual paste: write PEM to a temp file the SSH client can read.
		tmp, err := os.CreateTemp("", "ab-test-key-*")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := tmp.WriteString(manualKeyData); err != nil {
			tmp.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		keyPath = tmp.Name()
	}
	if loginPass != "" {
		keyPath = "password:" + loginPass
	}
	if keyPath == "" {
		// No explicit key/password — try the panel default key.
		if settings, err := s.store().GetSettings(); err == nil && settings.DefaultSSHKeyID != "" {
			keyPath = settings.DefaultSSHKeyID
		}
	}

	host := &model.Host{ID: id, Addr: addr, User: user, KeyPath: keyPath}
	b := s.factory.Create()
	status, err := b.GetStatus(r.Context(), *host)
	if err != nil {
		s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + escHTML(err.Error()) + `</span></div>`})
		return
	}
	s.render(w, r, &simpleHTML{html: fmt.Sprintf(
		`<div class="alert alert-success"><span>`+i18n.T(r.Context(), "Connection OK. sing-box: %s, OS: %s")+`</span></div>`,
		escHTML(status.Version), escHTML(status.OS))})
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
			if err := chain.LoadPresets(customs); err != nil {
				slog.Warn("load custom presets failed", "err", err)
			}
		}
	}
	users, _ := s.store().ListUsers()

	// Build protocol→presets JSON for client-side filtering (embedded in
	// dialog data attribute). Only protocol-scoped presets are offered (legacy
	// kitchen-sink presets with Protocol == "" are excluded by the strict
	// filter). SS/Trojan/VMess/Telemt reuse the XHTTP preset set. TUIC/Hysteria2
	// are frozen and omitted from new-selection dropdowns.
	protocolPresets := map[string][]string{
		"awg":           chain.ListPresetsForProtocol("awg"),
		"vless-reality": chain.ListPresetsForProtocol("vless-reality"),
		"xhttp":         chain.ListPresetsForProtocol("xhttp"),
		"shadowsocks":   chain.ListPresetsForProtocol("xhttp"), // SS uses XHTTP presets
		"trojan":        chain.ListPresetsForProtocol("xhttp"),
		"vmess":         chain.ListPresetsForProtocol("xhttp"),
		"telemt":        chain.ListPresetsForProtocol("xhttp"),
		// tuic/hysteria2 frozen — omitted from new-selection dropdowns
	}
	presetsJSON, _ := json.Marshal(protocolPresets)

	s.render(w, r, templates.NodeInboundsForm(info, users, string(presetsJSON)))
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

		if chain.IsFrozenStandaloneProtocol(newIb.Protocol) {
			allowed := false
			for _, oldIb := range info.Inbounds {
				if oldIb.Source == "" && oldIb.Protocol == newIb.Protocol && oldIb.Port == newIb.Port {
					allowed = true
					break
				}
			}
			if !allowed {
				http.Error(w, chain.ValidateStandaloneProtocol(newIb.Protocol).Error(), http.StatusBadRequest)
				return
			}
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
				newIb.Tag = oldIb.Tag
				break
			}
		}
		// Stable inbound tag (used as the sing-box inbound/endpoint tag + the
		// users-by-inbound map key). Generated once, preserved across re-saves.
		if newIb.Tag == "" {
			tag, err := chain.GenerateInboundTag(newIb.Protocol)
			if err != nil {
				http.Error(w, i18n.T(r.Context(), "failed to generate inbound tag"), http.StatusInternalServerError)
				return
			}
			newIb.Tag = tag
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

// handleMarkNodeBlocked sets a node's health state to NodeStateBlocked. This is
// an operator-initiated action (the orchestrator SSHes from a free region and
// cannot observe a DPI block itself — see AGENTS.md / P1a). Blocked is sticky:
// the metrics loop never clears it; the operator must unblock explicitly. The
// reason from the form is recorded in StateReason + the audit payload.
func (s *Server) handleMarkNodeBlocked(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if _, err := st.GetHost(id); err != nil {
		http.Error(w, i18n.T(r.Context(), "Host not found"), http.StatusNotFound)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		reason = "operator marked"
	}

	m, _ := st.GetMetrics(id)
	if m == nil {
		m = &model.NodeMetrics{HostID: id, State: model.NodeStateUnknown}
	}
	chain.SetNodeState(m, model.NodeStateBlocked, reason)
	m.Online = false
	if err := st.SaveMetrics(m); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "blocked", "node", id, chain.AuditPayload{"reason": reason}, "operator")

	// HTMX swaps the status cell back in (hx-target="closest td" on the button).
	s.render(w, r, templates.NodeStatusCell(id, m))
}

// handleClearNodeBlocked clears an operator-marked block, resetting the state
// to NodeStateUnknown so the next metrics tick reclassifies from live signals
// (rather than inheriting stale counters). Only acts on an actually-blocked
// node — unblocking a healthy/down node is a no-op-able error (409).
func (s *Server) handleClearNodeBlocked(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if _, err := st.GetHost(id); err != nil {
		http.Error(w, i18n.T(r.Context(), "Host not found"), http.StatusNotFound)
		return
	}
	m, _ := st.GetMetrics(id)
	if m == nil || m.State != model.NodeStateBlocked {
		http.Error(w, i18n.T(r.Context(), "node is not blocked"), http.StatusConflict)
		return
	}
	chain.SetNodeState(m, model.NodeStateUnknown, "operator cleared")
	if err := st.SaveMetrics(m); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "unblocked", "node", id, chain.AuditPayload{}, "operator")

	s.render(w, r, templates.NodeStatusCell(id, m))
}

// registerNodeRoutes wires every node-scoped route (CRUD + capture + test +
// inbounds + apply) onto the mux. Trust-host-key lives in dashboard.go (the
// handler is there); host-status lives in misc.go. Grouped by path prefix
// /ui/nodes and /ui/hosts (the legacy alias). CTO-review §4: split out of
// server.go Register (~60 routes in one method).
func (s *Server) registerNodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/hosts", s.auth(s.handleNodes))
	mux.HandleFunc("GET /ui/nodes", s.auth(s.handleNodes))
	mux.HandleFunc("GET /ui/nodes/{id}/edit", s.auth(s.handleEditNodeForm))
	mux.HandleFunc("POST /ui/nodes/{id}/edit", s.auth(s.handleUpdateNode))
	mux.HandleFunc("DELETE /ui/nodes/{id}", s.auth(s.handleDeleteNode))
	mux.HandleFunc("POST /ui/nodes/{id}/capture", s.auth(s.handleCaptureNode))
	mux.HandleFunc("GET /ui/nodes/{id}/capture", s.auth(s.handleNodeCaptureForm))
	mux.HandleFunc("POST /ui/nodes/{id}/test", s.auth(s.handleTestNodeConnection))
	mux.HandleFunc("GET /ui/nodes/{id}/inbounds", s.auth(s.handleNodeInboundsForm))
	mux.HandleFunc("POST /ui/nodes/{id}/inbounds", s.auth(s.handleSaveNodeInbounds))
	// apply is a node-path route but the handler shares chain apply logic —
	// registered here by path, handler stays in chains.go.
	mux.HandleFunc("POST /ui/nodes/{id}/apply", s.auth(s.handleApplyNode))
	// relocate: move a blocked node to a new VPS + re-deploy dependent chains.
	mux.HandleFunc("GET /ui/nodes/{id}/relocate", s.auth(s.handleRelocateForm))
	mux.HandleFunc("POST /ui/nodes/{id}/relocate", s.auth(s.handleRelocateNode))
	// health: operator mark/clear a DPI block (P1a). Not auto-detected — the
	// orchestrator can't see a block from its free-region vantage point.
	mux.HandleFunc("POST /ui/nodes/{id}/block", s.auth(s.handleMarkNodeBlocked))
	mux.HandleFunc("POST /ui/nodes/{id}/unblock", s.auth(s.handleClearNodeBlocked))
}
