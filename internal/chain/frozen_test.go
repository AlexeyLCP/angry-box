package chain

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestValidateChainTransport_FrozenHysteria2(t *testing.T) {
	if err := ValidateChainTransport(model.TransportHysteria2); err == nil {
		t.Fatal("expected error for frozen Hysteria2 transport")
	}
}

func TestValidateChainTransport_AllowedXHTTP(t *testing.T) {
	if err := ValidateChainTransport(model.TransportXHTTP); err != nil {
		t.Fatalf("XHTTP should be allowed: %v", err)
	}
}

func TestValidateChainUserProtocol_FrozenTUIC(t *testing.T) {
	if err := ValidateChainUserProtocol(model.UserProtocolTUIC); err == nil {
		t.Fatal("expected error for frozen TUIC user protocol")
	}
}

func TestValidateStandaloneProtocol_Frozen(t *testing.T) {
	for _, proto := range []string{"tuic", "hysteria2"} {
		if err := ValidateStandaloneProtocol(proto); err == nil {
			t.Fatalf("expected error for frozen %q", proto)
		}
	}
}