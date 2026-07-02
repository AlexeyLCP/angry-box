package web

// users.go — user CRUD + client config/link generation (extracted from ui.go as
// part of the M11 split). Includes the client URI / .conf builders used to
// render copy-pasteable configs for a user's assigned chains and standalone
// inbounds.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListUsers()
	chains, _ := st.ListChains()

	// Auto-deactivate expired users on every view
	now := time.Now()
	for _, u := range users {
		if u.Active && !u.ExpiresAt.IsZero() && now.After(u.ExpiresAt) {
			u.Active = false
			st.SaveUser(u)
		}
	}

	s.renderContent(w, r, i18n.T(r.Context(), "Users"), templates.Users(users, chains))
}

func (s *Server) handleNewUserForm(w http.ResponseWriter, r *http.Request) {
	chains, _ := s.store().ListChains()
	s.render(w, r, templates.UserForm(nil, chains))
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	telegram := strings.TrimSpace(r.FormValue("telegram"))
	email := strings.TrimSpace(r.FormValue("email"))
	expiryStr := strings.TrimSpace(r.FormValue("expires_at"))
	protocols := r.Form["protocols"]
	chainNames := r.Form["chains"]
	importedSecret := strings.TrimSpace(r.FormValue("imported_secret"))
	secretType := strings.TrimSpace(r.FormValue("secret_type"))

	if id == "" || name == "" {
		http.Error(w, i18n.T(r.Context(), "id and name are required"), http.StatusBadRequest)
		return
	}

	var expiresAt time.Time
	if expiryStr != "" {
		expiresAt, _ = time.Parse("2006-01-02", expiryStr)
	}

	u := &model.User{
		ID:             id,
		Name:           name,
		Telegram:       telegram,
		Email:          email,
		ExpiresAt:      expiresAt,
		Active:         true,
		Protocols:      protocols,
		ChainNames:     chainNames,
		ImportedSecret: importedSecret,
		SecretType:     secretType,
		CreatedAt:      time.Now(),
	}

	if len(u.Protocols) == 0 {
		u.Protocols = []string{"awg"}
	}

	// Generate per-user credentials for the selected protocols so this user
	// can authenticate to a multi-user inbound with its own identity (the basis
	// for per-client auth_user routing). Existing creds are preserved.
	chain.EnsureUserCreds(u)

	st := s.store()
	if err := st.SaveUser(u); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "create", "user", u.ID, chain.AuditPayload{"name": u.Name, "protocols": u.Protocols}, "operator")
	s.scheduleAutoApplyForUser(st, u, "user create")
	s.render(w, r, templates.UserRow(u))
}

func (s *Server) handleEditUserForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, err := s.store().GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	chains, _ := s.store().ListChains()
	s.render(w, r, templates.UserForm(u, chains))
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}

	u.Name = strings.TrimSpace(r.FormValue("name"))
	u.Telegram = strings.TrimSpace(r.FormValue("telegram"))
	u.Email = strings.TrimSpace(r.FormValue("email"))
	if expiryStr := strings.TrimSpace(r.FormValue("expires_at")); expiryStr != "" {
		u.ExpiresAt, _ = time.Parse("2006-01-02", expiryStr)
	} else {
		u.ExpiresAt = time.Time{}
	}
	u.Protocols = r.Form["protocols"]
	u.ChainNames = r.Form["chains"]
	u.ImportedSecret = strings.TrimSpace(r.FormValue("imported_secret"))
	u.SecretType = strings.TrimSpace(r.FormValue("secret_type"))
	u.Active = r.FormValue("active") == "on"

	if len(u.Protocols) == 0 {
		u.Protocols = []string{"awg"}
	}

	// Fill any per-user credentials that are now missing because a new protocol
	// was added to this user's Protocols list. Existing creds are preserved.
	chain.EnsureUserCreds(u)

	st.SaveUser(u)
	chain.WriteAudit(st, "update", "user", u.ID, chain.AuditPayload{"name": u.Name, "protocols": u.Protocols}, "operator")
	s.scheduleAutoApplyForUser(st, u, "user update")
	if isHTMXRequest(r) {
		s.render(w, r, templates.UserRow(u))
	} else {
		http.Redirect(w, r, "/ui/users", http.StatusSeeOther)
	}
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	// Load the user first so we can audit + schedule auto-apply on the chains it
	// belonged to before deletion.
	if u, err := st.GetUser(id); err == nil {
		chain.WriteAudit(st, "delete", "user", id, chain.AuditPayload{"name": u.Name}, "operator")
		s.scheduleAutoApplyForUser(st, u, "user delete")
	}
	if err := st.DeleteUser(id); err != nil {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "delete: %v"), err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// scheduleAutoApplyForUser triggers background deploys on every node of every
