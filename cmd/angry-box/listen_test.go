package main

// listen_test.go pins the behavior of isLoopbackListen, which the serve
// command uses to decide whether the panel is safely bound to the loopback
// interface (plain HTTP acceptable) or exposed to the network (TLS strongly
// advised). Misclassifying an all-interfaces bind as loopback would silently
// drop the cleartext warning and leave SSH keys / Basic-Auth credentials
// sniffable; misclassifying loopback as exposed would only be noisy, so the
// critical assertions are the "exposed" cases returning false.

import "testing"

func TestIsLoopbackListen(t *testing.T) {
	cases := []struct {
		addr string
		want bool
		why  string
	}{
		{"127.0.0.1:9080", true, "explicit IPv4 loopback"},
		{"localhost:9080", true, "localhost alias"},
		{"[::1]:9080", true, "IPv6 loopback"},
		{"127.1.2.3:9080", true, "any 127/8 is loopback"},
		{":9080", false, "empty host binds all interfaces"},
		{"0.0.0.0:9080", false, "all-interfaces IPv4"},
		{"::9080", false, "all-interfaces IPv6"},
		{"0.0.0.0:0", false, "all-interfaces, ephemeral port"},
		{"10.0.0.5:9080", false, "LAN address is not loopback"},
		{"[2001:db8::1]:9080", false, "public IPv6 is not loopback"},
	}
	for _, c := range cases {
		got := isLoopbackListen(c.addr)
		if got != c.want {
			t.Errorf("isLoopbackListen(%q) = %v, want %v (%s)", c.addr, got, c.want, c.why)
		}
	}
}