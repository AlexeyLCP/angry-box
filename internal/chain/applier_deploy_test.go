package chain

// applier_deploy_test.go — exercises the SSH deploy pipeline (pushConfig/
// pushConfigLocked/performRollback/probeServiceUp/cleanupBackups and the
// ApplyChain/ApplyMergedNode entrypoints) against the fake SSH client from
// fakessh_test.go. No real network. CTO-review C3 phase 2.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// validCfg is a minimal valid sing-box JSON config used across the deploy tests.
const validCfg = `{"log":{"level":"error"},"inbounds":[{"type":"direct","tag":"direct"}],"outbounds":[{"type":"direct","tag":"direct"}]}`

// deployRules returns the rule set for a successful deploy: backup prints a
// path, sing-box check succeeds, systemctl restart succeeds, is-active prints
// UP, journalctl prints empty.
func deployRules() []fakeRule {
	return []fakeRule{
		{substring: "sing-box-orch-backup", out: "/tmp/sing-box-orch-backup-x/config.json.bak"},
		{substring: "sing-box check", out: "", exit: 0},
		{substring: "systemctl restart sing-box", out: ""},
		{substring: "is-active", out: "UP"},
		{substring: "journalctl", out: ""},
		{substring: "openssl", out: ""}, // cert generation (best-effort)
		{substring: "ls -t", out: ""},   // cleanupBackups
	}
}

// TestPushConfig_HappyPath_NonSudo verifies the full deploy sequence succeeds
// and the config is uploaded to the real path (no sudo tmp dance).
func TestPushConfig_HappyPath_NonSudo(t *testing.T) {
	client := newFakeSSH(deployRules()...)
	out, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err != nil {
		t.Fatalf("pushConfig: %v", err)
	}
	if out != "success" {
		t.Fatalf("got %q, want success", out)
	}
	uploads := client.Uploads()
	if len(uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(uploads))
	}
	if uploads[0].Path != "/etc/sing-box/config.json" {
		t.Errorf("uploaded to %s, want /etc/sing-box/config.json", uploads[0].Path)
	}
	if !client.SawCommand("sing-box check") {
		t.Error("sing-box check was not run")
	}
	if !client.SawCommand("systemctl restart sing-box") {
		t.Error("restart was not run")
	}
	if !client.SawCommand("is-active") {
		t.Error("health probe was not run")
	}
}

// TestPushConfig_HappyPath_Sudo verifies the sudo path writes to /tmp first
// then cp's into place (UploadText can't sudo the cat for a root-owned target).
func TestPushConfig_HappyPath_Sudo(t *testing.T) {
	client := newFakeSSH(deployRules()...)
	out, err := pushConfig(context.Background(), client, "", validCfg, true)
	if err != nil {
		t.Fatalf("pushConfig: %v", err)
	}
	if out != "success" {
		t.Fatalf("got %q, want success", out)
	}
	uploads := client.Uploads()
	if len(uploads) != 1 {
		t.Fatalf("expected 1 upload (to /tmp), got %d", len(uploads))
	}
	if !strings.HasPrefix(uploads[0].Path, "/tmp/angry-config-") {
		t.Errorf("sudo path uploaded to %s, want /tmp/angry-config-*", uploads[0].Path)
	}
	if !client.SawCommand("sudo bash -c 'cp") {
		t.Error("sudo cp into place was not run")
	}
}

// TestPushConfig_InvalidJSON short-circuits before any SSH command.
func TestPushConfig_InvalidJSON(t *testing.T) {
	client := newFakeSSH(deployRules()...)
	_, err := pushConfig(context.Background(), client, "", "not json", false)
	if err == nil {
		t.Fatal("expected invalid-JSON error")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("got %q, want invalid JSON", err.Error())
	}
	if len(client.commands) != 0 {
		t.Errorf("no SSH command should run on bad JSON, got %d", len(client.commands))
	}
}

