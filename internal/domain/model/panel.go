package model

import (
	"encoding/json"
	"time"
)

// User represents a proxy user with protocol preferences and optional expiry.
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Telegram  string    `json:"telegram,omitempty"`
	Email     string    `json:"email,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Active    bool      `json:"active"`

	// Protocol preferences — which protocols this user gets configs for.
	Protocols []string `json:"protocols,omitempty"`

	// ImportedSecret holds an external WireGuard/TUIC/VLESS key for migration.
	ImportedSecret string `json:"imported_secret,omitempty"`
	SecretType     string `json:"secret_type,omitempty"` // "awg", "tuic", "vless-reality"

	// Chain assignments — which chains this user has access to.
	ChainNames []string `json:"chain_names,omitempty"`

	// Per-user protocol credentials (generated once at user-create, persisted,
	// stable across applies; rotated only explicitly). These let each user
	// authenticate to a multi-user inbound with their own identity, which is the
	// basis for per-client routing. Empty fields = legacy behavior (the user
	// falls back to the chain-wide / inbound-shared credentials).
	//
	// TUIC/VLESS use an auth_user identity (UUID/Name) matched by sing-box route
	// rules. AWG (AmneziaWG) is a WireGuard L3 tunnel — it has no auth_user;
	// each user is a distinct peer identified by a PublicKey + a unique tunnel
	// IP (AWGAddress). The tunnel IP doubles as the route-rule source IP
	// (source_ip_cidr), so per-client routing for AWG keys on the peer's inner
	// address, not on an auth_user string.
	VLESSUUID    string `json:"vless_uuid,omitempty"`
	TUICUUID     string `json:"tuic_uuid,omitempty"`
	TUICPassword string `json:"tuic_password,omitempty"`
	// Hysteria2Password is the per-user Hysteria2 password (the inbound's users
	// array carries password-based auth, no separate UUID).
	Hysteria2Password string `json:"hysteria2_password,omitempty"`
	// AWG per-user peer credentials. AWGPrivateKey is the client's WireGuard
	// private key (rendered into the per-user awg-quick .conf). AWGPublicKey is
	// the corresponding public key — it goes into the server endpoint's Peers[]
	// so the server accepts this user's handshakes. AWGAddress is the user's
	// unique tunnel IP (e.g. "10.8.0.3/32"); it is both the peer's AllowedIPs
	// on the server and the source_ip_cidr used by per-client route rules.
	AWGPrivateKey string `json:"awg_private_key,omitempty"`
	AWGPublicKey  string `json:"awg_public_key,omitempty"`
	AWGAddress    string `json:"awg_address,omitempty"`

	// MTProxy (Telegram FakeTLS) credentials. Optional — set when the user is
	// also an MTProxy client. Empty MTProxySecret = user is not an MTProxy
	// client on any node. MTProxyNodes lists the node IDs this user is an
	// MTProxy client on (replaces the old per-node MtproxyUser.NodeID).
	MTProxySecret     string   `json:"mtproxy_secret,omitempty"` // 32 hex chars (16 random bytes)
	MTProxyDomain     string   `json:"mtproxy_domain,omitempty"` // FakeTLS SNI, default "disk.yandex.ru"
	MTProxyOrderIndex int      `json:"mtproxy_order_index,omitempty"`
	MTProxyNodes      []string `json:"mtproxy_nodes,omitempty"` // node IDs this user is an MTProxy client on

	// ChainExit optionally pins a user to a specific exit node per chain. The
	// map key is the chain name; the value is the ChainNode.ID of the exit.
	// When set and the chain's route section is enabled (AB_ROUTE_DNS=1), a
	// per-user route rule steers that user's traffic to the chosen exit's
	// outbound: for TUIC/VLESS via auth_user, for AWG via source_ip_cidr (the
	// peer's tunnel IP). Empty map = use the chain's default exit (last node).
	ChainExit map[string]string `json:"chain_exit,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// IsExpired returns true if the user has a non-zero expiry before now.
