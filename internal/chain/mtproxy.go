package chain

// mtproxy.go — Telegram MTProxy (FakeTLS) inbound renderer for sing-box-extended.
// Only the patched sing-box-extended build supports type:"mtproxy" (with_mtproxy
// build tag); vanilla sing-box rejects it. The extended secret format is
// "ee" + secret_hex + hex(fake_tls_domain), assembled by MTProxyFullSecret
// (cryptogen.go). Per-client routing is by auth_user (sing-box sets
// metadata.User = streamContext.SecretName() = the user's Name), not source IP.
//
// Field reference: VPN/docs/sing-box-extended.md (MTProxy inbound options) and
// VPN/orchestrator/app/templates/mtproxy_server.json.j2 (the reference config).

import (
	"encoding/json"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// buildMTProxyInbound renders a sing-box MTProxy inbound from the node's MTProxy
// users. Each active user becomes a users[] entry with the extended "ee"+hex
// secret. Returns nil (no inbound) when there are no active users with a
// non-empty secret — a node with no MTProxy users should not emit an empty
// inbound (sing-box rejects an mtproxy inbound with an empty users[]).
func buildMTProxyInbound(port int, tag string, users []*model.User) json.RawMessage {
	mtUsers := make([]config.MTProxyUser, 0, len(users))
	for _, u := range users {
		if !u.Active {
			continue
		}
		secret, err := MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)
		if err != nil {
			// A malformed secret would make sing-box reject the whole config.
			// Skip the user rather than fail the deploy — the operator can fix
			// the secret via the UI and re-apply.
			continue
		}
		name := u.Name
		if name == "" {
			name = u.ID
		}
		mtUsers = append(mtUsers, config.MTProxyUser{Name: name, Secret: secret})
	}
	if len(mtUsers) == 0 {
		return nil
	}
	inb := config.MTProxyInbound{
		Type:                        "mtproxy",
		Tag:                         tag,
		Listen:                      "0.0.0.0",
		ListenPort:                  port,
		Concurrency:                 8192,
		DomainFrontingPort:          443,
		DomainFrontingProxyProtocol: false,
		PreferIP:                    "prefer-ipv4",
		AutoUpdate:                  true,
		AllowFallbackOnUnknownDC:    false,
		TolerateTimeSkewness:        "3s",
		IdleTimeout:                 "5m",
		HandshakeTimeout:            "10s",
		Users:                       mtUsers,
	}
	data, _ := json.Marshal(inb)
	return data
}

// mtproxyUsersForNode filters users down to active ones with a non-empty
// MTProxySecret. Returns nil when none qualify (so the caller skips
// emitting an inbound). Used by the deploy path to feed buildMTProxyInbound.
func mtproxyUsersForNode(users []*model.User) []*model.User {
	if len(users) == 0 {
		return nil
	}
	out := make([]*model.User, 0, len(users))
	for _, u := range users {
		if u.Active && u.MTProxySecret != "" {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mtproxyInboundPort picks the listen port for an MTProxy inbound: the
// NodeInbound.Port when set, else 443 (MTProxy's canonical FakeTLS port — it
// must look like HTTPS to DPI).
func mtproxyInboundPort(port int) int {
	if port > 0 {
		return port
	}
	return 443
}