// TestPushConfig_CheckFails_RollsBack verifies a sing-box check failure triggers
// rollback (cp backup back into place) and returns the rolled-back error.
func TestPushConfig_CheckFails_RollsBack(t *testing.T) {
	rules := deployRules()
	// Override sing-box check to fail with a diagnostics stderr.
	rules[1] = fakeRule{substring: "sing-box check", out: "", errOut: "unknown field foo", exit: 1, err: errExitOne}
	client := newFakeSSH(rules...)
	_, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err == nil {
		t.Fatal("expected check-failure error")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("got %q, want rolled back", err.Error())
	}
	// CTO-review §6: rollback succeeded → ErrDeployFailed (retry-able, node
	// still running old config), NOT ErrRollbackFailed.
	if !errors.Is(err, ErrDeployFailed) {
		t.Errorf("got %q, want errors.Is(err, ErrDeployFailed)", err.Error())
	}
	if errors.Is(err, ErrRollbackFailed) {
		t.Errorf("got %q, want !errors.Is(err, ErrRollbackFailed)", err.Error())
	}
	if !client.SawCommand("systemctl restart sing-box") {
		// rollback restarts the service to restore the old config
		t.Error("rollback did not restart the service")
	}
}

// TestPushConfig_CheckFails_RollbackAlsoFails verifies the critical "node left
// broken" path: when sing-box check fails AND the rollback (cp backup back into
// place) ALSO fails, the error must wrap ErrRollbackFailed (manual intervention
// needed) — NOT ErrDeployFailed (which would imply the node is back on the old
// config and retry-able). CTO-review §6: this path was previously untested.
func TestPushConfig_CheckFails_RollbackAlsoFails(t *testing.T) {
	rules := deployRules()
	// sing-box check fails → triggers rollback.
	rules[1] = fakeRule{substring: "sing-box check", out: "", errOut: "unknown field foo", exit: 1, err: errExitOne}
	// The rollback command is `test -f <bak> && cp <bak> <file>; systemctl
	// restart sing-box; sleep 2; ...`. Insert a rule (FIRST match wins, so it
	// must precede the "systemctl restart sing-box" rule) that matches the
	// rollback command uniquely via "test -f" and forces Run to error — the
	// normal restart command ("systemctl restart sing-box") does NOT contain
	// "test -f", so this rule only matches the rollback invocation.
	rules = append([]fakeRule{{substring: "test -f", out: "", err: errors.New("ssh: cp: no such file")}}, rules...)
	client := newFakeSSH(rules...)
	_, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err == nil {
		t.Fatal("expected check-failure + rollback-failure error")
	}
	if !strings.Contains(err.Error(), "rollback failed") {
		t.Errorf("got %q, want 'rollback failed' in message", err.Error())
	}
	if !errors.Is(err, ErrRollbackFailed) {
		t.Errorf("got %q, want errors.Is(err, ErrRollbackFailed) (node left broken)", err.Error())
	}
	if errors.Is(err, ErrDeployFailed) {
		t.Errorf("got %q, want !errors.Is(err, ErrDeployFailed) on rollback-also-failed", err.Error())
	}
}

// TestPushConfig_CheckFails_NoBackup verifies the no-backup branch (first deploy
// with no prior config): backup path is empty so rollback is skipped.
func TestPushConfig_CheckFails_NoBackup(t *testing.T) {
	rules := deployRules()
	rules[0] = fakeRule{substring: "sing-box-orch-backup", out: ""} // empty backup path
	rules[1] = fakeRule{substring: "sing-box check", out: "", errOut: "bad", exit: 1, err: errExitOne}
	client := newFakeSSH(rules...)
	_, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err == nil {
		t.Fatal("expected check-failure error")
	}
	if !strings.Contains(err.Error(), "no backup") {
		t.Errorf("got %q, want no-backup message", err.Error())
	}
}

