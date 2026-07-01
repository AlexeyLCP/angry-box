package model

import (
	"fmt"
	"time"
)

// ConfigType distinguishes transport configs (inter-hop) from user configs (client-facing).
type ConfigType int

const (
	ConfigTransport ConfigType = iota
	ConfigUser
	ConfigStandaloneNode
)

// Host describes a remote machine accessible via SSH.
type Host struct {
	ID      string // unique identifier (user-provided name or UUID)
	Addr    string // IP:port for SSH connection
	User    string // SSH user
	KeyPath string // path to private key for SSH auth
}

// KnownHost stores a verified SSH host key fingerprint.
type KnownHost struct {
	Addr        string    `json:"addr"`        // IP:port
	Fingerprint string    `json:"fingerprint"` // SHA256 base64 fingerprint
	FirstSeen   time.Time `json:"first_seen"`
	Trusted     bool      `json:"trusted"`
}

// Config is the result of config generation, ready to be applied.
type Config struct {
	Content string // the full config file content
	Format  string // "json" for both sing-box and xray
	Version string // backend version this config was generated for
}

// ConfigParams holds parameters needed to generate a proxy configuration.
// Common fields are typed explicitly; backend-specific settings go into Extra.
type ConfigParams struct {
	Port     int
	Protocol string // VLESS, VMess, Trojan, Shadowsocks, etc.
	Extra    map[string]any
}

// DeployResult describes the outcome of a Deploy operation.
type DeployResult struct {
	Success bool
	Version string
	Message string
}

// DeployOptions tunes Deploy behaviour. It is the backend-agnostic contract for
// options-aware deploys; each backend interprets the flags it understands and
// ignores (or rejects) the rest. Carrying these on the Backend interface (via
// DeployWithOptions) instead of type-asserting to a concrete backend keeps the
// hexagonal boundary intact and avoids panics when the factory returns a
// backend the caller did not hard-code for (CTO-review H5).
type DeployOptions struct {
	// InstallAWGModule installs the AmneziaWG kernel module + awg-quick (needed
	// for the awg_balancer role and any kernel-AWG server side). Backends that
	// do not support kernel AWG should ignore this flag.
	InstallAWGModule bool
	// UseSudo wraps privileged commands in sudo (for non-root SSH users with
	// passwordless sudo configured on the VPS).
	UseSudo bool
}

// Status describes the current state of a proxy process on a remote host.
type Status struct {
	Running bool
	Version string
	PID     int
	Uptime  string
	Error   string
}

// String returns a human-readable representation of ConfigType.
func (c ConfigType) String() string {
	switch c {
	case ConfigTransport:
		return "transport"
	case ConfigUser:
		return "user"
	case ConfigStandaloneNode:
		return "standalone"
	default:
		return fmt.Sprintf("ConfigType(%d)", c)
	}
}
