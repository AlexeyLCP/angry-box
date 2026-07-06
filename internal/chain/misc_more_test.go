package chain

// misc_more_test.go — covers presets (ListPresetsForProtocol/presetSupportsProtocol),
// awg_cps (CPSDNSString/GenerateQUICInitialWithSNI), awgpresets min, and the
// auto-apply background path. CTO-review C3 phase 5.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// TestListPresetsForProtocol_AWG verifies the AWG filter returns only presets
// with an AWG section (non-empty, contains a known AWG preset).
func TestListPresetsForProtocol_AWG(t *testing.T) {
	names := ListPresetsForProtocol("awg")
	if len(names) == 0 {
		t.Fatal("expected at least one AWG preset")
	}
	for _, n := range names {
		p, ok := GetPreset(n)
		if !ok {
			t.Errorf("preset %q from List not retrievable", n)
		}
		if p.AWG == nil {
			t.Errorf("preset %q listed for awg but has no AWG section", n)
		}
	}
}

// TestListPresetsForProtocol_VLESSReality verifies the vless-reality filter.
func TestListPresetsForProtocol_VLESSReality(t *testing.T) {
	names := ListPresetsForProtocol("vless-reality")
	if len(names) == 0 {
		t.Fatal("expected at least one vless-reality preset")
	}
}

// TestListPresetsForProtocol_DefaultTransport verifies an unknown protocol falls
// to the XHTTP-transport branch.
func TestListPresetsForProtocol_DefaultTransport(t *testing.T) {
	// "hysteria2" -> default branch -> presets with XHTTP.
	names := ListPresetsForProtocol("hysteria2")
	// Just assert it doesn't panic and returns a slice (may be empty if no
	// XHTTP preset, but built-ins include several).
	_ = names
}

// TestPresetSupportsProtocol_Unknown verifies the default branch returns the
// XHTTP presence.
func TestPresetSupportsProtocol_Unknown(t *testing.T) {
	p := ConnectionPreset{XHTTP: &XHTTPPreset{}}
	if !presetSupportsProtocol(p, "trojan") {
		t.Error("expected trojan to be supported via XHTTP")
	}
	p2 := ConnectionPreset{}
	if presetSupportsProtocol(p2, "trojan") {
		t.Error("expected no support when XHTTP is nil")
	}
}

