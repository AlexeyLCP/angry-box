package chain

// fakessh_test.go — a fake ports.SSHClient/SSHConnector for unit-testing the
// deploy pipeline (pushConfig/performRollback/probeServiceUp/ApplyChain/...) without
// opening a real SSH connection. The fake matches Run/RunWithOutput commands by
// substring and returns scripted stdout/stderr/exit/error. UploadText records the
// last uploaded payload so tests can assert what was pushed.
//
// This is the keystone of C3 phase 2: it lets the chain package's deploy logic be
// exercised end-to-end (backup → upload → sing-box check → restart → health-probe
// → rollback) entirely in memory.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// fakeRule is one scripted response. If Substring is found in the command, the
// fake returns Out, ErrOut, Exit and Err. The first matching rule wins; rules
// are checked in insertion order. A rule with an empty Substring matches every
// command (use as a default/fallback, append last).
type fakeRule struct {
	substring string
	out       string
	errOut    string
	exit      int
	err       error
}

// fakeSSHClient is a ports.SSHClient that answers Run/RunWithOutput from a
// scripted rule set and records every command it was asked to run.
type fakeSSHClient struct {
	mu       sync.Mutex
	rules    []fakeRule
	commands []string          // every Run/RunWithOutput cmd, in order
	uploads  []fakeUpload      // every UploadText, in order
	failConn bool              // when true, the connector refuses to connect
	closed   bool
}

// fakeUpload records one UploadText call.
type fakeUpload struct {
	Content string
	Path    string
	Mode    os.FileMode
}

// newFakeSSH builds a client with the given rules (first match wins).
func newFakeSSH(rules ...fakeRule) *fakeSSHClient {
	return &fakeSSHClient{rules: rules}
}

// cmd records cmd and returns the first matching rule's response. Run is the
// stdout-only variant (stderr discarded on error by the real client contract).
func (f *fakeSSHClient) Run(cmd string) (string, error) {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	rule := f.matchLocked(cmd)
	f.mu.Unlock()
	if rule != nil && rule.err != nil {
		return "", rule.err
	}
	if rule != nil {
		return rule.out, nil
	}
	return "", nil
}

// RunWithOutput mirrors Run but returns the separated streams + exit code.
func (f *fakeSSHClient) RunWithOutput(ctx context.Context, cmd string, timeout time.Duration) (stdout, stderr string, exitCode int, err error) {
	_ = ctx
	_ = timeout
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	rule := f.matchLocked(cmd)
	f.mu.Unlock()
	if rule == nil {
		return "", "", 0, nil
	}
	return rule.out, rule.errOut, rule.exit, rule.err
}

// matchLocked returns the first rule whose substring is in cmd. Caller holds f.mu.
func (f *fakeSSHClient) matchLocked(cmd string) *fakeRule {
	for i := range f.rules {
		if f.rules[i].substring == "" || strings.Contains(cmd, f.rules[i].substring) {
			return &f.rules[i]
		}
	}
	return nil
}

// UploadText records the upload and returns nil unless a rule with substring
// "upload:" + path matches (used to simulate an upload failure).
func (f *fakeSSHClient) UploadText(ctx context.Context, content, remotePath string, mode os.FileMode) error {
	_ = ctx
	f.mu.Lock()
	f.uploads = append(f.uploads, fakeUpload{Content: content, Path: remotePath, Mode: mode})
	rule := f.matchLocked("upload:" + remotePath)
	f.mu.Unlock()
	if rule != nil {
		return rule.err
	}
	return nil
}

// Close marks the client closed. A rule with substring "close" can force an error.
func (f *fakeSSHClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// Commands returns a snapshot of every command the client was asked to run.
func (f *fakeSSHClient) Commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.commands))
	copy(out, f.commands)
	return out
}

// Uploads returns a snapshot of every UploadText call.
func (f *fakeSSHClient) Uploads() []fakeUpload {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeUpload, len(f.uploads))
	copy(out, f.uploads)
	return out
}

// SawCommand reports whether a command containing needle was run.
func (f *fakeSSHClient) SawCommand(needle string) bool {
	for _, c := range f.Commands() {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

// fakeConnector is a ports.SSHConnector that hands out a fixed fakeSSHClient (or
// refuses to connect). Tests inject this into NewApplier.
type fakeConnector struct {
	client *fakeSSHClient
	err    error // when set, Connect returns this error
}

func (c *fakeConnector) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.client, nil
}

// newFakeConnector wraps a fakeSSHClient as a connector.
func newFakeConnector(client *fakeSSHClient) *fakeConnector {
	return &fakeConnector{client: client}
}

// failingConnector returns a connector that always fails Connect with err.
func failingConnector(err error) *fakeConnector {
	return &fakeConnector{err: err}
}

// Compile-time check: the fake satisfies the port.
var _ ports.SSHClient = (*fakeSSHClient)(nil)
var _ ports.SSHConnector = (*fakeConnector)(nil)

// errExitOne is a convenient non-zero exit error for scripted failures.
var errExitOne = fmt.Errorf("ssh: exit status 1")