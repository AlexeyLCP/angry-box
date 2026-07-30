package chain

// awg_push_test.go — exercises pushConfigWithAWG (the AWG-aware deploy push)
// against the fake SSH client. Verifies: (1) no AWG files → plain pushConfig
// passthrough; (2) AWG files → awg0.conf pushed + awg-quick@awg0 enabled before
// the sing-box config; (3) sing-box check failure rolls back BOTH the sing-box
// config and the awg0.conf. No real network.

import (
	"context"
	"strings"
	"testing"
)

// awgDeployRules returns the rule set for a successful AWG+sing-box deploy:
// mkdir, backup (both awg0 and config.json use the same backup helper which
// prints a path), sing-box check succeeds, all systemctl restart/is-active
// succeed, journalctl empty.
func awgDeployRules() []fakeRule {
	return []fakeRule{
		{substring: "mkdir -p /etc/amnezia/amneziawg", out: ""},
		{substring: "sing-box-orch-backup", out: "/tmp/sing-box-orch-backup-x/config.json.bak"},
		{substring: "sing-box check", out: "", exit: 0},
		{substring: "systemctl restart", out: ""},
		{substring: "is-active", out: "UP"},
		{substring: "journalctl", out: ""},
		{substring: "openssl", out: ""},
		{substring: "ls -t", out: ""},
		{substring: "systemctl daemon-reload", out: ""},
		{substring: "systemctl enable", out: ""},
		{substring: "systemctl reset-failed", out: ""},
		{substring: "cat /etc/sing-box/config.json", out: ""}, // old-config read (ApplyMergedNode)
	}
}

// TestPushConfigWithAWG_NoAWGFiles_Passthrough verifies that with no kernel
// AWG .conf files, pushConfigWithAWG delegates to pushConfig unchanged (no
// awg-quick commands, no mkdir, just the sing-box deploy).
func TestPushConfigWithAWG_NoAWGFiles_Passthrough(t *testing.T) {
	client := newFakeSSH(deployRules()...)
	out, err := pushConfigWithAWG(context.Background(), client, "node-x", validCfg, nil, false)
	if err != nil {
		t.Fatalf("pushConfigWithAWG: %v", err)
	}
	if out != "success" {
		t.Fatalf("got %q, want success", out)
	}
	if client.SawCommand("awg-quick") {
		t.Error("non-AWG node must not run any awg-quick commands")
	}
	if client.SawCommand("mkdir -p /etc/amnezia/amneziawg") {
		t.Error("non-AWG node must not create the amneziawg dir")
	}
	if !client.SawCommand("sing-box check") {
		t.Error("sing-box check was not run (passthrough broken)")
	}
}

// TestPushConfigWithAWG_AWGFiles_PushesAndEnables verifies the AWG+sing-box
// happy path: awg0.conf is uploaded to /etc/amnezia/amneziawg/awg0.conf,
// awg-quick@awg0 is enabled+restarted, THEN the sing-box config is pushed and
// checked. The awg-quick enable must happen BEFORE the sing-box restart so
// awg0 is up when sing-box's TUN overlay tries to capture it.
func TestPushConfigWithAWG_AWGFiles_PushesAndEnables(t *testing.T) {
	client := newFakeSSH(awgDeployRules()...)
	awgFiles := []AWGConfFile{
		{Path: awg0ConfPath, ServiceName: "awg-quick@awg0", Content: "[Interface]\nPrivateKey = K\n"},
	}
	out, err := pushConfigWithAWG(context.Background(), client, "node-awg", validCfg, awgFiles, false)
	if err != nil {
		t.Fatalf("pushConfigWithAWG: %v", err)
	}
	if out != "success" {
		t.Fatalf("got %q, want success", out)
	}

	// awg0.conf was uploaded to its real path.
	uploads := client.Uploads()
	foundAWG := false
	for _, u := range uploads {
		if u.Path == awg0ConfPath && strings.Contains(u.Content, "PrivateKey = K") {
			foundAWG = true
		}
	}
	if !foundAWG {
		t.Errorf("awg0.conf not uploaded to %s; uploads: %+v", awg0ConfPath, uploads)
	}

	// awg-quick@awg0 was enabled + restarted.
	if !client.SawCommand("systemctl enable awg-quick@awg0") {
		t.Error("awg-quick@awg0 was not enabled")
	}
	if !client.SawCommand("systemctl restart awg-quick@awg0") {
		t.Error("awg-quick@awg0 was not restarted")
	}

	// Ordering: awg-quick enable must come BEFORE sing-box restart, so awg0 is
	// up when sing-box's TUN overlay captures it.
	cmds := client.Commands()
	awgRestartIdx, sbRestartIdx := -1, -1
	for i, c := range cmds {
		if awgRestartIdx < 0 && strings.Contains(c, "restart awg-quick@awg0") {
			awgRestartIdx = i
		}
		if sbRestartIdx < 0 && strings.Contains(c, "restart sing-box") {
			sbRestartIdx = i
		}
	}
	if awgRestartIdx < 0 {
		t.Fatal("awg-quick restart not observed")
	}
	if sbRestartIdx < 0 {
		t.Fatal("sing-box restart not observed")
	}
	if awgRestartIdx > sbRestartIdx {
		t.Errorf("awg-quick restart (cmd %d) must come BEFORE sing-box restart (cmd %d)", awgRestartIdx, sbRestartIdx)
	}

	// sing-box config was still pushed + checked.
	if !client.SawCommand("sing-box check") {
		t.Error("sing-box check was not run after AWG push")
	}
}

