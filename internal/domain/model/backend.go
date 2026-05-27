package model

import "fmt"

// BackendKind identifies a proxy backend implementation.
type BackendKind string

const (
	SingBox BackendKind = "sing-box"
	Xray    BackendKind = "xray"
)

// ConfigType distinguishes transport configs (inter-hop) from user configs (client-facing).
type ConfigType int

const (
	ConfigTransport ConfigType = iota
	ConfigUser
)

// Host describes a remote machine accessible via SSH.
type Host struct {
	ID      string // unique identifier (user-provided name or UUID)
	Addr    string // IP:port for SSH connection
	User    string // SSH user
	KeyPath string // path to private key for SSH auth
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
	default:
		return fmt.Sprintf("ConfigType(%d)", c)
	}
}
