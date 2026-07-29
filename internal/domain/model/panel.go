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

	// Per-user AWG traffic counters (cumulative bytes, folded from the kernel's
	// per-peer `awg show transfer` by the background metrics loop — v0.7).
	// Counters survive node redeploys and interface restarts (the folder
	// handles kernel counter resets); they are usage telemetry, not billing.
	AWGRxBytes  int64     `json:"awg_rx_bytes,omitempty"`
	AWGTxBytes  int64     `json:"awg_tx_bytes,omitempty"`
	AWGTrafficAt time.Time `json:"awg_traffic_at,omitempty"` // last traffic observation

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

	// ── P0b Slice 1: subscription / expiry-strategy / quota ────────────────────
	// These fields are ADDITIVE (omitempty) so old stores load unchanged — no
	// migration required to read them. Quota fields (DataLimit/UsedTraffic) are
	// stored now but NOT enforced this slice; enforcement is the P0b-2 V2Ray
	// stats poller. ExpireStrategy lets a user expire on a fixed date, on first
	// use, or never (Marzneshin model). SubscriptionToken backs the public
	// /sub/{token} endpoint. Status is the derived lifecycle state, computed
	// by ComputeStatus() at save + list time.
	SubscriptionToken string `json:"subscription_token,omitempty"` // url-safe secret in /sub/{token}
	ExpireStrategy    string `json:"expire_strategy,omitempty"`    // "fixed_date"|"start_on_first_use"|"never"
	UsageDuration     int64  `json:"usage_duration,omitempty"`      // seconds, when start_on_first_use
	ActivationDeadline time.Time `json:"activation_deadline,omitempty"` // outer bound to first connect (start_on_first_use)
	DataLimit          int64  `json:"data_limit,omitempty"`          // bytes, 0 = unlimited (P0b-2 enforces)
	DataLimitResetStrategy string `json:"data_limit_reset_strategy,omitempty"` // no_reset|day|week|month
	UsedTraffic        int64  `json:"used_traffic,omitempty"`        // populated by P0b-2 poller (zero this slice)
	LifetimeUsedTraffic int64 `json:"lifetime_used_traffic,omitempty"` // populated by P0b-2 poller
	FirstUseAt         time.Time `json:"first_use_at,omitempty"`    // stamped on first /sub fetch (start_on_first_use)
	Status             string `json:"status,omitempty"`            // active|disabled|limited|expired|on_hold
	ServiceID          string `json:"service_id,omitempty"`         // link to the Service the user was created from
}

// IsExpired returns true if the user has a non-zero expiry before now.
func (u *User) IsExpired() bool {
	return !u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt)
}

// ComputeStatus derives the user's lifecycle status from its fields. Pure
// function over the user (no clock-dependent "limited" — that needs the P0b-2
// stats poller). Called by handlers before SaveUser and by the list renderer
// so Status is always consistent. This slice can only produce
// active/disabled/expired/on_hold; "limited" is set only by the future poller.
//
// "never" ExpireStrategy means the user has no expiry (ExpiresAt ignored even
// if set), matching the Marzneshin expire_strategy semantics.
func (u *User) ComputeStatus() string {
	if !u.Active {
		return "disabled"
	}
	if u.ExpireStrategy != "never" && u.IsExpired() {
		return "expired"
	}
	if u.ExpireStrategy == "start_on_first_use" && u.FirstUseAt.IsZero() {
		return "on_hold"
	}
	return "active"
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
	// DefaultRealitySNI is the global default SNI domain for REALITY / TUIC
	// inbounds when no preset specifies one (ResolveServerName fallback).
	// Empty = the built-in const (www.cloudflare.com). Setting this lets the
	// operator pick a regional SNI (e.g. www.bing.com for CN) without editing
	// every preset.
	DefaultRealitySNI string `json:"default_reality_sni,omitempty"`
	// Services is the operator-defined product-tier catalog (a JSON array of
	// Service). A Service bundles chains + protocols + per-chain exit pin +
	// MTProxy defaults, offered as a single pick in the user wizard ("Step 2 —
	// What"). Mirrors the CustomPresets precedent: stored as json.RawMessage,
	// round-tripped via Store.GetSettings/SaveSettings, CRUD in the web layer
	// (internal/web/services.go). Empty = no Services defined (wizard shows
	// only the Custom/advanced path).
	Services json.RawMessage `json:"services,omitempty"`

	// OffsiteBackup is the operator-configured target for encrypted offsite
	// store backups (P2a). nil/empty = offsite backup disabled. The passphrase
	// is stored here (in the at-rest-encrypted store, protected by the master
	// key when enabled); it is NEVER the master key itself — the offsite blob
	// is encrypted with a passphrase-derived key (see chain.backup_crypto).
	// The master-key file never leaves the host.
	OffsiteBackup *OffsiteBackupConfig `json:"offsite_backup,omitempty"`

	// AutoRelocate is the global master switch + tuning for automatic node
	// relocation (P2b). nil/Enabled=false = the health monitor never relocates
	// on its own (operator relocates manually via the node row button). Even
	// when enabled globally, a node is only relocated if its own
	// NodeInfo.AutoRelocate opt-in is set — double opt-in by design.
	AutoRelocate *AutoRelocateConfig `json:"auto_relocate,omitempty"`
}

