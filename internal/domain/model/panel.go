package model

import "time"

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
	MetricsInterval   int    `json:"metrics_interval,omitempty"` // minutes, default 15 minutes
	SSHKeys           []SSHKeyEntry `json:"ssh_keys,omitempty"`
	DefaultProtocol   string `json:"default_protocol,omitempty"` // "awg", "tuic", "vless-reality"
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

	// User-facing inbounds on this node (for per-user config generation).
	Inbounds []NodeInbound `json:"inbounds,omitempty"`
}

// NodeInbound describes a user-facing inbound on a node.
type NodeInbound struct {
	Protocol    string `json:"protocol"`    // "awg", "tuic", "vless-reality"
	Port        int    `json:"port"`
	Obfuscation string `json:"obfuscation,omitempty"` // extra obfuscation notes
	ForUsers    []string `json:"for_users,omitempty"`  // user IDs this inbound serves
}

// ConnectionLink represents a link between two nodes in a chain (spider web edge).
type ConnectionLink struct {
	FromNodeID string        `json:"from_node_id"`
	ToNodeID   string        `json:"to_node_id"`
	Transport  TransportType `json:"transport"`
	ChainName  string        `json:"chain_name,omitempty"`
}