// chain the user belongs to. Best-effort: missing chains/nodes are skipped.
func (s *Server) scheduleAutoApplyForUser(st *chain.Store, u *model.User, reason string) {
	if u == nil {
		return
	}
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		for _, n := range c.Nodes {
			chain.ScheduleAutoApply(n.ID, reason+":"+chainName)
		}
	}
}

func (s *Server) handleUserConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "user not found"), http.StatusNotFound)
		return
	}

	// Generate configs for user's assigned chains
	var configs []templates.UserChainConfig
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		// Build a config link for this chain
		link := buildConnectionLink(c, u)
		configs = append(configs, templates.UserChainConfig{
			ChainName:   chainName,
			Protocol:    string(c.UserProtocol),
			ConfigLink:  link,
			Description: fmt.Sprintf(i18n.T(r.Context(), "%s chain — %d hops, strategy: %s"), chainName, len(c.Nodes), c.Strategy),
		})
	}

	// Generate configs for standalone inbounds assigned to this user
	nodes, _ := st.ListNodeInfos()
	for _, node := range nodes {
		for _, ib := range node.Inbounds {
			if contains(ib.ForUsers, u.ID) {
				link := buildStandaloneLink(node.Addr, ib, u)
				configs = append(configs, templates.UserChainConfig{
					ChainName:   "node: " + node.ID,
					Protocol:    ib.Protocol,
					ConfigLink:  link,
					Description: fmt.Sprintf(i18n.T(r.Context(), "Standalone inbound on %s (port %d)"), node.ID, ib.Port),
				})
			}
		}
	}

	// If no configs assigned, list available chains for the user to choose from
	if len(configs) == 0 {
		allChains, _ := st.ListChains()
		if len(allChains) > 0 {
			configs = append(configs, templates.UserChainConfig{
				ChainName:   "unassigned",
				Protocol:    "any",
				ConfigLink:  "# Assign chains or node inbounds to this user to generate configs.",
				Description: fmt.Sprintf(i18n.T(r.Context(), "User has no chains assigned. %d chain(s) available — edit user to assign."), len(allChains)),
			})
		} else {
			configs = append(configs, templates.UserChainConfig{
				ChainName:   "no-chains",
				Protocol:    "any",
				ConfigLink:  "# Create a chain or node inbound first, then assign it to this user.",
				Description: i18n.T(r.Context(), "No chains or standalone inbounds exist yet."),
			})
		}
	}

	s.render(w, r, templates.UserConfigView(u, configs))
}

func buildConnectionLink(c *model.Chain, u *model.User) string {
	if len(c.Nodes) == 0 {
		return "# no nodes in chain"
	}
	entry := c.Nodes[0]
	proto := string(c.UserProtocol)
	if proto == "" {
		proto = "awg"
	}

	ip := strings.Split(entry.Addr, ":")[0]
	// Per-user TUIC creds take precedence over the chain-wide shared creds so
	// each user's share link authenticates as that user (per-client routing).
	// Fall back to chain-wide when the user has no per-user identity (legacy).
	uuid := c.TUICEntryUserUUID
	password := c.TUICEntryUserPassword
	if u != nil {
		if u.TUICUUID != "" {
			uuid = u.TUICUUID
		}
		if u.TUICPassword != "" {
			password = u.TUICPassword
		}
	}
	return buildClientURI(proto, ip, 8443, uuid, password, c.AWGEntryServerPub, "", c.Name, u, "", false)
}

func buildStandaloneLink(addr string, ib model.NodeInbound, u *model.User) string {
	ip := strings.Split(addr, ":")[0]
	if ib.Protocol == "awg" && u.ImportedSecret == "" && ib.AWGClientPriv != "" {
		// Full AWG client .conf with Amnezia obfuscation params from the active
		// preset (Jc/Jmin/Jmax/S1-S4/H1-H4 + I1-I5 when CPS is enabled). This
		// matches the server-side amnezia block so the client connects.
		return buildAWGClientConf(ip, ib.Port, ib.AWGClientPriv, ib.ServerPubKey, ib.AWGClientPub, "")
	}
	if ib.Protocol == "awg" && u.ImportedSecret != "" && u.SecretType == "awg" {
		// Imported AWG private key — build a .conf using it.
		return buildAWGClientConf(ip, ib.Port, u.ImportedSecret, ib.ServerPubKey, "", "")
	}
	if ib.Protocol == "mtproxy" {
		full, err := chain.MTProxyFullSecret(ib.UUID, defaultFakeTLSDomain(ib))
		if err != nil {
			full = "ee" + ib.UUID
		}
		return fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", ip, ib.Port, full)
	}
	// Hysteria2 uses a self-signed cert when TLSCertificate is present, so the
	// client link must carry insecure=1 only in that case; the per-node obfs
	// password travels via ib.ObfsPassword.
	insecure := ib.Protocol == "hysteria2" && ib.TLSCertificate != ""
	return buildClientURI(ib.Protocol, ip, ib.Port, ib.UUID, ib.ServerPrivKey, ib.ServerPubKey, ib.ShortID, ib.Protocol, u, ib.ObfsPassword, insecure)
}

