package singbox

// fakessh_test.go — a ports.SSHClient/SSHConnector fake for singbox-backend
// tests (Deploy/InstallAWGModule/ApplyConfig/GetStatus/Remove/Reload all SSH).
// Mirrors the chain/web fakes. CTO-review C3 phase 4.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// fakeRule: if substring is in the command, return out/err. First match wins;
// empty substring = catch-all. When outs is non-empty, each match returns the
// next element (last element repeats once exhausted) — useful for repeated
// probes like lsmod before/after install.
type fakeRule struct {
	substring string
	out       string
	outs      []string
	err       error
}

// fakeSSH is a ports.SSHClient.
type fakeSSH struct {
	mu       sync.Mutex
	rules    []fakeRule
	seq      map[int]int
	commands []string
	uploads  []fakeUpload
}

type fakeUpload struct {
	Path    string
	Content string
}

func newFakeSSH(rules ...fakeRule) *fakeSSH { return &fakeSSH{rules: rules} }

func (f *fakeSSH) Run(cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	out, err := f.matchLocked(cmd)
	return out, err
}

func (f *fakeSSH) RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (string, string, int, error) {
	_ = ctx
	_ = timeout
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	out, err := f.matchLocked(cmd)
	return out, "", 0, err
}

func (f *fakeSSH) matchLocked(cmd string) (out string, err error) {
	for i := range f.rules {
		r := &f.rules[i]
		if r.substring != "" && !strings.Contains(cmd, r.substring) {
			continue
		}
		if len(r.outs) > 0 {
			if f.seq == nil {
				f.seq = make(map[int]int)
			}
			idx := f.seq[i]
			if idx >= len(r.outs) {
				idx = len(r.outs) - 1
			}
			f.seq[i]++
			return r.outs[idx], r.err
		}
		if r.substring == "" {
			return r.out, r.err
		}
		return r.out, r.err
	}
	return "", nil
}

func (f *fakeSSH) UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, fakeUpload{Path: remotePath, Content: content})
	_, err := f.matchLocked("upload:" + remotePath)
	return err
}

func (f *fakeSSH) Close() error { return nil }

// Saw reports whether a command containing needle was run.
func (f *fakeSSH) Saw(needle string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

// fakeConnector wraps a fakeSSH as a ports.SSHConnector.
type fakeConnector struct {
	client *fakeSSH
	err    error
}

func (c *fakeConnector) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.client, nil
}

// deployRules returns the rule set for a successful sing-box deploy:
// - version check reports the patched build (so installPatchedBinary is skipped)
// - mkdir/cert/unit/restart succeed
// - verifyServiceUp waits for "UP" (from `is-active --quiet ... && echo UP`)
// - journalctl returns empty
func deployRules() []fakeRule {
	// The version string must carry one of the extended build tags
	// (with_mtproxy/with_trusttunnel/with_sudoku) that isPatchedExtended
	// detects — a real binary built without version ldflags reports
	// "sing-box version unknown" plus its Tags: line, so mirror that.
	return []fakeRule{
		{substring: "version", out: "sing-box version unknown\nTags: with_gvisor,with_quic,with_wireguard,with_utls,with_mtproxy,with_trusttunnel,with_sudoku"},
		{substring: "mkdir -p", out: ""},
		{substring: "openssl", out: ""}, // ensureSelfSignedCert best-effort
		{substring: "cat > ", out: ""},  // systemd unit write via UploadText path
		{substring: "systemctl daemon-reload", out: ""},
		{substring: "is-active", out: "UP"},
		{substring: "journalctl", out: ""},
	}
}

var _ ports.SSHClient = (*fakeSSH)(nil)
var _ ports.SSHConnector = (*fakeConnector)(nil)

// errAny is a generic non-nil error for scripted failures.
var errAny = errors.New("scripted ssh failure")