func (u *User) IsExpired() bool {
	return !u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt)
}

// PanelSettings holds global panel configuration.
type PanelSettings struct {
	AdminPasswordHash       string            `json:"admin_password_hash"`
	PanelCountry            string            `json:"panel_country,omitempty"`    // e.g. "RU", "IR", "CN"
	Language                string            `json:"language,omitempty"`         // e.g. "en", "ru"
	MetricsInterval         int               `json:"metrics_interval,omitempty"` // minutes, default 15 minutes
	SSHKeys                 []SSHKeyEntry     `json:"ssh_keys,omitempty"`
	DefaultProtocol         string            `json:"default_protocol,omitempty"`           // "awg", "tuic", "vless-reality"
	CustomPresets           json.RawMessage   `json:"custom_presets,omitempty"`             // user-created obfuscation presets (JSON array of ConnectionPreset)
	DefaultPresetByProtocol map[string]string `json:"default_preset_by_protocol,omitempty"` // optional per-protocol default override
	DefaultSSHKeyID         string            `json:"default_ssh_key_id,omitempty"`
}

// Source of an SSHKeyEntry. Stored as a JSON string (no iota) so the registry
// stays human-readable and forward-compatible.
const (
	SourceStored = "stored" // user-pasted PEM, lives in KeyData
	SourceSystem = "system" // auto-detected from ~/.ssh/, KeyPath is the file
	SourceAuto   = "auto"   // generated by the capture wizard auto-install
	SourceManual = "manual" // pasted via the wizard "paste manually" path
)

// SSHKeyEntry is an SSH key stored in the panel.
type SSHKeyEntry struct {
	ID          string `json:"id"`                    // unique identifier
	Name        string `json:"name"`                  // display name
	KeyPath     string `json:"key_path,omitempty"`    // filesystem path (system keys)
	KeyData     string `json:"key_data,omitempty"`    // private key content (user/manual keys)
	Source      string `json:"source"`                // "stored", "system", "auto", "manual"
	Fingerprint string `json:"fingerprint,omitempty"` // last 8 of SHA256 pubkey fingerprint
}

// NodeMetrics holds the latest health/metrics snapshot for a node.
type NodeMetrics struct {
	HostID      string    `json:"host_id"`
	Online      bool      `json:"online"`
	Version     string    `json:"version,omitempty"`
	LatencyMs   int64     `json:"latency_ms,omitempty"`
	BytesSent   int64     `json:"bytes_sent,omitempty"`
	BytesRecv   int64     `json:"bytes_recv,omitempty"`
	LastChecked time.Time `json:"last_checked"`

	OS                 string `json:"os,omitempty"`
	SingBoxInstalled   bool   `json:"sing_box_installed,omitempty"`
	AWGModuleInstalled bool   `json:"awg_module_installed,omitempty"`
}