// defaultFakeTLSDomain returns the MTProxy FakeTLS domain for an inbound (the
// Obfuscation field if set, else the canonical "disk.yandex.ru").
func defaultFakeTLSDomain(ib model.NodeInbound) string {
	if ib.Obfuscation != "" {
		return ib.Obfuscation
	}
	return "disk.yandex.ru"
}

// buildAWGClientConf renders a full awg-quick client .conf with Amnezia params.
// hostOverride lets callers swap the domain/IP for the Endpoint line.
func buildAWGClientConf(ip string, port int, clientPriv, serverPub, clientPub, hostOverride string) string {
	host := hostOverride
	if host == "" {
		host = ip
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("Address = 10.8.0.2/24\n")
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", clientPriv))
	b.WriteString("MTU = 1420\n\n")
	b.WriteString("[Peer]\n")
	b.WriteString(fmt.Sprintf("PublicKey = %s\n", serverPub))
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	b.WriteString(fmt.Sprintf("Endpoint = %s:%d\n", host, port))
	b.WriteString("PersistentKeepalive = 25\n")
	// Amnezia params from the active preset's AWG section.
	if preset := chain.GetDefaultPreset(); preset.AWG != nil {
		amn := chain.BuildAWGAmnezia(preset.AWG, &preset)
		if amn != nil {
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("Jc = %d\n", amn.JC))
			b.WriteString(fmt.Sprintf("Jmin = %d\n", amn.JMIN))
			b.WriteString(fmt.Sprintf("Jmax = %d\n", amn.JMAX))
			b.WriteString(fmt.Sprintf("S1 = %d\n", amn.S1))
			b.WriteString(fmt.Sprintf("S2 = %d\n", amn.S2))
			b.WriteString(fmt.Sprintf("S3 = %d\n", amn.S3))
			b.WriteString(fmt.Sprintf("S4 = %d\n", amn.S4))
			b.WriteString(fmt.Sprintf("H1 = %s\n", amn.H1))
			b.WriteString(fmt.Sprintf("H2 = %s\n", amn.H2))
			b.WriteString(fmt.Sprintf("H3 = %s\n", amn.H3))
			b.WriteString(fmt.Sprintf("H4 = %s\n", amn.H4))
			if amn.I1 != "" {
				b.WriteString(fmt.Sprintf("I1 = %s\n", amn.I1))
				b.WriteString(fmt.Sprintf("I2 = %s\n", amn.I2))
				b.WriteString(fmt.Sprintf("I3 = %s\n", amn.I3))
				b.WriteString(fmt.Sprintf("I4 = %s\n", amn.I4))
				b.WriteString(fmt.Sprintf("I5 = %s\n", amn.I5))
			}
		}
	}
	_ = clientPub
	return b.String()
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// buildClientURI is a unified helper for generating copy-pasteable client configurations (URIs)
func buildClientURI(proto, ip string, port int, uuid, privKey, pubKey, shortID, name string, u *model.User, obfsPassword string, insecure bool) string {
	switch proto {
	case "awg":
		if u != nil && u.ImportedSecret != "" && u.SecretType == "awg" {
			return buildAWGClientConf(ip, port, u.ImportedSecret, pubKey, "", "")
		}
		return fmt.Sprintf("awg://%s:%d?pub=%s&psk=&mtu=1420", ip, port, pubKey)
	case "tuic":
		if u != nil && u.ImportedSecret != "" && u.SecretType == "tuic" {
			return fmt.Sprintf("tuic://%s:%s@%s:%d?congestion_control=bbr&alpn=h3",
				u.ImportedSecret, u.ImportedSecret, ip, port)
		}
		// For standalone TUIC, password is in privKey. For chains, it's passed explicitly in privKey argument.
		return fmt.Sprintf("tuic://%s:%s@%s:%d?congestion_control=bbr&alpn=h3",
			uuid, privKey, ip, port)
	case "vless-reality":
		serverName := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			serverName = preset.Reality.ServerNames[0]
		}
		return fmt.Sprintf("vless://%s@%s:%d?type=tcp&security=reality&pbk=%s&sni=%s&sid=%s&fp=chrome&flow=xtls-rprx-vision",
			uuid, ip, port, pubKey, serverName, shortID)
	case "vless-reality-xhttp":
		// Combined REALITY + XHTTP max obfuscation share-link. Values are
		// percent-encoded (per protocol_presets.generate_vless_reality_xhttp_share_link),
		// unlike the plain vless-reality link above.
		sni := "www.microsoft.com"
		path := "/api"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			sni = preset.Reality.ServerNames[0]
		}
		q := url.Values{}
		q.Set("type", "xhttp")
		q.Set("security", "reality")
		q.Set("sni", sni)
		q.Set("fp", "chrome")
		q.Set("pbk", pubKey)
		q.Set("sid", shortID)
		q.Set("spx", "/")
		q.Set("flow", "xtls-rprx-vision")
		q.Set("mode", "packet-up")
		q.Set("path", path)
		q.Set("host", sni)
		return fmt.Sprintf("vless://%s@%s:%d?%s#vless-reality-xhttp", uuid, ip, port, q.Encode())
	case "xhttp":
		serverName := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.XHTTP != nil && len(preset.XHTTP.Hosts) > 0 {
			serverName = preset.XHTTP.Hosts[0]
		}
		return fmt.Sprintf("vless://%s@%s:%d?type=xhttp&security=none&host=%s&path=%%2Fapi",
			uuid, ip, port, serverName)
	case "vmess":
		// vmess:// + base64(JSON{v,ps,add,port,id,aid,net,type,tls})
		obj := map[string]string{
			"v": "2", "ps": name, "add": ip, "port": fmt.Sprintf("%d", port),
			"id": uuid, "aid": "0", "net": "tcp", "type": "none", "tls": "",
		}
		b, _ := json.Marshal(obj)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "trojan":
		sni := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			sni = preset.Reality.ServerNames[0]
		}
		return fmt.Sprintf("trojan://%s@%s:%d?type=tcp&security=tls&sni=%s#%s",
			privKey, ip, port, sni, name)
	case "shadowsocks", "ss":
		cipher := chain.SS_DEFAULT_CIPHER
		password := privKey
		if password == "" {
			password = chain.GenerateSSPassword(cipher)
		}
		userinfo := base64.StdEncoding.EncodeToString([]byte(cipher + ":" + password))
		return fmt.Sprintf("ss://%s@%s:%d#%s", userinfo, ip, port, name)
	case "hysteria2":
		// Per-node obfs password: every node gets its own random salamander
		// password instead of a single fleet-wide predictable one. If for some
		// reason none was persisted, fall back to a generated value rather than
		// the hardcoded default — but the server side must carry the same value.
		obfsPW := obfsPassword
		if obfsPW == "" {
			obfsPW = chain.GenerateHysteria2ObfsPassword()
		}
		link := fmt.Sprintf("hysteria2://%s@%s:%d?obfs=salamander&obfs-password=%s", uuid, ip, port, obfsPW)
		// insecure=1 disables the client's TLS certificate verification — only
		// appropriate when the server uses a self-signed cert (which we
		// generate for standalone Hysteria2). When a real cert is present the
		// client should verify it, so we do not emit insecure=1 unconditionally.
		if insecure {
			link += "&insecure=1"
		}
		return link
	case "mtproxy":
		fakeDomain := "disk.yandex.ru"
		full, err := chain.MTProxyFullSecret(privKey, fakeDomain)
		if err != nil {
			full = "ee" + privKey
		}
		return fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", ip, port, full)
	default:
		return fmt.Sprintf("# %s config for %s:%d", proto, ip, port)
	}
}

func (s *Server) handleUserQR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "user not found"), http.StatusNotFound)
		return
	}

	var links []string
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		link := buildConnectionLink(c, u)
		links = append(links, link)
	}

	// Also include standalone inbounds assigned to this user.
	nodes, _ := st.ListNodeInfos()
	for _, node := range nodes {
		for _, ib := range node.Inbounds {
			if contains(ib.ForUsers, u.ID) {
				links = append(links, buildStandaloneLink(node.Addr, ib, u))
			}
		}
	}

	s.render(w, r, templates.UserQRView(u, links))
}