// TestPushConfig_RestartFails_RollsBack verifies a systemctl restart failure
// triggers rollback.
func TestPushConfig_RestartFails_RollsBack(t *testing.T) {
	rules := deployRules()
	rules[2] = fakeRule{substring: "systemctl restart sing-box", out: "", err: errExitOne}
	client := newFakeSSH(rules...)
	_, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err == nil {
		t.Fatal("expected restart-failure error")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("got %q, want rolled back", err.Error())
	}
}

// TestPushConfig_HealthProbeFails_RollsBack verifies the is-active probe failing
// triggers rollback and returns the journalctl tail in the error.
func TestPushConfig_HealthProbeFails_RollsBack(t *testing.T) {
	rules := deployRules()
	rules[3] = fakeRule{substring: "is-active", out: "DOWN"} // never UP
	rules[4] = fakeRule{substring: "journalctl", out: "FATAL: bind: address already in use"}
	client := newFakeSSH(rules...)
	_, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err == nil {
		t.Fatal("expected health-probe failure")
	}
	if !strings.Contains(err.Error(), "not active") {
		t.Errorf("got %q, want not-active", err.Error())
	}
	if !strings.Contains(err.Error(), "bind: address already in use") {
		t.Errorf("error should carry journalctl tail, got %q", err.Error())
	}
}

// TestPushConfig_UploadFails verifies an UploadText failure aborts the deploy.
func TestPushConfig_UploadFails(t *testing.T) {
	rules := deployRules()
	// Make the upload to the config path fail.
	rules = append(rules, fakeRule{substring: "upload:/etc/sing-box/config.json", err: errors.New("scp: permission denied")})
	client := newFakeSSH(rules...)
	_, err := pushConfig(context.Background(), client, "", validCfg, false)
	if err == nil {
		t.Fatal("expected upload failure")
	}
	if !strings.Contains(err.Error(), "write config") {
		t.Errorf("got %q, want write-config error", err.Error())
	}
}

// TestPushConfig_LockingUsesHostLock verifies pushConfig with a nodeID takes the
// per-host lock (the same mutex identity as withHostLock). CTO-review C2.
func TestPushConfig_LockingUsesHostLock(t *testing.T) {
	client := newFakeSSH(deployRules()...)
	if _, err := pushConfig(context.Background(), client, "node-X", validCfg, false); err != nil {
		t.Fatalf("pushConfig: %v", err)
	}
	// The host lock for node-X must now exist (withHostLock created it lazily).
	mu := hostLock("node-X")
	if mu == nil {
		t.Fatal("hostLock(node-X) returned nil after a locked pushConfig")
	}
}

// TestPerformRollback_NoBackupPath verifies an empty backup path is rejected.
func TestPerformRollback_NoBackupPath(t *testing.T) {
	client := newFakeSSH()
	if err := performRollback(client, "/etc/sing-box/config.json", "", "sing-box", false); err == nil {
		t.Fatal("expected error for empty backup path")
	}
}

// TestPerformRollback_RestoresAndRestarts verifies the rollback command restores
// the backup and restarts the service.
func TestPerformRollback_RestoresAndRestarts(t *testing.T) {
	client := newFakeSSH(fakeRule{substring: "", out: ""})
	if err := performRollback(client, "/etc/sing-box/config.json", "/tmp/bak/config.json.bak", "sing-box", false); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !client.SawCommand("/tmp/bak/config.json.bak") {
		t.Error("rollback did not cp the backup back")
	}
	if !client.SawCommand("systemctl restart sing-box") {
		t.Error("rollback did not restart the service")
	}
}

// TestCreateBackup_FirstDeploy_NoConfig verifies createBackup returns an empty
// path string (but no error) when there is no existing config.
func TestCreateBackup_FirstDeploy_NoConfig(t *testing.T) {
	client := newFakeSSH(fakeRule{substring: "sing-box-orch-backup", out: ""})
	path, err := createBackup(client, "/etc/sing-box/config.json")
	if err != nil {
		t.Fatalf("createBackup: %v", err)
	}
	if path != "" {
		t.Errorf("got %q, want empty path on first deploy", path)
	}
}