// NodeInfo enriches a Host with metadata for the web UI (country, bandwidth, inbounds).
type NodeInfo struct {
	Host

	Country   string `json:"country,omitempty"`
	Bandwidth string `json:"bandwidth,omitempty"` // human-readable: "100 Mbps", "1 Gbps"
	Source    string `json:"source,omitempty"`    // "ssh_key", "password", "captured"

	// AutoApply enables background SSH deploy when a client/inbound on this
	// node is created/updated/deleted (hybrid mode — structural changes still
	// need an explicit Apply). Mirrors the Python project's per-resource
	// auto_apply_on behaviour.
	AutoApply bool `json:"auto_apply,omitempty"`

	// UseSudo wraps privileged remote commands in sudo (for non-root SSH users
	// with passwordless sudo configured on the VPS).
	UseSudo bool `json:"use_sudo,omitempty"`

	// Deploy-status tracking: sha256 hex of the last successfully-applied
	// rendered config + when. hasPendingChanges = never deployed OR current
	// render hash differs from LastDeployedHash.
	LastDeployedHash string    `json:"last_deployed_hash,omitempty"`
	LastDeployedAt   time.Time `json:"last_deployed_at,omitempty"`

	// Spider Web persistent layout: x/y coordinates saved on node drag so the
	// graph layout survives reloads. (0,0) means "no saved position → use the
	// default circular layout".
	PosX float64 `json:"pos_x,omitempty"`
	PosY float64 `json:"pos_y,omitempty"`

	// User-facing inbounds on this node (for per-user config generation).
	Inbounds []NodeInbound `json:"inbounds,omitempty"`

	// PendingHostKeyFingerprint is the remote SSH host-key fingerprint the
	// orchestrator observed during a capture/apply attempt that failed the
	// TOFU check (host key changed or untrusted). It is stored so that the
	// subsequent /trust POST can be verified against the actually-observed
	// fingerprint, preventing an attacker (or CSRF) from trusting an arbitrary
	// MITM fingerprint via a forged POST. Cleared on a successful trust or the
	// next capture attempt. (CTO-review §6 HIGH finding.)
	PendingHostKeyFingerprint string `json:"pending_host_key_fingerprint,omitempty"`

	// Takeover state — set when the node was captured from an existing VPN
	// (awg/singbox/xray/mtproxy). Carries the old service name + config backup
	// paths so the takeover can be rolled back to the old VPN if sing-box fails.
	Takeover *TakeoverState `json:"takeover,omitempty"`
}

// TakeoverState records what angry-box found on the node and what it did to
// take it over. Persisted on NodeInfo so the UI can show "taken over from Xray"
// and so a rollback knows which old service + config to restore.
type TakeoverState struct {
	// DetectedType is the VPN kind that was detected: awg|singbox|xray|mtproxy.
	DetectedType string `json:"detected_type"`
	// OldServiceName is the systemd unit of the old VPN (e.g. "xray",
	// "sing-box", "awg-quick@awg0"). Empty for kernel AWG (no single unit).
	OldServiceName string `json:"old_service_name,omitempty"`
	// OldConfigPath is the path of the old VPN's config file that was backed up.
	OldConfigPath string `json:"old_config_path,omitempty"`
	// OldConfigBackup is where the pre-takeover config was copied (under
	// $HOME/sing-box-orch-backup-<ts>/) for rollback.
	OldConfigBackup string `json:"old_config_backup,omitempty"`
	// OldUnitBackup is where the old systemd unit was copied (if any).
	OldUnitBackup string `json:"old_unit_backup,omitempty"`
	// OldEnabled records whether the old service was enabled at takeover time
	// (so rollback re-enables it only if it was enabled before).
	OldEnabled bool `json:"old_enabled,omitempty"`
	// ConvertedInbounds is how many inbounds the converter produced.
	ConvertedInbounds int `json:"converted_inbounds,omitempty"`
	// ConvertedAt is when the takeover completed.
	ConvertedAt time.Time `json:"converted_at,omitempty"`
	// Status: "taken" | "rolled-back" | "failed-both" | "detected".
	Status string `json:"status,omitempty"`
}