// AutoRelocateConfig configures automatic relocation of failed nodes onto
// warm-pool spares (P2b). Trigger: the background health monitor transitions
// a node to down/unreachable. Guardrails: global Enabled + per-node
// NodeInfo.AutoRelocate opt-in + CooldownHours between relocations + a spare
// must exist (Spare nodes with no chains/inbounds). Every decision (taken or
// skipped) is written to the audit log.
type AutoRelocateConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// CooldownHours is the minimum interval between two auto-relocations of the
	// SAME node. 0 = default 6. Prevents relocation loops on a flapping VPS.
	CooldownHours int `json:"cooldown_hours,omitempty"`
}

// OffsiteBackupConfig describes an encrypted offsite backup target (P2a).
// When Enabled, the orchestrator periodically (IntervalMin, default 360=6h)
// exports the store as plaintext (ExportStore), encrypts it with the
// passphrase (EncryptBackup), and pushes the blob to Host:RemotePath via SSH
// (UploadText). The SSH key is resolved by ID from the key registry (same
// mechanism as node SSH). "Backup now" also uses this config.
type OffsiteBackupConfig struct {
	Enabled     bool      `json:"enabled,omitempty"`
	Host        string    `json:"host,omitempty"`         // offsite SSH target, host:port
	User        string    `json:"user,omitempty"`
	SSHKeyID    string    `json:"ssh_key_id,omitempty"`   // registry key ID (resolved by the SSH connector)
	RemotePath  string    `json:"remote_path,omitempty"`  // e.g. /home/backup/angry-box.abbkp
	Passphrase  string    `json:"passphrase,omitempty"`   // never the master key; scrypt-derived
	IntervalMin int       `json:"interval_min,omitempty"` // 0 = default 360 (6h)
	LastBackupAt time.Time `json:"last_backup_at,omitempty"`
	// Retention is how many recent blobs to keep on the offsite target (rotation
	// via ls+rm after each push). 0 = default 5. Blobs are written to
	// <RemotePath>/angry-box-<timestamp>.abbkp; older blobs beyond Retention are
	// removed. A very large value effectively disables rotation.
	Retention int `json:"retention,omitempty"`
	// ScryptN overrides the default scrypt cost (2^16) for the passphrase KDF.
	// 0 = default. Lower N = less memory/faster but weaker brute-force resistance
	// (use on a weak orchestrator); higher N = stronger but heavier. The chosen
	// N is stored per-blob, so old blobs always decrypt regardless of this setting.
	ScryptN int `json:"scrypt_n,omitempty"`
}

