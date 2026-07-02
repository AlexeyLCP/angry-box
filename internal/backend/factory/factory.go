package factory

import (
	"github.com/alexeylcp/angry-box/internal/backend/singbox"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// Ensure Factory implements ports.Factory.
var _ ports.Factory = (*Factory)(nil)

// Factory creates Backend instances. Currently only sing-box-extended is supported.
// It carries an SSHConnector so every Backend it produces shares the same
// connection strategy — production via the composition root, a fake in tests.
type Factory struct {
	connector ports.SSHConnector
}

// New creates a new Factory. If connector is nil, the production SSH connector
// is used (resolved through the singbox package to avoid an import cycle here).
func New(connector ports.SSHConnector) *Factory {
	if connector == nil {
		connector = singbox.DefaultConnector()
	}
	return &Factory{connector: connector}
}

// Create returns a sing-box-extended Backend (the only supported backend).
func (f *Factory) Create() ports.Backend {
	return singbox.New(f.connector)
}