// NodeInbound describes a user-facing inbound on a node.
type NodeInbound struct {
	Protocol    string   `json:"protocol"` // "awg", "tuic", "vless-reality"
	Port        int      `json:"port"`
	Obfuscation string   `json:"obfuscation,omitempty"`  // extra obfuscation notes (preset name for AWG)
	ForUsers    []string `json:"for_users,omitempty"`    // user IDs this inbound serves
	OutboundTag string   `json:"outbound_tag,omitempty"` // target outbound tag; empty = "direct-out"
	Source      string   `json:"source,omitempty"`       // "standalone" or "chain:<chain_name>"
	// Tag is a stable identifier for this inbound. The merged-config render
	// derives the sing-box inbound/endpoint tag from it (and keys the
	// users-by-inbound map by it). Empty -> the legacy index-based "sa-<i>-<proto>"
	// tag is used (backward compat for inbounds created before Tag existed).
	Tag string `json:"tag,omitempty"`

	// Persisted server-side credentials
	UUID          string `json:"uuid,omitempty"`
	ServerPrivKey string `json:"server_priv_key,omitempty"`
	ServerPubKey  string `json:"server_pub_key,omitempty"`
	ShortID       string `json:"short_id,omitempty"`
	// ObfsPassword is the per-node Hysteria2 salamander obfs password. Each
	// node gets its own random password so the fleet does not share a single
	// predictable obfs secret. The client link must carry the same value.
	ObfsPassword string `json:"obfs_password,omitempty"`

	// For TLS-based standalone inbounds (TUIC, Hysteria2, etc.)
	TLSCertificate string `json:"tls_certificate,omitempty"`
	TLSPrivateKey  string `json:"tls_private_key,omitempty"`

	// For AWG standalone: sample client pub for the peer (to make server accept connections)
	// For full multi-user, client keys can be imported per user.
	AWGClientPub  string `json:"awg_client_pub,omitempty"`
	AWGClientPriv string `json:"awg_client_priv,omitempty"` // corresponding private for sample client config
}

// ConnectionLink represents a link between two nodes in a chain (spider web edge).
// It is the source of truth for the graph TOPOLOGY, while Chain.Nodes (an ordered
// list) remains the materialized deploy path. The two are kept in sync by the
// spider handlers when edges are created/deleted.
type ConnectionLink struct {
	ID         string        `json:"id"`
	FromNodeID string        `json:"from_node_id"`
	ToNodeID   string        `json:"to_node_id"`
	Transport  TransportType `json:"transport"`
	ChainName  string        `json:"chain_name,omitempty"`
	Label      string        `json:"label,omitempty"` // optional edge label
}

// ─── Audit log ───────────────────────────────────────────────────────────────

// AuditLog records a single operator/system action (CRUD, deploy, install,
// assign). target_id is always stored as a string (ints coerced); payload_json
// is the JSON-encoded payload or empty when no payload was supplied.
type AuditLog struct {
	ID          string    `json:"id"`
	Actor       string    `json:"actor"`       // "operator" by default
	Action      string    `json:"action"`      // create|update|delete|deploy|install|assign|unassign
	TargetType  string    `json:"target_type"` // node|chain|user|profile|route_rule|client_assignment|...
	TargetID    string    `json:"target_id,omitempty"`
	PayloadJSON string    `json:"payload_json,omitempty"`
	TS          time.Time `json:"ts"`
}

// ─── Per-node route rules ────────────────────────────────────────────────────

// RouteRule is an operator-editable routing rule scoped to a node. MatchType
// selects which field the match values populate; Action selects the route
// action. sing-box 1.13+ uses action rules (sniff/hijack-dns/route/reject).
type RouteRule struct {
	ID          string    `json:"id"`
	NodeID      string    `json:"node_id"`
	Priority    int       `json:"priority"`               // lower = earlier
	MatchType   string    `json:"match_type"`             // domain|domain_suffix|domain_keyword|ip_cidr|protocol
	MatchValues string    `json:"match_values"`           // newline- or comma-separated
	Action      string    `json:"action"`                 // route|block|sniff|hijack-dns
	OutboundTag string    `json:"outbound_tag,omitempty"` // for action=route
	Comment     string    `json:"comment,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// ─── MTProxy FakeTLS users ───────────────────────────────────────────────────

// MtproxyUser is a Telegram MTProxy client with a FakeTLS secret. The full
// sing-box secret is "ee" + SecretHex + hex(FakeTLSDomain); SecretHex is 16
// random bytes hex-encoded (32 chars).
type MtproxyUser struct {
	ID            string    `json:"id"`
	NodeID        string    `json:"node_id"`
	Name          string    `json:"name"`
	SecretHex     string    `json:"secret_hex"`      // 32 hex chars
	FakeTLSDomain string    `json:"fake_tls_domain"` // default "disk.yandex.ru"
	OrderIndex    int       `json:"order_index,omitempty"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}
