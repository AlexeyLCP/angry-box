package ports

import (
	"context"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// Backend is the contract every proxy implementation must satisfy.
// All methods accept context.Context as the first parameter for cancellation and timeouts.
type Backend interface {
	// Deploy installs the proxy software on the remote host via SSH.
	Deploy(ctx context.Context, host model.Host) (*model.DeployResult, error)

	// ApplyConfig pushes a generated config to the remote host and restarts the proxy.
	ApplyConfig(ctx context.Context, host model.Host, cfgType model.ConfigType, params model.ConfigParams) error

	// Remove stops the proxy service and removes installed files from the remote host.
	Remove(ctx context.Context, host model.Host) error

	// GetStatus retrieves the current proxy status from the remote host.
	GetStatus(ctx context.Context, host model.Host) (*model.Status, error)

	// GenerateConfig produces a proxy configuration file for the given type and parameters.
	// This is a local operation — no SSH connection required.
	GenerateConfig(cfgType model.ConfigType, params model.ConfigParams) (*model.Config, error)

	// Reload sends a graceful reload signal (e.g. SIGHUP) to the proxy on the remote host.
	Reload(ctx context.Context, host model.Host) error

	// Name returns the backend identifier ("sing-box" or "xray").
	Name() string

	// Version returns the proxy software version this backend manages.
	Version() string
}

// Factory creates Backend instances.
type Factory interface {
	Create() Backend
}
