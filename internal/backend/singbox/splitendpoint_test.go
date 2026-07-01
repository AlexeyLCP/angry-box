package singbox

// splitendpoint_test.go pins the host:port splitters used by the sing-box
// config renderers. They previously located the last ':' and split there,
// which mis-splits bare IPv6 addresses (e.g. "2001:db8::1:51820") and left
// brackets on bracketed forms ("[2001:db8::1]:51820"), producing a broken
// `address`/`server` field in the generated sing-box config (CTO-review H8).
// These tests lock in net.SplitHostPort semantics for both entry points.

import "testing"

func TestSplitEndpoint_BracketedIPv6(t *testing.T) {
	host, port := splitEndpoint("[2001:db8::1]:51820")
	if host != "2001:db8::1" {
		t.Errorf("host: got %q, want %q", host, "2001:db8::1")
	}
	if port != 51820 {
		t.Errorf("port: got %d, want %d", port, 51820)
	}
}

func TestSplitEndpoint_PlainIPv4(t *testing.T) {
	host, port := splitEndpoint("198.51.100.7:443")
	if host != "198.51.100.7" || port != 443 {
		t.Errorf("got (%q,%d), want (%q,%d)", host, port, "198.51.100.7", 443)
	}
}

func TestSplitEndpoint_NoPort(t *testing.T) {
	host, port := splitEndpoint("example.com")
	if host != "example.com" || port != 0 {
		t.Errorf("got (%q,%d), want (%q,%d)", host, port, "example.com", 0)
	}
}

func TestSplitEndpoint_BareIPv6NoPort(t *testing.T) {
	// A bare IPv6 address without brackets and without a port must round-trip
	// as the whole host (no spurious split at the last colon).
	host, port := splitEndpoint("2001:db8::1")
	if host != "2001:db8::1" || port != 0 {
		t.Errorf("got (%q,%d), want (%q,%d)", host, port, "2001:db8::1", 0)
	}
}

func TestSplitHostPort_BracketedIPv6(t *testing.T) {
	host, port, err := splitHostPort("[2001:db8::1]:51820")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "2001:db8::1" {
		t.Errorf("host: got %q, want %q", host, "2001:db8::1")
	}
	if port != "51820" {
		t.Errorf("port: got %q, want %q", port, "51820")
	}
}

func TestSplitHostPort_PlainIPv4(t *testing.T) {
	host, port, err := splitHostPort("198.51.100.7:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "198.51.100.7" || port != "443" {
		t.Errorf("got (%q,%q), want (%q,%q)", host, port, "198.51.100.7", "443")
	}
}

func TestSplitHostPort_NoPort(t *testing.T) {
	if _, _, err := splitHostPort("example.com"); err == nil {
		t.Error("expected an error for an address with no port")
	}
}