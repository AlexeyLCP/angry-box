package web

// fakessh_test.go — a minimal ports.SSHClient/SSHConnector fake for web-package
// handler tests that exercise apply-chain / takeover (which SSH under the hood).
// Mirrors the chain-package fake but kept local to avoid an import cycle.
//
// CTO-review C3 phase 3.

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// webFakeRule is one scripted response: if substring is in the command, return
// out/err. First match wins; empty substring = catch-all.
type webFakeRule struct {
	substring string
	out       string
	err       error
}

// webFakeSSH is a ports.SSHClient for the web harness.
type webFakeSSH struct {
	mu             sync.Mutex
	rules          []webFakeRule
	commands       []string
	uploads        []string
	uploadContents map[string]string
}

func newWebFakeSSH(rules ...webFakeRule) *webFakeSSH {
	return &webFakeSSH{rules: rules, uploadContents: map[string]string{}}
}

func (f *webFakeSSH) Run(cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	r := f.matchLocked(cmd)
	if r == nil {
		return "", nil
	}
	return r.out, r.err
}

func (f *webFakeSSH) RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (string, string, int, error) {
	_ = ctx
	_ = timeout
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	r := f.matchLocked(cmd)
	if r == nil {
		return "", "", 0, nil
	}
	return r.out, "", 0, r.err
}

func (f *webFakeSSH) matchLocked(cmd string) *webFakeRule {
	for i := range f.rules {
		if f.rules[i].substring == "" || strings.Contains(cmd, f.rules[i].substring) {
			return &f.rules[i]
		}
	}
	return nil
}

func (f *webFakeSSH) UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, remotePath)
	if f.uploadContents == nil {
		f.uploadContents = map[string]string{}
	}
	f.uploadContents[remotePath] = content
	r := f.matchLocked("upload:" + remotePath)
	if r != nil {
		return r.err
	}
	return nil
}

// uploadedContent returns the content uploaded to remotePath ("" if none).
func (f *webFakeSSH) uploadedContent(remotePath string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uploadContents[remotePath]
}

func (f *webFakeSSH) Close() error { return nil }

// webFakeConnector wraps a webFakeSSH as a ports.SSHConnector.
type webFakeConnector struct {
	client *webFakeSSH
	err    error
}

func (c *webFakeConnector) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.client, nil
}

// deployRules returns the rule set for a successful sing-box deploy (backup,
// check ok, restart ok, is-active UP). Enough for apply-chain happy path.
func deployRules() []webFakeRule {
	return []webFakeRule{
		{substring: "sing-box-orch-backup", out: "/tmp/bak/config.json.bak"},
		{substring: "sing-box check", out: ""},
		{substring: "systemctl restart sing-box", out: ""},
		{substring: "is-active", out: "UP"},
		{substring: "journalctl", out: ""},
		{substring: "openssl", out: ""},
		{substring: "ls -t", out: ""},
	}
}

var _ ports.SSHClient = (*webFakeSSH)(nil)
var _ ports.SSHConnector = (*webFakeConnector)(nil)