// Service is an operator-defined product tier: a named bundle of chains +
// protocols + per-chain exit pin + (future) destination-routing presets,
// offered as a single selection in the user wizard ("Step 2 — What").
// Picking a Service in the wizard expands into the existing User fields
// (Protocols, ChainNames, ChainExit, MTProxy*) at save time. Mirrors the
// Marzneshin `Service` / Marzban `UserTemplate` pattern.
type Service struct {
	ID          string   `json:"id"`                    // unique, used as User.ServiceID ref
	Name        string   `json:"name"`                  // display ("Telegram Pro")
	Description string   `json:"description,omitempty"`
	ChainNames  []string `json:"chain_names,omitempty"` // expands to User.ChainNames
	// DefaultExitByChain pre-fills User.ChainExit per chain (chain name ->
	// ChainNode.ID). Empty value for a chain = chain's default exit (last node).
	// This is the first UI surface for User.ChainExit (panel.go:70), which is
	// already fully wired in buildMergedRoute (merged_config.go:345-472).
	DefaultExitByChain map[string]string `json:"default_exit_by_chain,omitempty"`
	// Protocols is the union of protocols the user gets configs for. When empty
	// the wizard falls back to the chains' protocols (legacy []string{"awg"}).
	Protocols []string `json:"protocols,omitempty"`
	// RoutingPresetIDs references ROUTING_PRESETS ids (telegram/youtube/...).
	// STORED BUT NOT RENDERED this slice — the data plane (BuildRoutingSection
	// in internal/chain/presets.go) consumes only ConnectionPreset.Routing
	// geosite names, not the ROUTING_PRESETS domain catalog. Per-user
	// destination routing is wired in P0b-3. The UI labels this clearly.
	RoutingPresetIDs []string `json:"routing_preset_ids,omitempty"`
	// MTProxy defaults applied when the Service bundles a Telegram MTProxy.
	// Auto-generates a secret on user-create if Enabled and the user has none.
	MTProxy struct {
		Enabled    bool     `json:"enabled,omitempty"`
		Domain     string   `json:"domain,omitempty"` // default "disk.yandex.ru"
		OrderIndex int      `json:"order_index,omitempty"`
		NodeIDs    []string `json:"node_ids,omitempty"`
	} `json:"mtproxy,omitempty"`
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

// Node health states (NodeMetrics.State). Computed by the metrics loop from
// SSH+systemd signals via chain.NextState, except NodeStateBlocked which is
// operator-marked via the block/unblock handlers (the orchestrator SSHes from
// a free region and cannot observe a DPI block itself — see AGENTS.md / P1a).
// String constants (no enum type) mirror the project convention (Source*,
// user Status). Additive: old stores with empty State derive from Online.
const (
	NodeStateHealthy     = "healthy"     // SSH ok, sing-box systemd active
	NodeStateSuspect     = "suspect"     // 1-2 consecutive failures (transient — not yet actionable)
	NodeStateDown        = "down"        // SSH ok but sing-box systemd inactive/failed (≥ DownThreshold fails)
	NodeStateUnreachable = "unreachable" // SSH dial fails (≥ DownThreshold fails) — host/network dead, not a service crash
	NodeStateBlocked     = "blocked"     // operator-marked DPI block — sticky, cleared only by the unblock handler
	NodeStateUnknown      = "unknown"     // no metrics yet / fresh node / just cleared
)

// HysteresisConfig controls how many consecutive probe failures/recoveries
// must occur before the node state machine transitions, so a single transient
// SSH timeout does not flap a node to "down" and spam audit events. Tunable
// via PanelSettings (P1a+); hardcoded DefaultHysteresis this slice.
type HysteresisConfig struct {
	DownThreshold    int `json:"down_threshold,omitempty"`    // consecutive fails → down/unreachable (default 3)
	RecoverThreshold int `json:"recover_threshold,omitempty"` // consecutive oks → healthy (default 2)
}

// DefaultHysteresis is the baked-in hysteresis used by collectAllMetrics when
// PanelSettings.Hysteresis is not configured. 3 fails at the default 15-min
// metrics interval ≈ 45 min before a node is flagged; 2 consecutive oks to
// recover — dampens flapping without hiding a real outage.
var DefaultHysteresis = HysteresisConfig{DownThreshold: 3, RecoverThreshold: 2}

// NodeMetrics holds the latest health/metrics snapshot for a node. Persisted
// to the store JSON (durable across restarts) — see store.go SaveMetrics.
type NodeMetrics struct {
	HostID      string    `json:"host_id"`
	Online      bool      `json:"online"`                 // back-compat bool, derived from State in the metrics loop
	State       string    `json:"state,omitempty"`        // one of NodeState* constants; empty → derive from Online (old stores)
	StateReason string    `json:"state_reason,omitempty"` // human-readable: "ssh dial: timeout", "sing-box inactive", "operator marked", ""
	Version     string    `json:"version,omitempty"`
	LatencyMs   int64     `json:"latency_ms,omitempty"`
	BytesSent   int64     `json:"bytes_sent,omitempty"`
	BytesRecv   int64     `json:"bytes_recv,omitempty"`
	LastChecked time.Time `json:"last_checked"`

	StateChangedAt  time.Time `json:"state_changed_at,omitempty"`  // when the current State was entered
	ConsecutiveFails int      `json:"consecutive_fails,omitempty"` // hysteresis: consecutive failing probes
	ConsecutiveOKs  int       `json:"consecutive_oks,omitempty"`   // hysteresis: consecutive successful probes (recovery)

	OS                 string `json:"os,omitempty"`
	SingBoxInstalled   bool   `json:"sing_box_installed,omitempty"`
	AWGModuleInstalled bool   `json:"awg_module_installed,omitempty"`

	// AWGPeerTransfer is the last observed per-peer kernel counters
	// (`awg show <iface> transfer`: pubkey → [rx,tx] bytes) on this node — the
	// baseline for the per-user traffic delta folding (v0.7). Updated by the
	// metrics loop on every tick it successfully reads the counters.
	AWGPeerTransfer map[string][2]int64 `json:"awg_peer_transfer,omitempty"`
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

	// Spare marks the node as a warm-pool standby (P2b): a healthy VPS with no
	// users/chains that auto-relocation can consume as the replacement address
	// for a down node. Spare nodes are excluded from chain building (they are
	// inventory, not topology) and shown separately in the UI.
	Spare bool `json:"spare,omitempty"`
	// AutoRelocate is the per-node opt-in for automatic relocation (P2b): when
	// the health state machine transitions this node to down/unreachable AND
	// the global PanelSettings.AutoRelocate.Enabled master switch is on, the
	// orchestrator relocates it onto a warm-pool spare (see chain.RelocateNode).
	AutoRelocate bool `json:"auto_relocate,omitempty"`
	// LastAutoRelocateAt records the last successful auto-relocation — the
	// cooldown guard (PanelSettings.AutoRelocate.CooldownHours) keys off it so
	// a flapping VPS is not relocated repeatedly.
	LastAutoRelocateAt time.Time `json:"last_auto_relocate_at,omitempty"`
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

	// SynthesizedUserIDs holds the IDs of model.User entries the AWG takeover
	// materialized from the imported awg0 peers (so per-client source_ip_cidr
	// routing is available on the takeover'd inbound — AGENTS.md Known Issue
	// #10). Populated on the success path; rollback deletes these users so a
	// rolled-back takeover does not leave phantom users in the store.
	SynthesizedUserIDs []string `json:"synthesized_user_ids,omitempty"`
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

	// ProfileID links this materialized inbound back to its InboundProfile
	// (the first-class, node-independent listener description). It is the
	// SINGLE source of truth for "which nodes is profile X deployed on" —
	// computed by scanning NodeInfo.Inbounds, never stored on the profile.
	// Empty = an ad-hoc standalone inbound created before profiles existed
	// (legacy) or a chain-materialized entry inbound (Source="chain:<name>").
	ProfileID string `json:"profile_id,omitempty"`

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

	// AWGServerAddress is the per-inbound kernel AWG server tunnel address
	// (e.g. "10.8.1.1/24"). Empty = "10.8.0.1/24" (the legacy default, shared
	// with chain AWG entries — a node hosting BOTH a chain AWG entry and a
	// standalone AWG inbound with the default collides on awg0; the deploy
	// emits a MergeReport warning in that case). Setting a distinct /24 here
	// (10.8.1.0/24, 10.8.2.0/24, ...) is the per-inbound subnet allocation that
	// avoids the collision — peers for this inbound then get IPs in the same
	// /24 via allocateAWGPeerIPInSubnet. AGENTS.md Known Issue #10 follow-up.
	AWGServerAddress string `json:"awg_server_address,omitempty"`

	// Standalone-AWG obfuscation material (v0.6.x follow-up to the chain-side
	// AWGCPS*/AWGH* persistence on model.Chain). Generated ONCE by
	// chain.EnsureInboundAWGMaterial (deploy + client-conf render paths) and
	// persisted so server and client render IDENTICAL values: without it the
	// standalone path rendered H1-H4 as degenerate zero-width "N-N" ranges
	// (header-junk randomization off — fingerprintable) and fresh random CPS
	// I1-I5 on every render. Level/Mimicry record what the material was built
	// for (a preset change invalidates the cache → regenerated).
	AWGCPSLevel   int    `json:"awg_cps_level,omitempty"`
	AWGCPSMimicry string `json:"awg_cps_mimicry,omitempty"`
	AWGCPSI1      string `json:"awg_cps_i1,omitempty"`
	AWGCPSI2      string `json:"awg_cps_i2,omitempty"`
	AWGCPSI3      string `json:"awg_cps_i3,omitempty"`
	AWGCPSI4      string `json:"awg_cps_i4,omitempty"`
	AWGCPSI5      string `json:"awg_cps_i5,omitempty"`
	AWGH1         string `json:"awg_h1,omitempty"`
	AWGH2         string `json:"awg_h2,omitempty"`
	AWGH3         string `json:"awg_h3,omitempty"`
	AWGH4         string `json:"awg_h4,omitempty"`

	// AWG 3.0 obfuscation mode (opt-in per-inbound toggle, AGENTS #5). When
	// set, this materialized inbound renders as a userspace sing-box
	// `type:"awg"` endpoint carrying the AWG3 fields (header protection /
	// content padding / rekey-after-time) — the kernel awg-quick path is
	// skipped for this inbound. Mirrors InboundProfile.AWG3* (the profile is
	// the shared source; ApplyProfileMaterialToInbound copies it here for
	// ad-hoc / takeover materialization). See InboundProfile for field docs.
	AWG3Mode                  bool   `json:"awg3_mode,omitempty"`
	AWG3HeaderProtectionKey    string `json:"awg3_header_protection_key,omitempty"`
	AWG3ContentPaddingAddition string `json:"awg3_content_padding_addition,omitempty"`
	AWG3RekeyAfterTime         string `json:"awg3_rekey_after_time,omitempty"`
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
