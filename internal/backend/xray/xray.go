package xray

import (
	"context"
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// Ensure Backend implements ports.Backend.
var _ ports.Backend = (*Backend)(nil)

// Backend manages xray proxy instances on remote hosts.
type Backend struct{}

// New creates a new xray Backend.
func New() *Backend {
	return &Backend{}
}

func (b *Backend) Deploy(ctx context.Context, host model.Host) (*model.DeployResult, error) {
	return nil, fmt.Errorf("xray: deploy not implemented")
}

func (b *Backend) ApplyConfig(ctx context.Context, host model.Host, cfgType model.ConfigType, params model.ConfigParams) error {
	return fmt.Errorf("xray: applyConfig not implemented")
}

func (b *Backend) Remove(ctx context.Context, host model.Host) error {
	return fmt.Errorf("xray: remove not implemented")
}

func (b *Backend) GetStatus(ctx context.Context, host model.Host) (*model.Status, error) {
	return nil, fmt.Errorf("xray: getStatus not implemented")
}

func (b *Backend) GenerateConfig(cfgType model.ConfigType, params model.ConfigParams) (*model.Config, error) {
	return nil, fmt.Errorf("xray: generateConfig not implemented")
}

func (b *Backend) Reload(ctx context.Context, host model.Host) error {
	return fmt.Errorf("xray: reload not implemented")
}

func (b *Backend) Name() string  { return "xray" }
func (b *Backend) Version() string { return "26.0.0" }
