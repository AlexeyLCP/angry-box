package chain

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestConfigHash_Stable(t *testing.T) {
	h1 := ConfigHash([]byte(`{"a":1}`))
	h2 := ConfigHash([]byte(`{"a":1}`))
	h3 := ConfigHash([]byte(`{"a":2}`))
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("sha256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestStore_AuditLog_AppendAndList(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.SaveAuditLog(&model.AuditLog{Action: "action-" + strconv.Itoa(i), TargetType: "test"}); err != nil {
			t.Fatalf("SaveAuditLog: %v", err)
		}
	}
	logs, err := s.ListAuditLogs(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(logs))
	}
	if logs[0].Action != "action-2" {
		t.Errorf("newest-first: got %s, want action-2", logs[0].Action)
	}
}

func TestStore_AuditLog_Cap(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5005; i++ {
		_ = s.SaveAuditLog(&model.AuditLog{Action: "action-" + strconv.Itoa(i), TargetType: "test"})
	}
	logs, _ := s.ListAuditLogs(0)
	if len(logs) != 5000 {
		t.Errorf("audit log should be capped to 5000, got %d", len(logs))
	}
}

// TestStore_AuditLog_WritesJsonlNotStore verifies the write-amplification split
// (CTO-review §12 D1): SaveAuditLog must append to <store>.audit.jsonl (O(1))
// and must NOT rewrite store.json on each audit entry. We snapshot store.json's
// mtime before any audit writes, write audit entries, and assert store.json's
// mtime is unchanged while the jsonl file grew.
func TestStore_AuditLog_WritesJsonlNotStore(t *testing.T) {
	s := newTestStore(t)
	// Seed store.json with a host so it exists on disk (a baseline mtime to
	// compare against).
	if err := s.SaveHost(&model.Host{ID: "h1", Addr: "1.1.1.1:22"}); err != nil {
		t.Fatal(err)
	}
	storePath := s.path
	jsonlPath := s.auditLogPath()

	before, err := os.Stat(storePath)
	if err != nil {
		t.Fatal(err)
	}
	mtimeBefore := before.ModTime()

	// Give the filesystem a moment so a same-second rewrite would still show a
	// different mtime on platforms with coarse mtime resolution.
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 3; i++ {
		if err := s.SaveAuditLog(&model.AuditLog{Action: "a" + strconv.Itoa(i), TargetType: "t"}); err != nil {
			t.Fatalf("SaveAuditLog: %v", err)
		}
	}

	after, err := os.Stat(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(mtimeBefore) {
		t.Errorf("store.json was rewritten by SaveAuditLog (mtime %v → %v); the split should append to the jsonl only", mtimeBefore, after.ModTime())
	}

	// jsonl must exist and have 3 lines.
	fi, err := os.Stat(jsonlPath)
	if err != nil {
		t.Fatalf("audit jsonl not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("audit jsonl is empty — entries were not appended")
	}
	data, _ := os.ReadFile(jsonlPath)
	if got := strings.Count(string(data), "\n"); got != 3 {
		t.Errorf("audit jsonl has %d lines, want 3", got)
	}
}

// TestStore_AuditLog_MergesLegacyInline verifies ListAuditLogs merges the
// legacy inline storeFile.AuditLogs (read-only) with the new jsonl file, so a
// store created before the split keeps its history (CTO-review §12 D1).
func TestStore_AuditLog_MergesLegacyInline(t *testing.T) {
	s := newTestStore(t)
	// Write legacy inline entries directly to store.json (bypassing SaveAuditLog
	// — simulates a pre-split store on disk).
	legacy := []*model.AuditLog{
		{ID: "legacy-1", Action: "old-1", TargetType: "test", TS: time.Unix(1000, 0), Actor: "operator"},
		{ID: "legacy-2", Action: "old-2", TargetType: "test", TS: time.Unix(2000, 0), Actor: "operator"},
	}
	s.mu.Lock()
	sf, _ := s.readStore()
	if sf == nil {
		sf = &storeFile{}
	}
	sf.AuditLogs = legacy
	_ = s.writeStore(sf)
	s.mu.Unlock()

	// Append a new entry via the split path (goes to jsonl).
	if err := s.SaveAuditLog(&model.AuditLog{Action: "new-3", TargetType: "test"}); err != nil {
		t.Fatal(err)
	}

	logs, err := s.ListAuditLogs(0)
	if err != nil {
		t.Fatal(err)
	}
	// newest-first: new-3 (jsonl) > old-2 > old-1 (legacy reverse).
	if len(logs) != 3 {
		t.Fatalf("expected 3 merged logs, got %d: %+v", len(logs), logs)
	}
	if logs[0].Action != "new-3" {
		t.Errorf("newest = %q, want new-3 (jsonl)", logs[0].Action)
	}
	if logs[1].Action != "old-2" || logs[2].Action != "old-1" {
		t.Errorf("legacy merge order wrong: got %q, %q, want old-2, old-1", logs[1].Action, logs[2].Action)
	}
}

// TestStore_AuditLog_DedupByID verifies a legacy inline entry that also appears
// in the jsonl (e.g. a store migrated mid-run) is not double-counted — the merge
// dedupes by ID.
func TestStore_AuditLog_DedupByID(t *testing.T) {
	s := newTestStore(t)
	// One entry in both legacy inline AND jsonl (same ID) + one jsonl-only.
	s.mu.Lock()
	sf, _ := s.readStore()
	if sf == nil {
		sf = &storeFile{}
	}
	sf.AuditLogs = []*model.AuditLog{{ID: "dup", Action: "shared", TargetType: "t", TS: time.Unix(1000, 0)}}
	_ = s.writeStore(sf)
	s.mu.Unlock()
	// Append the shared ID + a unique one to jsonl.
	if err := s.SaveAuditLog(&model.AuditLog{ID: "dup", Action: "shared", TargetType: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAuditLog(&model.AuditLog{ID: "uniq", Action: "unique", TargetType: "t"}); err != nil {
		t.Fatal(err)
	}
	logs, _ := s.ListAuditLogs(0)
	if len(logs) != 2 {
		t.Errorf("dedup by ID: expected 2 logs, got %d (dup not removed)", len(logs))
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(tempStoreFile(t))
}