// TestCreateBackup_BasenamePerFile verifies createBackup names the backup after
// the source file's basename (NOT a hardcoded config.json.bak). This is the
// regression guard for the multi-file AWG push collision: awg0.conf +
// awg-exit-n1.conf backed up in the same second must land in DISTINCT .bak
// files (awg0.conf.bak / awg-exit-n1.conf.bak), not both clobbered into one
// config.json.bak. The sing-box path (/etc/sing-box/config.json → config.json.bak)
// stays identical to the old hardcoded behavior.
func TestCreateBackup_BasenamePerFile(t *testing.T) {
	cases := []struct {
		name string
		file string
		// the issued shell command must cp the source to "$BAK_DIR/<want>"
		wantBak string
	}{
		{"sing-box config", "/etc/sing-box/config.json", "config.json.bak"},
		{"awg0 conf", "/etc/amnezia/amneziawg/awg0.conf", "awg0.conf.bak"},
		{"awg-exit conf", "/etc/amnezia/amneziawg/awg-exit-n1.conf", "awg-exit-n1.conf.bak"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeSSH(fakeRule{substring: "sing-box-orch-backup", out: "/tmp/bak/" + tc.wantBak})
			if _, err := createBackup(client, tc.file); err != nil {
				t.Fatalf("createBackup: %v", err)
			}
			cmds := client.Commands()
			if len(cmds) == 0 {
				t.Fatal("no command issued")
			}
			cmd := cmds[0]
			if !strings.Contains(cmd, tc.wantBak) {
				t.Errorf("backup command does not use basename %q:\n%s", tc.wantBak, cmd)
			}
			if strings.Contains(cmd, "$BAK_DIR/config.json.bak") && tc.wantBak != "config.json.bak" {
				t.Errorf("non-config file %q still uses hardcoded config.json.bak:\n%s", tc.file, cmd)
			}
		})
	}
}

// TestProbeServiceUp_UP verifies the probe returns nil when is-active prints UP.
func TestProbeServiceUp_UP(t *testing.T) {
	client := newFakeSSH(fakeRule{substring: "is-active", out: "UP"})
	if err := probeServiceUp(context.Background(), client, "sing-box", false); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

// TestProbeServiceUp_Down verifies the probe returns a journal-tailed error.
func TestProbeServiceUp_Down(t *testing.T) {
	client := newFakeSSH(
		fakeRule{substring: "is-active", out: "DOWN"},
		fakeRule{substring: "journalctl", out: "boom"},
	)
	err := probeServiceUp(context.Background(), client, "sing-box", false)
	if err == nil {
		t.Fatal("expected probe failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should carry journal tail, got %q", err.Error())
	}
}

// TestApplyChain_PreFlightConnectFails verifies a failed pre-flight SSH check to
// any node aborts ApplyChain before touching any config.
func TestApplyChain_PreFlightConnectFails(t *testing.T) {
	st := newTestStore(t)
	c := &model.Chain{
		Name:      "c1",
		Transport: model.TransportReality,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		},
	}
	st.SaveChain(c)
	// Connector that always refuses to connect.
	applier := NewApplier(&fakeFactory{noopBackend{}}, failingConnector(errors.New("dial tcp: connection refused")))
	_, err := applier.ApplyChain(context.Background(), st, c, "")
	if err == nil {
		t.Fatal("expected pre-flight failure")
	}
	if !strings.Contains(err.Error(), "pre-flight") {
		t.Errorf("got %q, want pre-flight", err.Error())
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("got %q, want connection refused", err.Error())
	}
}