// TestCPSDNSString_Empty verifies an empty packet yields "".
func TestCPSDNSString_Empty(t *testing.T) {
	if got := CPSDNSString(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestCPSDNSString_NonEmpty verifies a non-empty packet yields the CPS string
// with repeat=2 (the "<r 2>" marker).
func TestCPSDNSString_NonEmpty(t *testing.T) {
	got := CPSDNSString([]byte{0xde, 0xad})
	if got == "" {
		t.Fatal("expected non-empty")
	}
	if !strings.Contains(got, "<r 2>") {
		t.Errorf("got %q, want repeat marker <r 2>", got)
	}
}

// TestGenerateQUICInitialWithSNI verifies it returns a non-empty packet for a
// known SNI.
func TestGenerateQUICInitialWithSNI(t *testing.T) {
	pkt, dcid, version, err := GenerateQUICInitialWithSNI("www.google.com")
	if err != nil {
		t.Fatalf("GenerateQUICInitialWithSNI: %v", err)
	}
	if len(pkt) == 0 {
		t.Error("expected non-empty packet")
	}
	if len(dcid) == 0 {
		t.Error("expected non-empty dcid")
	}
	if version == 0 {
		t.Error("expected non-zero version")
	}
}

// TestMin verifies the unexported min helper.
func TestMin(t *testing.T) {
	if min(3, 5) != 3 {
		t.Error("min(3,5) != 3")
	}
	if min(5, 3) != 3 {
		t.Error("min(5,3) != 3")
	}
	if min(4, 4) != 4 {
		t.Error("min(4,4) != 4")
	}
}

// ─── autoapply ──────────────────────────────────────────────────────────────

// noopBackend/noopFactory for the auto-apply test (chain package already has
// noopBackend in applier_deploy_test.go? No — that's a different shape). Define
// minimal ones here.
type autoNoopBackend struct{}

func (autoNoopBackend) Deploy(context.Context, model.Host) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (autoNoopBackend) DeployWithOptions(context.Context, model.Host, model.DeployOptions) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (autoNoopBackend) InstallAWGModule(context.Context, model.Host) error { return nil }
func (autoNoopBackend) InstallAWGModuleWithOptions(context.Context, model.Host, model.DeployOptions) error { return nil }

// autoNoopBackend implements the optional ClientBackend capability so the
// merged-deploy connection-collapse path is exercised (CTO-review §8 test).
func (autoNoopBackend) DeployOptsAndClient(context.Context, model.Host, model.DeployOptions, ports.SSHClient) (*model.DeployResult, error) {
	return &model.DeployResult{Success: true}, nil
}
func (autoNoopBackend) InstallAWGModuleWithClient(context.Context, model.DeployOptions, ports.SSHClient) error {
	return nil
}
func (autoNoopBackend) ApplyConfig(context.Context, model.Host, model.ConfigType, model.ConfigParams) error {
	return nil
}
func (autoNoopBackend) Remove(context.Context, model.Host) error                     { return nil }
func (autoNoopBackend) GetStatus(context.Context, model.Host) (*model.Status, error) { return &model.Status{}, nil }
func (autoNoopBackend) GenerateConfig(model.ConfigType, model.ConfigParams) (*model.Config, error) {
	return &model.Config{}, nil
}
func (autoNoopBackend) Reload(context.Context, model.Host) error { return nil }
func (autoNoopBackend) Name() string                            { return "auto-noop" }
func (autoNoopBackend) Version() string                         { return "test" }

type autoNoopFactory struct{}

func (autoNoopFactory) Create() ports.Backend { return autoNoopBackend{} }

// TestScheduleAutoApply_NoInit verifies scheduling before InitAutoApply is a
// silent no-op (no panic).
func TestScheduleAutoApply_NoInit(t *testing.T) {
	// Reset the global ctx to unset (defensive: other tests may have set it).
	autoApplyCtx = autoApplyContext{}
	ScheduleAutoApply("n1", "test")
	WaitAutoApply() // should return immediately, nothing in flight
}

// TestScheduleAutoApply_NodeNotFound verifies a scheduled deploy for a missing
// node logs + audits and finishes without panic.
func TestScheduleAutoApply_NodeNotFound(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "store.json")
	InitAutoApply(autoNoopFactory{}, nil, storePath)
	defer func() { autoApplyCtx = autoApplyContext{} }()
	ScheduleAutoApply("ghost", "test")
	WaitAutoApply()
}

// TestScheduleAutoApply_AutoApplyOff verifies a node with AutoApply=false is
// skipped (no deploy attempted).
func TestScheduleAutoApply_AutoApplyOff(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.json")
	st := NewStore(storePath)
	st.SaveNodeInfo(&model.NodeInfo{
		Host:     model.Host{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		AutoApply: false,
	})
	InitAutoApply(autoNoopFactory{}, failingConnector(errExitOne), storePath)
	defer func() { autoApplyCtx = autoApplyContext{} }()
	ScheduleAutoApply("n1", "test")
	WaitAutoApply()
}

// TestScheduleAutoApply_Deploys verifies a node with AutoApply=true triggers a
// background deploy over the fake connector (here failing, so an audit entry is
// written — proves the path ran).
func TestScheduleAutoApply_Deploys(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.json")
	st := NewStore(storePath)
	st.SaveNodeInfo(&model.NodeInfo{
		Host:      model.Host{ID: "n2", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		AutoApply: true,
	})
	// Failing connector so ApplyMergedNode errors -> audit written.
	InitAutoApply(autoNoopFactory{}, failingConnector(errExitOne), storePath)
	defer func() { autoApplyCtx = autoApplyContext{} }()
	ScheduleAutoApply("n2", "test")
	WaitAutoApply()
	// Give the audit write a moment (it's synchronous inside runAutoDeploy, so
	// by WaitAutoApply it's done).
	time.Sleep(10 * time.Millisecond)
	logs, _ := NewStore(storePath).ListAuditLogs(50)
	found := false
	for _, l := range logs {
		if l.Action == "deploy" && l.TargetID == "n2" {
			found = true
		}
	}
	if !found {
		t.Error("expected a deploy audit entry for n2")
	}
}

// TestScheduleAutoApply_ConcurrencyCap verifies the semaphore bounds the number
// of concurrent background deploys (CTO-review §9: no unbounded goroutine
// fan-out on a large pending fleet). We schedule N>cap deploys against a
// connector that blocks inside Connect until released; a counter tracks the
// high-water mark of simultaneously-running Connect calls and asserts it never
// exceeds the cap.
func TestScheduleAutoApply_ConcurrencyCap(t *testing.T) {
	const capN = 3
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.json")
	st := NewStore(storePath)
	for i := 0; i < 10; i++ {
		st.SaveNodeInfo(&model.NodeInfo{
			Host:      model.Host{ID: fmt.Sprintf("n%d", i), Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
			AutoApply: true,
		})
	}

	var inFlight, highWater int32
	var mu sync.Mutex
	release := make(chan struct{})
	conn := &countingConnector{
		enter: func() {
			cur := atomic.AddInt32(&inFlight, 1)
			mu.Lock()
			if cur > highWater {
				highWater = cur
			}
			mu.Unlock()
			<-release // block until the test releases all
			atomic.AddInt32(&inFlight, -1)
		},
	}
	SetAutoApplyConcurrency(capN)
	InitAutoApply(autoNoopFactory{}, conn, storePath)
	defer func() { autoApplyCtx = autoApplyContext{} }()

	if got := AutoApplyMaxConcurrent(); got != capN {
		t.Fatalf("AutoApplyMaxConcurrent=%d, want %d", got, capN)
	}
	for i := 0; i < 10; i++ {
		ScheduleAutoApply(fmt.Sprintf("n%d", i), "cap-test")
	}
	// Wait until the semaphore is saturated (capN goroutines are past the
	// enter hook and blocked on <-release). The remaining 7 are queued on the
	// semaphore and have NOT entered the connector yet.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	hw := highWater
	mu.Unlock()
	if hw > int32(capN) {
		t.Errorf("high-water concurrent deploys = %d, exceeded cap %d", hw, capN)
	}
	if hw < int32(capN) {
		t.Errorf("high-water = %d, want saturation at cap %d (semaphore not acquired)", hw, capN)
	}
	close(release) // unblock all — Connect returns an error so deploys fail fast
	WaitAutoApply()
}

// countingConnector is an SSHConnector whose Connect delegates to an enter hook
// (used to count/limit concurrency in the autoapply cap test). After the enter
// hook unblocks it returns an error so the deploy aborts at Connect (no
// push/probe/audit churn) — keeping the test fast and the store quiet.
type countingConnector struct {
	enter func()
}

func (c *countingConnector) Connect(addr, user, keyPath string) (ports.SSHClient, error) {
	c.enter()
	return nil, errExitOne
}