// TestPushConfigWithAWG_SingBoxCheckFails_RollsBackBoth verifies that when the
// sing-box config check fails AFTER the awg0.conf was already pushed and
// enabled, BOTH are rolled back: the sing-box config is restored from backup
// AND awg-quick@awg0 is restarted from its backup. This is the atomic-rollback
// invariant — a failed sing-box deploy must not leave a half-applied awg0.
func TestPushConfigWithAWG_SingBoxCheckFails_RollsBackBoth(t *testing.T) {
	rules := awgDeployRules()
	// Make sing-box check fail (index 2 in awgDeployRules).
	rules[2] = fakeRule{substring: "sing-box check", out: "", errOut: "unknown field foo", exit: 1, err: errExitOne}
	client := newFakeSSH(rules...)
	awgFiles := []AWGConfFile{
		{Path: awg0ConfPath, ServiceName: "awg-quick@awg0", Content: "[Interface]\nPrivateKey = K\n"},
	}
	_, err := pushConfigWithAWG(context.Background(), client, "node-awg", validCfg, awgFiles, false)
	if err == nil {
		t.Fatal("expected sing-box check failure")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("got %q, want a rolled-back error", err.Error())
	}

	// awg-quick@awg0 must have been restarted by the rollback (performRollback
	// restarts the service after restoring the backup). Count restarts of
	// awg-quick@awg0: one from the enable step, one from rollback.
	cmds := client.Commands()
	awgRestarts := 0
	for _, c := range cmds {
		if strings.Contains(c, "systemctl restart awg-quick@awg0") {
			awgRestarts++
		}
	}
	if awgRestarts < 2 {
		t.Errorf("awg-quick@awg0 should be restarted twice (enable + rollback), got %d", awgRestarts)
	}
	// sing-box rollback also ran.
	if !client.SawCommand("systemctl restart sing-box") {
		t.Error("sing-box rollback did not restart sing-box")
	}
}

// TestPushConfigWithAWGTeardown_DisablesStaleUnitBeforeSingBox pins the SSH side
// of PROGRESS §39 bug B: an AWG3 inbound binds its UDP port from inside
// sing-box, so the stale kernel awg-quick@awg0 must be stopped+disabled BEFORE
// the sing-box restart — otherwise sing-box crash-loops with
// "bind: address already in use".
func TestPushConfigWithAWGTeardown_DisablesStaleUnitBeforeSingBox(t *testing.T) {
	rules := append(awgDeployRules(), fakeRule{substring: "systemctl disable --now", out: ""})
	client := newFakeSSH(rules...)
	// No rendered AWG files (AWG3-only node) but awg0 must be torn down.
	_, err := pushConfigWithAWGTeardown(context.Background(), client, "node-awg3", validCfg, nil, []string{"awg0"}, false)
	if err != nil {
		t.Fatalf("pushConfigWithAWGTeardown: %v", err)
	}
	if !client.SawCommand("systemctl disable --now awg-quick@awg0") {
		t.Fatalf("stale awg-quick@awg0 was not disabled; commands: %v", client.Commands())
	}
	// Ordering: the disable MUST precede the sing-box restart.
	cmds := client.Commands()
	disableIdx, sbRestartIdx := -1, -1
	for i, c := range cmds {
		if disableIdx < 0 && strings.Contains(c, "disable --now awg-quick@awg0") {
			disableIdx = i
		}
		if sbRestartIdx < 0 && strings.Contains(c, "restart sing-box") {
			sbRestartIdx = i
		}
	}
	if disableIdx < 0 || sbRestartIdx < 0 {
		t.Fatalf("missing disable (%d) or sing-box restart (%d)", disableIdx, sbRestartIdx)
	}
	if disableIdx > sbRestartIdx {
		t.Errorf("awg-quick disable (cmd %d) must come BEFORE sing-box restart (cmd %d)", disableIdx, sbRestartIdx)
	}
}

