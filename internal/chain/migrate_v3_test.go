package chain

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestMigrateV3_OrphanNodeInfoCleanup verifies the v2->v3 migration drops
// NodeInfo + Metrics records whose Host was deleted pre-v0.8.8 (when DeleteHost
// left them behind). The Deploy Status page and Inbound form dropdown read
// ListNodeInfos, so those orphans showed up as "deleted nodes still hanging".
// After the migration only NodeInfos with an existing Host remain.
func TestMigrateV3_OrphanNodeInfoCleanup(t *testing.T) {
	sf := &storeFile{
		Hosts: []*model.Host{
			{ID: "alive", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		},
		NodeInfos: []*model.NodeInfo{
			{Host: model.Host{ID: "alive"}},   // has a Host — kept
			{Host: model.Host{ID: "ghost1"}},  // Host deleted — orphan, dropped
			{Host: model.Host{ID: "ghost2"}},  // Host deleted — orphan, dropped
		},
		Metrics: []*model.NodeMetrics{
			{HostID: "alive", Online: true},
			{HostID: "ghost1"},
		},
	}
	st := newV1Store(t, sf)
	migrateNow(t, st)

	st.mu.RLock()
	defer st.mu.RUnlock()
	got, err := st.readStore()
	if err != nil {
		t.Fatalf("readStore: %v", err)
	}
	if len(got.NodeInfos) != 1 {
		t.Fatalf("NodeInfos after cleanup = %d, want 1 (only the alive host)", len(got.NodeInfos))
	}
	if got.NodeInfos[0].ID != "alive" {
		t.Fatalf("kept NodeInfo = %q, want alive", got.NodeInfos[0].ID)
	}
	if len(got.Metrics) != 1 {
		t.Fatalf("Metrics after cleanup = %d, want 1 (only the alive host)", len(got.Metrics))
	}
	if got.Metrics[0].HostID != "alive" {
		t.Fatalf("kept Metrics = %q, want alive", got.Metrics[0].HostID)
	}
}

// TestMigrateV3_NoOrphansIsNoOp verifies the migration is a no-op (does not
// touch the store) when every NodeInfo has a Host.
func TestMigrateV3_NoOrphansIsNoOp(t *testing.T) {
	sf := &storeFile{
		Hosts: []*model.Host{
			{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		},
		NodeInfos: []*model.NodeInfo{
			{Host: model.Host{ID: "n1"}},
		},
		Metrics: []*model.NodeMetrics{{HostID: "n1"}},
	}
	st := newV1Store(t, sf)
	migrateNow(t, st)

	st.mu.RLock()
	defer st.mu.RUnlock()
	got, err := st.readStore()
	if err != nil {
		t.Fatalf("readStore: %v", err)
	}
	if len(got.NodeInfos) != 1 || got.NodeInfos[0].ID != "n1" {
		t.Fatalf("no-op migration changed NodeInfos: %+v", got.NodeInfos)
	}
	if len(got.Metrics) != 1 || got.Metrics[0].HostID != "n1" {
		t.Fatalf("no-op migration changed Metrics: %+v", got.Metrics)
	}
}