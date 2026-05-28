package factory

import (
	"github.com/alexeylcp/angry-box/internal/backend/singbox"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// Ensure Factory implements ports.Factory.
var _ ports.Factory = (*Factory)(nil)

// Factory creates Backend instances. Currently only sing-box-extended is supported.
type Factory struct{}

// New creates a new Factory.
func New() *Factory {
	return &Factory{}
}

// Create returns a sing-box-extended Backend (the only supported backend).
func (f *Factory) Create() ports.Backend {
	return singbox.New()
}
