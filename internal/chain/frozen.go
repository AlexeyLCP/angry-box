package chain

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// Product focus (v0.2.x): AWG, VLESS+Reality+XHTTP, MTProxy/Telemt.
// TUIC and Hysteria2 (transport + standalone + user entry) are paused —
// QUIC/TLS self-signed cert plumbing is deferred until the core stack is stable.

// FrozenTransports are inter-node chain transports that must not be selected.
var FrozenTransports = map[model.TransportType]string{
	model.TransportHysteria2: "Hysteria2 transport is paused (QUIC/TLS cert work deferred); use XHTTP, Reality, or AWG",
}

// FrozenUserProtocols are user-entry protocols that must not be selected for new chains.
var FrozenUserProtocols = map[model.UserProtocol]string{
	model.UserProtocolTUIC: "TUIC is paused (QUIC/TLS issues); use AWG, VLESS+Reality, or Telemt",
}

// FrozenStandaloneProtocols are standalone inbound protocols blocked for new inbounds.
var FrozenStandaloneProtocols = map[string]string{
	"tuic":       "TUIC is paused (QUIC/TLS issues); use AWG, VLESS+Reality, or MTProxy",
	"hysteria2":  "Hysteria2 is paused (QUIC/TLS cert work deferred); use AWG, VLESS+Reality, or MTProxy",
}

// ValidateChainTransport returns an error when t is a frozen inter-node transport.
func ValidateChainTransport(t model.TransportType) error {
	if msg, ok := FrozenTransports[t]; ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// ValidateChainUserProtocol returns an error when p is a frozen user-entry protocol.
func ValidateChainUserProtocol(p model.UserProtocol) error {
	if msg, ok := FrozenUserProtocols[p]; ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// ValidateStandaloneProtocol returns an error when proto is a frozen standalone inbound.
func ValidateStandaloneProtocol(proto string) error {
	if msg, ok := FrozenStandaloneProtocols[proto]; ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// IsFrozenStandaloneProtocol reports whether proto is paused for new inbounds.
func IsFrozenStandaloneProtocol(proto string) bool {
	_, ok := FrozenStandaloneProtocols[proto]
	return ok
}