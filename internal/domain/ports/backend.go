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

	// DeployWithOptions is the options-aware variant of Deploy. It lets callers
	// request sudo-wrapping and AWG kernel-module installation without
	// type-asserting to a concrete backend, so the Factory can return any
	// Backend implementation safely (CTO-review H5).
	DeployWithOptions(ctx context.Context, host model.Host, opts model.DeployOptions) (*model.DeployResult, error)

	// InstallAWGModule ensures the AmneziaWG kernel module is installed on the host.
	// Only needed for AWG wireguard inbound support. Safe to call multiple times.
	// Assumes the SSH user is root; for non-root sudoers use InstallAWGModuleWithOptions.
	InstallAWGModule(ctx context.Context, host model.Host) error

	// InstallAWGModuleWithOptions is the options-aware variant of
	// InstallAWGModule: it wraps the privileged apt/modprobe/install commands in
	// sudo when opts.UseSudo is set, so a non-root sudoer VPS can install the
	// AmneziaWG kernel module. This is the AWG counterpart of DeployWithOptions
	// (CTO-review H5 follow-up — the chain apply path runs as a non-root sudoer).
	InstallAWGModuleWithOptions(ctx context.Context, host model.Host, opts model.DeployOptions) error

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
