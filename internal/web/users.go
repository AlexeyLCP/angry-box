package web

// users.go — user CRUD + client config/link generation (extracted from ui.go as
// part of the M11 split). Includes the client URI / .conf builders used to
// render copy-pasteable configs for a user's assigned chains and standalone
// inbounds.

import (
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListUsers()
	chains, _ := st.ListChains()

	// Auto-deactivate expired users on every view (legacy behaviour kept).
	// P0b Slice 1: also refresh the persisted Status field so the badge is
	// consistent (expired/disabled/on_hold/active).
	now := time.Now()
	for _, u := range users {
		if u.Active && !u.ExpiresAt.IsZero() && now.After(u.ExpiresAt) {
			u.Active = false
			st.SaveUser(u)
		}
		if u.Status == "" {
			u.Status = u.ComputeStatus()
		}
	}

	s.renderContent(w, r, i18n.T(r.Context(), "Users"), templates.Users(users, chains))
}

func (s *Server) handleNewUserForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	chains, _ := st.ListChains()
	s.render(w, r, templates.UserForm(nil, chains, subURLHost(r)))
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
	// Only Telemt (MTProto) imports are offered in the UI — the other protocol
	// secrets (AWG/VLESS/SS/Trojan/VMess/Hysteria2/TUIC) are complex, error-
	// prone, and not a product target. TUIC/Hysteria2 are additionally FROZEN
	// (AGENTS.md #6/#11). Reject a forged POST that sets a non-telemt type on a
	// NEW user (existing users keep their legacy SecretType for display/edit).
	if secretType != "" && secretType != "telemt" {
		http.Error(w, i18n.T(r.Context(), "only Telemt (MTProto) secret imports are supported"), http.StatusBadRequest)
		return
	}
	mtproxyEnabled := r.FormValue("mtproxy_enabled") == "on"
	mtproxySecret := strings.TrimSpace(r.FormValue("mtproxy_secret"))
	mtproxyDomain := strings.TrimSpace(r.FormValue("mtproxy_domain"))
	mtproxyOrderStr := strings.TrimSpace(r.FormValue("mtproxy_order_index"))
	mtproxyNodes := r.Form["mtproxy_nodes"]

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

	// Per-chain exit pins (exit_<chainName>) — the only per-client routing
	// knob on the simplified client form.
	if len(chainNames) > 0 {
		u.ChainExit = map[string]string{}
		for _, cn := range chainNames {
			if exit := strings.TrimSpace(r.FormValue("exit_" + cn)); exit != "" {
				u.ChainExit[cn] = exit
			}
		}
		if len(u.ChainExit) == 0 {
			u.ChainExit = nil
		}
	}

	// Protocols are derived from the selected chains' entry protocols — the
	// operator never picks them (v0.8 simplified client form).
	if derived := deriveProtocolsFromChains(s.store(), u.ChainNames); len(derived) > 0 {
		u.Protocols = derived
	}
	if len(u.Protocols) == 0 {
		u.Protocols = []string{"awg"}
	}

	// MTProxy (Telegram FakeTLS) credentials. Set only when the form enables the
	// MTProxy section or supplies a secret; otherwise the user is not an MTProxy
	// client on any node. A Service with MTProxy.Enabled pre-fills these via
	// applyServiceToUser; the form values override (operator can tweak).
	if mtproxyEnabled || mtproxySecret != "" || u.MTProxySecret != "" {
		if mtproxySecret != "" {
			u.MTProxySecret = mtproxySecret
		}
		if mtproxyDomain == "" {
			mtproxyDomain = u.MTProxyDomain
		}
		if mtproxyDomain == "" {
			mtproxyDomain = "disk.yandex.ru"
		}
		u.MTProxyDomain = mtproxyDomain
		if n, err := strconv.Atoi(mtproxyOrderStr); err == nil && n != 0 {
			u.MTProxyOrderIndex = n
		}
		if len(mtproxyNodes) > 0 {
			u.MTProxyNodes = mtproxyNodes
		}
		// If MTProxy is enabled but no secret anywhere, auto-generate one
		// (removes the blank-by-default footgun — operator no longer has to
		// click the Generate button).
		if u.MTProxySecret == "" && mtproxyEnabled {
			u.MTProxySecret = chain.GenerateMTProxySecret()
		}
	}

	// P0b Slice 1: expiry-strategy + quota fields.
	u.ExpireStrategy = strings.TrimSpace(r.FormValue("expire_strategy"))
	if u.ExpireStrategy == "" {
		u.ExpireStrategy = "never" // default: no expiry (matches legacy behaviour)
	}
	if u.ExpireStrategy == "fixed_date" {
		// expiresAt already parsed above; clear it if strategy is not fixed_date.
	}
	if u.ExpireStrategy != "fixed_date" {
		u.ExpiresAt = time.Time{} // never / start_on_first_use ignore the date
	}
	u.UsageDuration, _ = strconv.ParseInt(r.FormValue("usage_duration"), 10, 64)
	if dlStr := strings.TrimSpace(r.FormValue("activation_deadline")); dlStr != "" {
		if dl, err := time.Parse("2006-01-02", dlStr); err == nil {
			u.ActivationDeadline = dl
		}
	}
	u.DataLimit, _ = strconv.ParseInt(r.FormValue("data_limit"), 10, 64)
	u.DataLimitResetStrategy = strings.TrimSpace(r.FormValue("data_limit_reset_strategy"))

	// Generate per-user credentials for the selected protocols so this user
	// can authenticate to a multi-user inbound with its own identity (the basis
	// for per-client routing). Existing creds are preserved.
	chain.EnsureUserCreds(u)

	st := s.store()

	// Allocate a unique AWG tunnel IP for this user (per-client AWG routing
	// keys on the peer's inner IP). Gather addresses already taken by other
	// users so allocation does not collide.
	if existingAWGIPs, err := takenAWGAddresses(st, u.ID); err == nil {
		chain.EnsureUserAWGAddressPrefix(u, existingAWGIPs, userAWGPrefix(st, u.ChainNames))
	}

	// P0b Slice 1: mint a subscription token at create time (stable identity
	// for the /sub/{token} endpoint). Retry on the astronomically-unlikely
	// collision. Lazily backfilled for legacy users on first sub fetch.
	if u.SubscriptionToken == "" {
		for i := 0; i < 3; i++ {
			t, err := chain.GenerateSubscriptionToken()
			if err != nil {
				break
			}
			if _, err := st.GetUserBySubscriptionToken(t); err != nil {
				u.SubscriptionToken = t // not found = unique
				break
			}
		}
	}

	// Derive the lifecycle status before saving (active/disabled/expired/
	// on_hold; "limited" needs the P0b-2 poller).
	u.Status = u.ComputeStatus()

	if err := st.SaveUser(u); err != nil {
		if strings.Contains(err.Error(), "mtproxy secret already used") {
			http.Error(w, i18n.T(r.Context(), "mtproxy secret already used on node %s"), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "create", "user", u.ID, chain.AuditPayload{"name": u.Name, "protocols": u.Protocols, "service": u.ServiceID}, "operator")
	s.scheduleAutoApplyForUser(st, u, "user create")
	// P0b Slice 1: on create, return the sub URL box so the operator immediately
	// gets the shareable subscription URL (replaces the bare UserRow).
	subURL := subURLHost(r) + "/sub/" + u.SubscriptionToken
	s.render(w, r, templates.UserCreatedResult(u, subURL))
}

func (s *Server) handleEditUserForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, err := s.store().GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	chains, _ := s.store().ListChains()
	s.render(w, r, templates.UserForm(u, chains, subURLHost(r)))
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
	// Only Telemt imports are offered (UI); preserve an existing legacy SecretType
	// when the form sends it back unchanged (the legacy option is disabled and
	// not submitted, so an empty form value here would otherwise WIPE it). A
	// forged POST switching TO a non-telemt type is rejected.
	existingSecretType := u.SecretType
	newSecretType := strings.TrimSpace(r.FormValue("secret_type"))
	if newSecretType == "" {
		// Form cleared it — keep the existing type (legacy edit path; the UI's
		// "None" option is only selectable for new users). This preserves a
		// legacy AWG/etc import that's still in use.
		// EXCEPTION: if the user is being re-saved with no imported secret at
		// all (ImportedSecret empty), dropping the type is correct.
		if u.ImportedSecret == "" {
			u.SecretType = ""
		}
		// else: keep existingSecretType (already on u).
	} else if newSecretType != "telemt" && newSecretType != existingSecretType {
		http.Error(w, i18n.T(r.Context(), "only Telemt (MTProto) secret imports are supported"), http.StatusBadRequest)
		return
	} else {
		u.SecretType = newSecretType
	}
	u.Active = r.FormValue("active") == "on"

	// MTProxy (Telegram FakeTLS) credentials. On edit, the form is authoritative:
	// when the MTProxy section is off (and no secret supplied) we clear the
	// fields so a user that was previously an MTProxy client is removed from the
	// MTProxy inbounds on the next deploy.
	mtproxyEnabled := r.FormValue("mtproxy_enabled") == "on"
	mtproxySecret := strings.TrimSpace(r.FormValue("mtproxy_secret"))
	mtproxyDomain := strings.TrimSpace(r.FormValue("mtproxy_domain"))
	mtproxyOrderStr := strings.TrimSpace(r.FormValue("mtproxy_order_index"))
	mtproxyNodes := r.Form["mtproxy_nodes"]
	if mtproxyEnabled || mtproxySecret != "" {
		u.MTProxySecret = mtproxySecret
		if mtproxyDomain == "" {
			mtproxyDomain = "disk.yandex.ru"
		}
		u.MTProxyDomain = mtproxyDomain
		if n, err := strconv.Atoi(mtproxyOrderStr); err == nil && n != 0 {
			u.MTProxyOrderIndex = n
		}
		u.MTProxyNodes = mtproxyNodes
	} else {
		// MTProxy disabled in edit → clear the fields.
		u.MTProxySecret = ""
		u.MTProxyDomain = ""
		u.MTProxyOrderIndex = 0
		u.MTProxyNodes = nil
	}

	// Per-chain exit pins (exit_<chainName>).
	u.ChainExit = map[string]string{}
	for _, cn := range u.ChainNames {
		if exit := strings.TrimSpace(r.FormValue("exit_" + cn)); exit != "" {
			u.ChainExit[cn] = exit
		}
	}
	if len(u.ChainExit) == 0 {
		u.ChainExit = nil
	}

	// P0b Slice 1: expiry-strategy + quota fields (edit path).
	u.ExpireStrategy = strings.TrimSpace(r.FormValue("expire_strategy"))
	if u.ExpireStrategy == "" {
		u.ExpireStrategy = "never"
	}
	if u.ExpireStrategy == "fixed_date" {
		if expiryStr := strings.TrimSpace(r.FormValue("expires_at")); expiryStr != "" {
			u.ExpiresAt, _ = time.Parse("2006-01-02", expiryStr)
		} else {
			u.ExpiresAt = time.Time{}
		}
	} else {
		u.ExpiresAt = time.Time{} // never / start_on_first_use ignore the date
	}
	u.UsageDuration, _ = strconv.ParseInt(r.FormValue("usage_duration"), 10, 64)
	if dlStr := strings.TrimSpace(r.FormValue("activation_deadline")); dlStr != "" {
		if dl, err := time.Parse("2006-01-02", dlStr); err == nil {
			u.ActivationDeadline = dl
		}
	} else {
		u.ActivationDeadline = time.Time{}
	}
	u.DataLimit, _ = strconv.ParseInt(r.FormValue("data_limit"), 10, 64)
	u.DataLimitResetStrategy = strings.TrimSpace(r.FormValue("data_limit_reset_strategy"))

	// Protocols are derived from the selected chains' entry protocols (v0.8).
	if derived := deriveProtocolsFromChains(st, u.ChainNames); len(derived) > 0 {
		u.Protocols = derived
	}
	if len(u.Protocols) == 0 {
		u.Protocols = []string{"awg"}
	}

	// Fill any per-user credentials that are now missing because a new protocol
	// was added to this user's Protocols list. Existing creds are preserved.
	chain.EnsureUserCreds(u)

	// Allocate a unique AWG tunnel IP if AWG was just added and the user has
	// none yet. Avoid colliding with other users' addresses.
	if existingAWGIPs, err := takenAWGAddresses(st, u.ID); err == nil {
		chain.EnsureUserAWGAddressPrefix(u, existingAWGIPs, userAWGPrefix(st, u.ChainNames))
	}

	// P0b Slice 1: re-mint a sub token if the operator cleared it (stable
	// identity otherwise — do not rotate on every save). Derive status.
	if u.SubscriptionToken == "" {
		for i := 0; i < 3; i++ {
			t, err := chain.GenerateSubscriptionToken()
			if err != nil {
				break
			}
			if _, err := st.GetUserBySubscriptionToken(t); err != nil {
				u.SubscriptionToken = t
				break
			}
		}
	}
	u.Status = u.ComputeStatus()

	if err := st.SaveUser(u); err != nil {
		if strings.Contains(err.Error(), "mtproxy secret already used") {
			http.Error(w, i18n.T(r.Context(), "mtproxy secret already used on node %s"), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
		return
	}
	chain.WriteAudit(st, "update", "user", u.ID, chain.AuditPayload{"name": u.Name, "protocols": u.Protocols, "service": u.ServiceID}, "operator")
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
	// Standalone inbounds: any node whose inbound ForUsers lists this user gets
	// re-deployed too (e.g. multi-peer standalone AWG picks up the user's creds).
	nodes, _ := st.ListNodeInfos()
	for _, node := range nodes {
		for _, ib := range node.Inbounds {
			if ib.Protocol == "naive" || ib.Protocol == "mieru" || ib.Protocol == "trusttunnel" || contains(ib.ForUsers, u.ID) {
				chain.ScheduleAutoApply(node.ID, reason+":standalone")
				break // one schedule per node is enough
			}
		}
	}

	// MTProxy nodes: redeploy every node this user is an MTProxy client on.
	// (On update/delete, nodes it used to be on still appear in u.MTProxyNodes
	// because the caller passes the user it just loaded.)
	for _, n := range u.MTProxyNodes {
		chain.ScheduleAutoApply(n, reason+":mtproxy")
	}
}

// takenAWGAddresses returns the AWG tunnel IPs already claimed by other users
// (everyone except the user with excludeID). Used to allocate a collision-free
// per-user AWGAddress. Errors from ListUsers are propagated (caller skips
// allocation on error).
func takenAWGAddresses(st *chain.Store, excludeID string) ([]string, error) {
	users, err := st.ListUsers()
	if err != nil {
		return nil, err
	}
	var taken []string
	for _, u := range users {
		if u.ID == excludeID {
			continue
		}
		if u.AWGAddress != "" {
			taken = append(taken, u.AWGAddress)
		}
	}
	return taken, nil
}

func (s *Server) handleUserConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	u, err := st.GetUser(id)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "user not found"), http.StatusNotFound)
		return
	}
	persistUserCreds(st, u)

	// Generate configs for user's assigned chains
	var configs []templates.UserChainConfig
	for _, chainName := range u.ChainNames {
		c, err := st.GetChain(chainName)
		if err != nil {
			continue
		}
		// Build a config link for this chain
		link := buildConnectionLink(st, c, u, strings.TrimSpace(r.URL.Query().Get("awg_version")))
		configs = append(configs, templates.UserChainConfig{
			ChainName:   chainName,
			Protocol:    string(c.UserProtocol),
			ConfigLink:  link,
			Description: fmt.Sprintf(i18n.T(r.Context(), "%s chain — %d hops, strategy: %s"), chainName, len(c.Nodes), c.Strategy),
		})
	}

	// Generate configs for standalone inbounds assigned to this user
	nodes, _ := st.ListNodeInfos()
	ensureStandaloneAWGMaterial(st, nodes)
	for _, node := range nodes {
		for i, ib := range node.Inbounds {
			if chain.IsChainSourcedInbound(&ib) {
				continue // chain-entry materialization — served via the chain link above
			}
			if ib.Protocol == "naive" || ib.Protocol == "mieru" || ib.Protocol == "trusttunnel" || contains(ib.ForUsers, u.ID) {
				link := buildStandaloneLinkFor(node, i, u)
				configs = append(configs, templates.UserChainConfig{
					ChainName:   "node: " + node.ID,
					Protocol:    ib.Protocol,
					ConfigLink:  link,
					Description: fmt.Sprintf(i18n.T(r.Context(), "Standalone inbound on %s (port %d)"), node.ID, ib.Port),
				})
			}
		}
	}

	// MTProxy client links: one per node in u.MTProxyNodes that this user is an
	// MTProxy client on. Each becomes a tg://proxy?server=...&port=...&secret=...
	// entry using the node's mtproxy inbound port (443 if no mtproxy inbound is
	// configured on that node yet) and the user's full ("ee"+hex) secret.
	if u.MTProxySecret != "" && len(u.MTProxyNodes) > 0 {
		for _, nodeID := range u.MTProxyNodes {
			host, err := st.GetHost(nodeID)
			if err != nil {
				continue
			}
			addr := host.Addr
			if i := strings.Index(addr, ":"); i > 0 {
				addr = addr[:i] // strip the SSH port — MTProxy listens on its own port
			}
			port := 443
			// Prefer the port of an existing mtproxy inbound on this node.
			if info, err := st.GetNodeInfo(nodeID); err == nil {
				for _, ib := range info.Inbounds {
					if ib.Protocol == "mtproxy" && ib.Port > 0 {
						port = ib.Port
						break
					}
				}
			}
			fullSecret, err := chain.MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)
			if err != nil {
				continue
			}
			link := fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", addr, port, fullSecret)
			configs = append(configs, templates.UserChainConfig{
				ChainName:   "mtproxy: " + nodeID,
				Protocol:    "mtproxy",
				ConfigLink:  link,
				Description: fmt.Sprintf(i18n.T(r.Context(), "MTProxy on %s (port %d)"), nodeID, port),
			})
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

func buildConnectionLink(st *chain.Store, c *model.Chain, u *model.User, clampVersion ...string) string {
	nodes := c.AllNodes()
	if len(nodes) == 0 {
		return "# no nodes in chain"
	}
	entry := nodes[0]
	proto := string(c.UserProtocol)
	if proto == "" {
		proto = "awg"
	}

	// AWG chains render a per-user awg-quick .conf (each user is their own
	// WireGuard peer; per-client routing keys on the peer's inner source IP).
	if c.UserProtocol == model.UserProtocolAWG {
		awgVer := ""
		if len(clampVersion) > 0 {
			awgVer = clampVersion[0]
		}
		conf, err := chain.RenderClientAWGConf(chain.ClientConfigParams{
			Chain:      c,
			User:       u,
			AWGVersion: awgVer,
			// v2: resolve the selected entry's materialized inbound (profile
			// credentials) — chain-level AWG creds are empty on v2 chains.
			EntryInboundResolver: func(nodeID, profileID string) *model.NodeInbound {
				if st == nil {
					return nil
				}
				return st.ProfileInboundOn(nodeID, profileID)
			},
		})
		if err != nil {
			return "# " + err.Error()
		}
		return conf
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


// ensureStandaloneAWGMaterial lazily generates and persists obfs material on
// standalone AWG inbounds that predate the persistence (v0.6.x): the deploy
// path ensures it in the applier, but a client-conf render can happen before
// the next deploy — without this the render would fall back to the preset's
// degenerate H ranges and diverge from the server after the deploy ensures.
// Best-effort: render proceeds even if the save fails.
func ensureStandaloneAWGMaterial(st *chain.Store, nodes []*model.NodeInfo) {
	for _, node := range nodes {
		changed := false
		for i := range node.Inbounds {
			ib := &node.Inbounds[i]
			if ib.Protocol != "awg" {
				continue
			}
			preset := chain.ResolveStandaloneAWGPreset(ib)
			before := ib.AWGCPSI1 + ib.AWGH1
			// Profile-backed inbounds take the profile's shared material (e.g.
			// a live-captured signature); ad-hoc ones use the per-node ensure.
			var prof *model.InboundProfile
			if ib.ProfileID != "" {
				if p, err := st.GetInboundProfile(ib.ProfileID); err == nil {
					prof = p
				}
			}
			chain.ApplyProfileMaterialToInbound(ib, prof, preset)
			if ib.AWGCPSI1+ib.AWGH1 != before {
				changed = true
			}
		}
		if changed {
			_ = st.SaveNodeInfo(node)
		}
	}
}

// buildStandaloneLinkFor renders the client link for the index-th standalone
// inbound of a node. On caddy-mode nodes the TLS-utility protocols
// (naive/trusttunnel) are fronted by the SNI router, so the link points at the
// inbound's SNI subdomain on 443 with the SNI set to that subdomain — not at
// the raw node IP (which no longer listens publicly for them).
func buildStandaloneLinkFor(node *model.NodeInfo, index int, u *model.User) string {
	ib := node.Inbounds[index]
	if hosts := chain.CaddyFrontedHosts(node); hosts != nil {
		if host, ok := hosts[index]; ok {
			user, pass := "", ""
			if u != nil {
				user, pass = u.NaiveUsername, u.NaivePassword
				if ib.Protocol == "trusttunnel" {
					user, pass = u.TrustTunnelUsername, u.TrustTunnelPassword
				}
			}
			switch ib.Protocol {
			case "naive":
				return fmt.Sprintf("naive+https://%s:%s@%s:443?padding=true&sni=%s#%s",
					url.QueryEscape(user), url.QueryEscape(pass), host, url.QueryEscape(host), ib.Protocol)
			case "trusttunnel":
				return fmt.Sprintf("tt://%s:%s@%s:443?security=tls&sni=%s&alpn=h2#%s",
					url.QueryEscape(user), url.QueryEscape(pass), host, url.QueryEscape(host), ib.Protocol)
			}
		}
	}
	return buildStandaloneLink(node.Addr, ib, u)
}

func buildStandaloneLink(addr string, ib model.NodeInbound, u *model.User) string {
	ip := strings.Split(addr, ":")[0]
	if ib.Protocol == "awg" {
		priv := ""
		addrIP := ""
		psk := ""
		if u != nil {
			priv = u.AWGPrivateKey
			addrIP = u.AWGAddress
			psk = u.AWGPresharedKey
			if priv == "" && u.ImportedSecret != "" && u.SecretType == "awg" {
				priv = u.ImportedSecret
			}
		}
		if priv == "" {
			priv = ib.AWGClientPriv
		}
		if priv == "" {
			return buildClientURI("awg", ip, ib.Port, "", "", ib.ServerPubKey, "", ib.Protocol, u, "", false)
		}
		return buildAWGClientConf(ip, ib.Port, priv, ib.ServerPubKey, ib.AWGClientPub, "", addrIP, &ib, psk)
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
// hostOverride lets callers swap the domain/IP for the Endpoint line. address is
// the client's tunnel IP (its peer AllowedIPs on the server); empty falls back
// to the legacy single-client "10.8.0.2/24". For per-user (chain) configs the
// caller passes the user's AWGAddress so each client gets a unique inner IP —
// this is what source_ip_cidr per-client routing matches on. ib, when non-nil,
// is the standalone AWG inbound the conf belongs to: its preset + persisted
// obfs material (proper quadrant H1-H4 + stable CPS I1-I5) are rendered so the
// client matches the server exactly. nil = legacy fallback (default preset,
// fresh material — imported-secret paths without an inbound context).
func buildAWGClientConf(ip string, port int, clientPriv, serverPub, clientPub, hostOverride, address string, ib *model.NodeInbound, psk string) string {
	host := hostOverride
	if host == "" {
		host = ip
	}
	if address == "" {
		address = "10.8.0.2/24"
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString(fmt.Sprintf("Address = %s\n", address))
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", clientPriv))
	b.WriteString("MTU = 1420\n")
	// Amnezia params belong in [Interface] (BEFORE [Peer]) — awg-quick passes
	// the stripped config to `awg setconf`, which parses amnezia fields only
	// within [Interface]; after [Peer] setconf fails with "Line unrecognized".
	version := model.AWGVersion2
	if ib != nil {
		version = ib.EffectiveAWGVersion()
	}
	var amn *config.AmneziaOptions
	var material *chain.AWGObfsMaterial
	if ib != nil {
		material = chain.InboundAWGObfsMaterial(ib)
		preset := chain.ResolveStandaloneAWGPreset(ib)
		if preset.AWG != nil {
			amn = chain.BuildAWGAmnezia(preset.AWG, &preset, material)
		}
	} else if preset := chain.GetDefaultPreset(); preset.AWG != nil {
		amn = chain.BuildAWGAmnezia(preset.AWG, &preset, nil)
	}
	if amn != nil {
		b.WriteString(fmt.Sprintf("Jc = %d\n", amn.JC))
		b.WriteString(fmt.Sprintf("Jmin = %d\n", amn.JMIN))
		b.WriteString(fmt.Sprintf("Jmax = %d\n", amn.JMAX))
		b.WriteString(fmt.Sprintf("S1 = %d\n", amn.S1))
		b.WriteString(fmt.Sprintf("S2 = %d\n", amn.S2))
		// S3/S4 + I1-I5 are AWG 2.0+ (CPS); a 1.5 client must not receive them.
		if model.AWGVersionAtLeast(version, model.AWGVersion2) {
			b.WriteString(fmt.Sprintf("S3 = %d\n", amn.S3))
			b.WriteString(fmt.Sprintf("S4 = %d\n", amn.S4))
		}
		b.WriteString(fmt.Sprintf("H1 = %s\n", amn.H1))
		b.WriteString(fmt.Sprintf("H2 = %s\n", amn.H2))
		b.WriteString(fmt.Sprintf("H3 = %s\n", amn.H3))
		b.WriteString(fmt.Sprintf("H4 = %s\n", amn.H4))
		if amn.I1 != "" && model.AWGVersionAtLeast(version, model.AWGVersion2) {
			b.WriteString(fmt.Sprintf("I1 = %s\n", amn.I1))
			b.WriteString(fmt.Sprintf("I2 = %s\n", amn.I2))
			b.WriteString(fmt.Sprintf("I3 = %s\n", amn.I3))
			b.WriteString(fmt.Sprintf("I4 = %s\n", amn.I4))
			b.WriteString(fmt.Sprintf("I5 = %s\n", amn.I5))
		}
		// Itime intentionally omitted — awg setconf and sing-box-extended
		// endpoint both reject it; the default cache lifetime works.
	}
	// AWG 3.0 header protection (HPK/CPM/RAT) inline in [Interface] — the
	// AmneziaWG client apps parse them natively. Previously this standalone
	// builder omitted them entirely (a v3 inbound's client got no HPK).
	if material != nil && material.AWG3Mode && material.HeaderProtectionKey != "" &&
		model.AWGVersionAtLeast(version, model.AWGVersion3) {
		b.WriteString(fmt.Sprintf("HeaderProtectionKey = %s\n", material.HeaderProtectionKey))
		if material.ContentPaddingAddition != "" {
			b.WriteString(fmt.Sprintf("ContentPaddingAddition = %s\n", material.ContentPaddingAddition))
		}
		if material.RekeyAfterTime != "" {
			b.WriteString(fmt.Sprintf("RekeyAfterTime = %s\n", material.RekeyAfterTime))
		}
		if material.RandomTrailers {
			b.WriteString("RandomTrailers = on\n")
		}
		if material.DisableCookies {
			b.WriteString("DisableCookies = on\n")
		}
	}
	b.WriteString("\n[Peer]\n")
	b.WriteString(fmt.Sprintf("PublicKey = %s\n", serverPub))
	if psk != "" {
		b.WriteString(fmt.Sprintf("PresharedKey = %s\n", psk))
	}
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	b.WriteString(fmt.Sprintf("Endpoint = %s:%d\n", host, port))
	b.WriteString("PersistentKeepalive = 25\n")
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
			return buildAWGClientConf(ip, port, u.ImportedSecret, pubKey, "", "", "", nil, u.AWGPresharedKey)
		}
		psk := ""
		if u != nil {
			psk = u.AWGPresharedKey
		}
		return fmt.Sprintf("awg://%s:%d?pub=%s&psk=%s&mtu=1420", ip, port, pubKey, url.QueryEscape(psk))
	case "naive":
		user, pass := "", ""
		if u != nil {
			user, pass = u.NaiveUsername, u.NaivePassword
		}
		sni := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			sni = preset.Reality.ServerNames[0]
		}
		return fmt.Sprintf("naive+https://%s:%s@%s:%d?padding=true&sni=%s#%s",
			url.QueryEscape(user), url.QueryEscape(pass), ip, port, url.QueryEscape(sni), name)
	case "mieru":
		user, pass := "", ""
		if u != nil {
			user, pass = u.MieruUsername, u.MieruPassword
		}
		return fmt.Sprintf("mierus://%s:%s@%s:%d?transport=TCP#%s",
			url.QueryEscape(user), url.QueryEscape(pass), ip, port, name)
	case "trusttunnel":
		user, pass := "", ""
		if u != nil {
			user, pass = u.TrustTunnelUsername, u.TrustTunnelPassword
		}
		sni := "www.microsoft.com"
		if preset := chain.GetDefaultPreset(); preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
			sni = preset.Reality.ServerNames[0]
		}
		return fmt.Sprintf("tt://%s:%s@%s:%d?security=tls&sni=%s&alpn=h2#%s",
			url.QueryEscape(user), url.QueryEscape(pass), ip, port, url.QueryEscape(sni), name)
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
	// Reuse the shared collectUserLinks (subscription.go) so QR shows the same
	// link set as the subscription endpoint + config view (includes MTProxy).
	links := s.collectUserLinks(u, st)
	s.render(w, r, templates.UserQRView(u, links))
}

// handleGenerateMTProxySecret renders an <input> prefilled with a fresh 32-hex
// MTProxy secret. HTMX swaps the empty secret field with this fragment.
func (s *Server) handleGenerateMTProxySecret(w http.ResponseWriter, r *http.Request) {
	secret := chain.GenerateMTProxySecret()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<input type="text" name="mtproxy_secret" class="input input-bordered join-item font-mono" value="%s" maxlength="32" />`, secret)
}
// ── Bulk user creation (seller workflow) ─────────────────────────────────────

func (s *Server) handleBulkUserForm(w http.ResponseWriter, r *http.Request) {
	chains, _ := s.store().ListChains()
	s.render(w, r, templates.BulkUserForm(chains))
}

// handleBulkCreateUsers creates N users from one template (name prefix,
// chains, expiry days, data limit). Names/IDs get a zero-padded ordinal + a
// short random suffix so repeated bulk runs never collide. Every user gets the
// full create treatment: derived protocols, per-user creds, AWG tunnel IP,
// subscription token, audit entry + auto-apply scheduling.
func (s *Server) handleBulkCreateUsers(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	st := s.store()

	count, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("count")))
	if count < 1 || count > 100 {
		s.render(w, r, templates.BulkUserResult(0, "", i18n.T(r.Context(), "Count must be 1..100")))
		return
	}
	prefix := strings.TrimSpace(r.FormValue("name_prefix"))
	if prefix == "" {
		s.render(w, r, templates.BulkUserResult(0, "", i18n.T(r.Context(), "name required")))
		return
	}
	expDays, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("expires_days")))
	dataLimitGB, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("data_limit_gb")), 10, 64)

	var chainNames []string
	for _, cn := range r.Form["chain_names"] {
		if cn = strings.TrimSpace(cn); cn != "" {
			chainNames = append(chainNames, cn)
		}
	}

	var expiresAt time.Time
	if expDays > 0 {
		expiresAt = time.Now().AddDate(0, 0, expDays)
	}

	created := 0
	for i := 1; i <= count; i++ {
		suffix := bulkSuffix()
		u := &model.User{
			ID:         fmt.Sprintf("%s-%02d-%s", prefix, i, suffix),
			Name:       fmt.Sprintf("%s-%02d", prefix, i),
			Active:     true,
			ChainNames: chainNames,
			ExpiresAt:  expiresAt,
			DataLimit:  dataLimitGB * 1024 * 1024 * 1024,
			CreatedAt:  time.Now(),
		}
		if len(chainNames) > 0 {
			if derived := deriveProtocolsFromChains(st, chainNames); len(derived) > 0 {
				u.Protocols = derived
			}
		}
		if len(u.Protocols) == 0 {
			u.Protocols = []string{"awg"}
		}
		u.ExpireStrategy = "never"
		if !expiresAt.IsZero() {
			u.ExpireStrategy = "fixed_date"
		}
		chain.EnsureUserCreds(u)
		if taken, err := takenAWGAddresses(st, u.ID); err == nil {
			chain.EnsureUserAWGAddressPrefix(u, taken, userAWGPrefix(st, u.ChainNames))
		}
		if tok, err := chain.GenerateSubscriptionToken(); err == nil {
			u.SubscriptionToken = tok
		}
		u.Status = u.ComputeStatus()
		if err := st.SaveUser(u); err != nil {
			continue
		}
		chain.WriteAudit(st, "bulk-create", "user", u.ID, chain.AuditPayload{"name": u.Name, "prefix": prefix}, "operator")
		s.scheduleAutoApplyForUser(st, u, "bulk user create")
		created++
	}
	if created == 0 {
		s.render(w, r, templates.BulkUserResult(0, prefix, i18n.T(r.Context(), "no users were created (check chains/store)")))
		return
	}
	s.render(w, r, templates.BulkUserResult(created, prefix, ""))
}

// bulkSuffix returns 4 hex chars of randomness for collision-proof bulk IDs.
func bulkSuffix() string {
	buf := make([]byte, 2)
	_, _ = cryptoRand.Read(buf)
	return hex.EncodeToString(buf)
}

// registerUserRoutes wires every user-scoped route (CRUD + config + qr + the
// MTProxy secret generator). The legacy /ui/users GET redirects to the unified
// /ui/clients page. The /ui/qr-image route is in qr.go. CTO-review §4.
func (s *Server) registerUserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/users", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/clients", http.StatusMovedPermanently)
	})
	mux.HandleFunc("POST /ui/users", s.auth(s.handleCreateUser))
	mux.HandleFunc("GET /ui/users/new", s.auth(s.handleNewUserForm))
	mux.HandleFunc("GET /ui/users/{id}/edit", s.auth(s.handleEditUserForm))
	mux.HandleFunc("POST /ui/users/{id}/edit", s.auth(s.handleUpdateUser))
	mux.HandleFunc("DELETE /ui/users/{id}", s.auth(s.handleDeleteUser))
	mux.HandleFunc("GET /ui/users/{id}/config", s.auth(s.handleUserConfig))
	mux.HandleFunc("GET /ui/users/{id}/qr", s.auth(s.handleUserQR))
	mux.HandleFunc("POST /ui/users/generate-mtproxy-secret", s.auth(s.handleGenerateMTProxySecret))
	mux.HandleFunc("GET /ui/users/bulk", s.auth(s.handleBulkUserForm))
	mux.HandleFunc("POST /ui/users/bulk", s.auth(s.handleBulkCreateUsers))
}

// ── P0b Slice 1 helpers: Service expansion + subscription URL host ─────────────

// subURLHost returns the scheme://host[:port] for the subscription URL shown
// post-create. Prefers X-Forwarded-Host (reverse-proxy), then r.Host, then the
// server's listen addr. Scheme is https when TLS is configured (caller-side),
// otherwise http — kept simple here (the /sub/{token} endpoint itself is
// scheme-agnostic; this is only the displayed copy string).
func subURLHost(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "localhost:9080"
	}
	return "http://" + host
}

// userAWGPrefix returns the "10.8.X" prefix of the entry inbound subnet for
// the user's first AWG chain (so the peer IP lands in the same /24 as the
// interface that accepts it). Defaults to "10.8.0" (chain-entry convention)
// for legacy chains / non-AWG entries / missing materialization.
func userAWGPrefix(st *chain.Store, chainNames []string) string {
	for _, cn := range chainNames {
		c, err := st.GetChain(cn)
		if err != nil || !c.IsLevelized() || len(c.Levels) == 0 {
			continue
		}
		for _, n := range c.Levels[0].Nodes {
			if n.InboundRef == "" {
				continue
			}
			ib := st.ProfileInboundOn(n.ID, n.InboundRef)
			if ib == nil || ib.AWGServerAddress == "" {
				continue
			}
			a := ib.AWGServerAddress
			if i := strings.IndexByte(a, '/'); i >= 0 {
				a = a[:i]
			}
			parts := strings.Split(a, ".")
			if len(parts) == 4 {
				return strings.Join(parts[:3], ".")
			}
		}
	}
	return "10.8.0"
}

// deriveProtocolsFromChains returns the distinct user-entry protocols of the
// given chains (v0.8: a client's protocols are implied by its chains, never
// picked manually).
func deriveProtocolsFromChains(st *chain.Store, chainNames []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, cn := range chainNames {
		c, err := st.GetChain(cn)
		if err != nil {
			continue
		}
		proto := string(c.UserProtocol)
		if proto == "" {
			proto = "awg"
		}
		if !seen[proto] {
			seen[proto] = true
			out = append(out, proto)
		}
	}
	return out
}