// TestApplyChain_NodeDeployFails verifies a deploy failure on a node is recorded
// as a failed NodeResult (not a hard abort) — the chain reports per-node.
func TestApplyChain_NodeDeployFails(t *testing.T) {
	st := newTestStore(t)
	c := &model.Chain{
		Name:         "c2",
		Transport:    model.TransportReality,
		UserProtocol: model.UserProtocolVLESSReality,
		Nodes: []model.ChainNode{
			{ID: "n2", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		},
	}
	st.SaveChain(c)
	// Connector connects, but sing-box check fails → pushConfig returns error.
	client := newFakeSSH(
		fakeRule{substring: "sing-box-orch-backup", out: "/tmp/bak/config.json.bak"},
		fakeRule{substring: "sing-box check", out: "", errOut: "bad", exit: 1, err: errExitOne},
		fakeRule{substring: "systemctl restart sing-box", out: ""}, // rollback restart
		fakeRule{substring: "is-active", out: "UP"},
	)
	applier := NewApplier(&fakeFactory{noopBackend{}}, newFakeConnector(client))
	report, err := applier.ApplyChain(context.Background(), st, c, "")
	// ApplyChain returns an error only when EVERY node fails; with one node
	// failing the report carries the failure and err may be non-nil.
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	foundFailed := false
	for _, n := range report.Nodes {
		if n.ID == "n2" && !n.Success && strings.Contains(n.Error, "check") {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Errorf("expected n2 to be a failed node, got %+v; err=%v", report.Nodes, err)
	}
}

// TestApplyMergedNode_PreFlightConnectFails verifies ApplyMergedNode surfaces a
// connect failure.
func TestApplyMergedNode_PreFlightConnectFails(t *testing.T) {
	st := newTestStore(t)
	info := &model.NodeInfo{
		Host: model.Host{ID: "n3", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		Inbounds: []model.NodeInbound{
			{Protocol: "vless", Port: 8443, ForUsers: []string{"u1"}},
		},
	}
	st.SaveNodeInfo(info)
	applier := NewApplier(&fakeFactory{noopBackend{}}, failingConnector(errors.New("no route to host")))
	_, _, err := applier.ApplyMergedNode(context.Background(), st, info)
	if err == nil {
		t.Fatal("expected connect failure")
	}
	if !strings.Contains(err.Error(), "ssh connect") {
		t.Errorf("got %q, want ssh connect", err.Error())
	}
}

// TestApplyMergedNode_HappyPath verifies a full merged deploy succeeds and the
// config is pushed.
func TestApplyMergedNode_HappyPath(t *testing.T) {
	st := newTestStore(t)
	info := &model.NodeInfo{
		Host: model.Host{ID: "n4", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		Inbounds: []model.NodeInbound{
			{Protocol: "vless", Port: 8443, ForUsers: []string{"u1"}},
		},
	}
	st.SaveNodeInfo(info)
	client := newFakeSSH(deployRules()...)
	applier := NewApplier(&fakeFactory{noopBackend{}}, newFakeConnector(client))
	report, _, err := applier.ApplyMergedNode(context.Background(), st, info)
	if err != nil {
		t.Fatalf("ApplyMergedNode: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(client.Uploads()) == 0 {
		t.Error("expected the merged config to be uploaded")
	}
	// Verify the pushed config is valid JSON (sanity).
	if len(client.Uploads()) > 0 {
		if !json.Valid([]byte(client.Uploads()[0].Content)) {
			t.Error("uploaded config is not valid JSON")
		}
	}
}

// TestApplyMergedNode_OpensOneConnection verifies the connection-collapse
// plumbing: a merged deploy opens EXACTLY ONE SSH connection per node, even
// though it runs Deploy + (optionally) InstallAWG + the config push. Before the
// CTO-review §8 fix the applier's client + backend.DeployWithOptions's client +
// backend.InstallAWGModuleWithOptions's client were three separate dials.
func TestApplyMergedNode_OpensOneConnection(t *testing.T) {
	st := newTestStore(t)
	info := &model.NodeInfo{
		Host: model.Host{ID: "n-conn", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51820, Tag: "sa-awg-test"}, // AWG → triggers InstallAWG path too
		},
	}
	st.SaveNodeInfo(info)
	client := newFakeSSH(deployRules()...)
	conn := newFakeConnector(client)
	applier := NewApplier(&fakeFactory{noopBackend{}}, conn)
	_, _, err := applier.ApplyMergedNode(context.Background(), st, info)
	if err != nil {
		t.Fatalf("ApplyMergedNode: %v", err)
	}
	if conn.Connects != 1 {
		t.Errorf("merged deploy opened %d SSH connection(s), want 1 (connection collapse)", conn.Connects)
	}
}

// TestApplyChain_OpensOneConnectionPerNode verifies the per-node connection
// collapse on the ApplyChain path: each node gets one Connect for the deploy
// (InstallAWG + Deploy + config push all share it) plus one throwaway Connect
// for the pre-flight reachability check. Pre-collapse this was ~4 Connects per
// node (pre-flight + applier + backend.Deploy + backend.InstallAWG); now 2.
// (CTO-review §8.)
func TestApplyChain_OpensOneConnectionPerNode(t *testing.T) {
	st := newTestStore(t)
	c := &model.Chain{
		Name:         "conn-chain",
		Transport:    model.TransportXHTTP,
		UserProtocol: model.UserProtocolAWG, // AWG → InstallAWG runs per node
		Nodes: []model.ChainNode{
			{ID: "n-a", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k", Port: 443},
		},
	}
	st.SaveChain(c)
	client := newFakeSSH(deployRules()...)
	conn := newFakeConnector(client)
	applier := NewApplier(&fakeFactory{noopBackend{}}, conn)
	_, err := applier.ApplyChain(context.Background(), st, c, "")
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	// 1 node → 2 Connects: 1 pre-flight (throwaway) + 1 deploy (shared across
	// InstallAWG + Deploy + push). Pre-collapse this was ~4.
	if conn.Connects != 2 {
		t.Errorf("ApplyChain (1 node) opened %d SSH connection(s), want 2 (1 pre-flight + 1 collapsed deploy)", conn.Connects)
	}
}

// TestNewApplier_NilConnectorDefaults verifies NewApplier with a nil connector
// falls back to the production default (non-nil, doesn't panic).
func TestNewApplier_NilConnectorDefaults(t *testing.T) {
	a := NewApplier(nil, nil)
	if a == nil {
		t.Fatal("NewApplier returned nil")
	}
	if a.connector == nil {
		t.Fatal("connector should default to production, not stay nil")
	}
}

// fakeFactory is a ports.Factory returning a fake backend — used when an
// ApplyChain test needs the factory path (AWG module install etc.) to be a no-op.
type fakeFactory struct{ backend ports.Backend }

func (f *fakeFactory) Create() ports.Backend { return f.backend }

// noopBackend is a ports.Backend whose every method is a no-op success.
type noopBackend struct{}

func (noopBackend) Deploy(context.Context, model.Host) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) DeployWithOptions(context.Context, model.Host, model.DeployOptions) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) InstallAWGModule(context.Context, model.Host) error { return nil }
func (noopBackend) InstallAWGModuleWithOptions(context.Context, model.Host, model.DeployOptions) error {
	return nil
}

// noopBackend implements the optional ClientBackend capability so the
// connection-collapse path is exercised (CTO-review §8 test).
func (noopBackend) DeployOptsAndClient(context.Context, model.Host, model.DeployOptions, ports.SSHClient) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (noopBackend) InstallAWGModuleWithClient(context.Context, model.DeployOptions, ports.SSHClient) error {
	return nil
}
func (noopBackend) ApplyConfig(context.Context, model.Host, model.ConfigType, model.ConfigParams) error {
	return nil
}
func (noopBackend) Remove(context.Context, model.Host) error { return nil }
func (noopBackend) GetStatus(context.Context, model.Host) (*model.Status, error) {
	return &model.Status{}, nil
}
func (noopBackend) GenerateConfig(model.ConfigType, model.ConfigParams) (*model.Config, error) {
	return &model.Config{}, nil
}
func (noopBackend) Reload(context.Context, model.Host) error { return nil }
func (noopBackend) Name() string                             { return "fake" }
func (noopBackend) Version() string                          { return "test" }

// keep time imported (some rules above reference it implicitly via the package).
var _ = time.Second
