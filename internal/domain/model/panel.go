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

	CreatedAt time.Time `json:"created_at"`
}

// IsExpired returns true if the user has a non-zero expiry before now.
func (u *User) IsExpired() bool {
	return !u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt)
}

// PanelSettings holds global panel configuration.
type PanelSettings struct {
	AdminPasswordHash string `json:"admin_password_hash"`
	PanelCountry      string `json:"panel_country,omitempty"`   // e.g. "RU", "IR", "CN"
	Language          string `json:"language,omitempty"`        // e.g. "en", "ru"
	MetricsInterval   int    `json:"metrics_interval,omitempty"` // minutes, default 15 minutes
	SSHKeys           []SSHKeyEntry `json:"ssh_keys,omitempty"`
	DefaultProtocol   string          `json:"default_protocol,omitempty"` // "awg", "tuic", "vless-reality"
	CustomPresets     json.RawMessage `json:"custom_presets,omitempty"` // user-created obfuscation presets (JSON array of ConnectionPreset)
}

// SSHKeyEntry is an SSH key stored in the panel.
type SSHKeyEntry struct {
	ID      string `json:"id"`                // unique identifier
	Name    string `json:"name"`              // display name
	KeyPath string `json:"key_path,omitempty"` // filesystem path (system keys)
	KeyData string `json:"key_data,omitempty"` // private key content (user/manual keys)
	Source  string `json:"source"`            // "stored", "system", "manual"
}

// NodeMetrics holds the latest health/metrics snapshot for a node.
type NodeMetrics struct {
	HostID       string    `json:"host_id"`
	Online       bool      `json:"online"`
	Version      string    `json:"version,omitempty"`
	LatencyMs    int64     `json:"latency_ms,omitempty"`
	BytesSent    int64     `json:"bytes_sent,omitempty"`
	BytesRecv    int64     `json:"bytes_recv,omitempty"`
	LastChecked  time.Time `json:"last_checked"`
}

// NodeInfo enriches a Host with metadata for the web UI (country, bandwidth, inbounds).
type NodeInfo struct {
	Host

	Country    string `json:"country,omitempty"`
	Bandwidth  string `json:"bandwidth,omitempty"` // human-readable: "100 Mbps", "1 Gbps"
	Source     string `json:"source,omitempty"`    // "ssh_key", "password", "captured"

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
}

// NodeInbound describes a user-facing inbound on a node.
type NodeInbound struct {
	Protocol    string   `json:"protocol"`              // "awg", "tuic", "vless-reality"
	Port        int      `json:"port"`
	Obfuscation string   `json:"obfuscation,omitempty"` // extra obfuscation notes
	ForUsers    []string `json:"for_users,omitempty"`    // user IDs this inbound serves
	OutboundTag string   `json:"outbound_tag,omitempty"` // target outbound tag; empty = "direct-out"
	Source      string   `json:"source,omitempty"`       // "standalone" or "chain:<chain_name>"

	// Persisted server-side credentials
	UUID          string `json:"uuid,omitempty"`
	ServerPrivKey string `json:"server_priv_key,omitempty"`
	ServerPubKey  string `json:"server_pub_key,omitempty"`
	ShortID       string `json:"short_id,omitempty"`

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
	ID        string        `json:"id"`
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
	ID           string    `json:"id"`
	Actor        string    `json:"actor"`                    // "operator" by default
	Action       string    `json:"action"`                   // create|update|delete|deploy|install|assign|unassign
	TargetType   string    `json:"target_type"`              // node|chain|user|profile|route_rule|client_assignment|...
	TargetID     string    `json:"target_id,omitempty"`
	PayloadJSON  string    `json:"payload_json,omitempty"`
	TS           time.Time `json:"ts"`
}

// ─── Profiles / Services (client↔server mediation) ──────────────────────────

// Profile (a.k.a. Service) bundles a client type with a server role and an
// optional list of node IDs, decoupling clients from specific nodes (modelled
// after Marzneshin/Xboard). ClientType values: "user", "awg-peer",
// "exit-node", "mtproxy". ServerRole: "proxy_node", "awg_balancer",
// "mtproxy_server", "any".
type Profile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`        // unique
	Description string    `json:"description,omitempty"`
	ClientType  string    `json:"client_type"`
	ServerRole  string    `json:"server_role,omitempty"` // default "any"
	AutoApply   bool      `json:"auto_apply,omitempty"`  // intent flag (per-resource set is the real gate)
	ServerIDs   []string  `json:"server_ids,omitempty"`  // empty = all matching role
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ClientAssignment is the polymorphic many-to-many link between a Profile and a
// client entity (User / MtproxyUser / AWG peer / exit node). ClientID refers to
// the client entity's ID by ClientType; there is no DB-level FK.
type ClientAssignment struct {
	ID         string    `json:"id"`
	ProfileID  string    `json:"profile_id"`
	ClientType string    `json:"client_type"` // user|mtproxy|awg-peer|exit-node
	ClientID   string    `json:"client_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// ─── Per-node route rules ────────────────────────────────────────────────────

// RouteRule is an operator-editable routing rule scoped to a node. MatchType
// selects which field the match values populate; Action selects the route
// action. sing-box 1.13+ uses action rules (sniff/hijack-dns/route/reject).
type RouteRule struct {
	ID          string    `json:"id"`
	NodeID      string    `json:"node_id"`
	Priority    int       `json:"priority"` // lower = earlier
	MatchType   string    `json:"match_type"`   // domain|domain_suffix|domain_keyword|ip_cidr|protocol
	MatchValues string    `json:"match_values"` // newline- or comma-separated
	Action      string    `json:"action"`       // route|block|sniff|hijack-dns
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
