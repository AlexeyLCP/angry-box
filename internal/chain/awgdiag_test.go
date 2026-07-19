package chain

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var errDiagFake = errors.New("exit status 1")

// diagRules scripts a healthy node: active services, up interfaces, fresh
// handshake, ip_forward=1, rp_filter=0, FORWARD rule present.
func diagRules() []fakeRule {
	fresh := strconv.FormatInt(time.Now().Add(-30*time.Second).Unix(), 10)
	return []fakeRule{
		{substring: "systemctl is-active awg-quick@awg0", out: "active\n"},
		{substring: "ip -br link show awg0", out: "awg0             UP\n"},
		{substring: "awg show awg0 listen-port", out: "51820\n"},
		{substring: "awg show awg0 peers", out: "PUB_A\nPUB_B\n"},
		{substring: "latest-handshakes", out: "PUB_A\t" + fresh + "\n"},
		{substring: "sysctl -n net.ipv4.ip_forward", out: "1\n"},
		{substring: "rp_filter", out: "0\n"},
		{substring: "iptables -C FORWARD", out: ""},
		{substring: "command -v iptables", out: "/usr/sbin/iptables\n"},
		{substring: "systemctl is-active sing-box", out: "active\n"},
		{substring: "ip -br link show sing-box-tun", out: "sing-box-tun     UP\n"},
		{substring: "", out: ""},
	}
}

func checkByName(checks []DiagCheck, name string) *DiagCheck {
	for i := range checks {
		if strings.Contains(checks[i].Name, name) {
			return &checks[i]
		}
	}
	return nil
}

func TestDiagnoseAWGNode_Healthy(t *testing.T) {
	client := newFakeSSH(diagRules()...)
	checks := DiagnoseAWGNode(context.Background(), client, "awg0", false)
	if len(checks) < 9 {
		t.Fatalf("want >=9 checks, got %d: %+v", len(checks), checks)
	}
	for _, name := range []string{"systemd", "interface", "listen port", "ip_forward", "rp_filter", "FORWARD", "iptables package", "sing-box service", "sing-box-tun"} {
		c := checkByName(checks, name)
		if c == nil {
			t.Errorf("missing check %q", name)
			continue
		}
		if c.Status != DiagOK {
			t.Errorf("check %q = %s (%s), want ok", name, c.Status, c.Detail)
		}
	}
	if c := checkByName(checks, "peers"); c == nil || c.Status != DiagOK || !strings.Contains(c.Detail, "2 peer") {
		t.Errorf("peers check = %+v, want ok with 2 peers and fresh handshake", c)
	}
}

func TestDiagnoseAWGNode_BrokenDataPlane(t *testing.T) {
	rules := diagRules()
	// Break: service down, rp_filter on, FORWARD missing, no iptables.
	for i := range rules {
		switch rules[i].substring {
		case "systemctl is-active awg-quick@awg0":
			rules[i].out = "inactive\n"
		case "rp_filter":
			rules[i].out = "1\n"
		case "iptables -C FORWARD":
			rules[i].errOut = "iptables: Bad rule (does a matching rule exist in that chain?)"
			rules[i].exit = 1
			rules[i].err = errDiagFake
		case "command -v iptables":
			rules[i].exit = 1
			rules[i].err = errDiagFake
		}
	}
	client := newFakeSSH(rules...)
	checks := DiagnoseAWGNode(context.Background(), client, "awg0", false)
	for _, name := range []string{"systemd", "rp_filter", "FORWARD", "iptables package"} {
		c := checkByName(checks, name)
		if c == nil || c.Status != DiagFail {
			t.Errorf("%s check = %+v, want fail", name, c)
		}
	}
}

func TestHandshakeFreshness(t *testing.T) {
	now := time.Now()
	out := "A\t" + strconv.FormatInt(now.Add(-1*time.Minute).Unix(), 10) + "\n" +
		"B\t" + strconv.FormatInt(now.Add(-1*time.Hour).Unix(), 10) + "\n" +
		"C\t0\n"
	fresh, stale := handshakeFreshness(out, now)
	if fresh != 1 || stale != 1 {
		t.Errorf("fresh=%d stale=%d, want 1/1", fresh, stale)
	}
}