// TestPushConfigWithAWGTeardown_RestoresUnitOnSingBoxFailure verifies the
// rollback symmetry: when the sing-box push fails after a unit was torn down,
// the unit is re-enabled so the node returns to its pre-deploy state.
func TestPushConfigWithAWGTeardown_RestoresUnitOnSingBoxFailure(t *testing.T) {
	rules := append(awgDeployRules(), fakeRule{substring: "systemctl disable --now", out: ""})
	// is-active reports "active" so the teardown records awg0 for restore.
	for i := range rules {
		if rules[i].substring == "is-active" {
			rules[i] = fakeRule{substring: "is-active", out: "active"}
		}
		if rules[i].substring == "sing-box check" {
			rules[i] = fakeRule{substring: "sing-box check", out: "", errOut: "unknown field foo", exit: 1, err: errExitOne}
		}
	}
	client := newFakeSSH(rules...)
	_, err := pushConfigWithAWGTeardown(context.Background(), client, "node-awg3", validCfg, nil, []string{"awg0"}, false)
	if err == nil {
		t.Fatal("expected the sing-box check to fail")
	}
	if !client.SawCommand("systemctl enable --now awg-quick@awg0") {
		t.Errorf("torn-down unit must be restored on sing-box failure; commands: %v", client.Commands())
	}
}

// TestPushConfigWithAWGTeardown_InactiveUnitNotRestored verifies idempotency:
// a unit that was ALREADY inactive is not "restored" on failure (that would
// start an interface the node deliberately does not run).
func TestPushConfigWithAWGTeardown_InactiveUnitNotRestored(t *testing.T) {
	rules := append(awgDeployRules(), fakeRule{substring: "systemctl disable --now", out: ""})
	for i := range rules {
		if rules[i].substring == "is-active" {
			rules[i] = fakeRule{substring: "is-active", out: "inactive"}
		}
		if rules[i].substring == "sing-box check" {
			rules[i] = fakeRule{substring: "sing-box check", out: "", errOut: "boom", exit: 1, err: errExitOne}
		}
	}
	client := newFakeSSH(rules...)
	_, _ = pushConfigWithAWGTeardown(context.Background(), client, "node-awg3", validCfg, nil, []string{"awg0"}, false)
	if client.SawCommand("systemctl enable --now awg-quick@awg0") {
		t.Error("an already-inactive unit must NOT be started by the rollback")
	}
}

// TestPushConfigWithAWG_AWGEnableFails verifies that when the awg-quick@awg0
// enable/restart fails, the AWG .conf is rolled back and the sing-box push is
// never attempted.
func TestPushConfigWithAWG_AWGEnableFails(t *testing.T) {
	rules := awgDeployRules()
	// Make the awg-quick restart fail (is-active reports DOWN for awg-quick).
	// The enable step restarts awg-quick@awg0 then probes is-active; a DOWN
	// probe makes enableAWGService return an error.
	rules[4] = fakeRule{substring: "is-active", out: "DOWN"} // probe fails
	client := newFakeSSH(rules...)
	awgFiles := []AWGConfFile{
		{Path: awg0ConfPath, ServiceName: "awg-quick@awg0", Content: "[Interface]\nPrivateKey = K\n"},
	}
	_, err := pushConfigWithAWG(context.Background(), client, "node-awg", validCfg, awgFiles, false)
	if err == nil {
		t.Fatal("expected awg enable failure")
	}
	if !strings.Contains(err.Error(), "awg push") && !strings.Contains(err.Error(), "not active") {
		t.Errorf("got %q, want an awg-push/not-active error", err.Error())
	}
	// sing-box push must NOT have run (AWG failed first).
	if client.SawCommand("sing-box check") {
		t.Error("sing-box check must not run when AWG enable failed")
	}
}
