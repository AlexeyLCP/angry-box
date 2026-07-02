package factory

// factory_test.go is a compile-time guard that the concrete backend produced
// by the factory satisfies the ports.Backend contract — including
// DeployWithOptions, which replaced the unsafe b.(*singbox.Backend) type-
// assert in the CLI (CTO-review H5). If any backend drops the method, this
// file fails to compile; if the factory ever returns something that does not
// implement ports.Backend, the assertion catches it at build time rather than
// panicking in production at runtime.

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

func TestFactoryCreateSatisfiesBackend(t *testing.T) {
	// Compile-time assertion: New().Create() must implement ports.Backend,
	// including DeployWithOptions. Dropping the method on singbox or xray
	// breaks the build here — exactly the guarantee the old type-assert lacked.
	var _ ports.Backend = New(nil).Create()
}