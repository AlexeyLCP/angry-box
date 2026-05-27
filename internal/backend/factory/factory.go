package factory

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/backend/singbox"
	"github.com/alexeylcp/angry-box/internal/backend/xray"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// Ensure Factory implements ports.Factory.
var _ ports.Factory = (*Factory)(nil)

// Factory creates Backend instances for a given BackendKind.
type Factory struct{}

// New creates a new Factory.
func New() *Factory {
	return &Factory{}
}

// Create returns a Backend matching the requested kind.
func (f *Factory) Create(kind model.BackendKind) (ports.Backend, error) {
	switch kind {
	case model.SingBox:
		return singbox.New(), nil
	case model.Xray:
		return xray.New(), nil
	default:
		return nil, fmt.Errorf("factory: unknown backend kind %q", kind)
	}
}
