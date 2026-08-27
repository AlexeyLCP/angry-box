package chain

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestStore_RevisionBumps(t *testing.T) {
	s := tempStore(t)
	if got := s.GetRevision(); got != 0 {
		t.Fatalf("empty store revision = %d, want 0", got)
	}
	seedHost(t, s, "n1", "1.2.3.4:22")
	r1 := s.GetRevision()
	if r1 == 0 {
		t.Fatal("revision did not bump after SaveHost")
	}
	seedHost(t, s, "n2", "5.6.7.8:22")
	if got := s.GetRevision(); got <= r1 {
		t.Fatalf("revision %d did not bump past %d after second write", got, r1)
	}

	// Revision is persisted and survives a re-open (no reset on load).
	s2 := NewStore(s.path)
	if got := s2.GetRevision(); got != s.GetRevision() {
		t.Fatalf("re-opened store revision = %d, want %d", got, s.GetRevision())
	}
}

func TestStore_UtilityStateRoundtrip(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.2.3.4:22")

	// No NodeInfo yet — SetUtilityState must create the record.
	u := &model.UtilityState{Name: model.UtilityCaddy, Installed: true, Version: "2.9.1", InstalledAt: time.Now()}
	if err := s.SetUtilityState("n1", u); err != nil {
		t.Fatalf("SetUtilityState: %v", err)
	}
	got, err := s.GetUtilityState("n1", model.UtilityCaddy)
	if err != nil {
		t.Fatalf("GetUtilityState: %v", err)
	}
	if got == nil || !got.Installed || got.Version != "2.9.1" {
		t.Fatalf("roundtrip got %+v", got)
	}

	// Upsert replaces the same-named record instead of appending.
	u2 := &model.UtilityState{Name: model.UtilityCaddy, Installed: false}
	if err := s.SetUtilityState("n1", u2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	info, err := s.GetNodeInfo("n1")
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	if n := len(info.Utilities); n != 1 {
		t.Fatalf("utilities len = %d, want 1 (upsert must not append)", n)
	}
	if info.Utilities[0].Installed {
		t.Fatal("upsert did not replace the record")
	}

	// Unknown utility on a known node = nil, no error.
	got, err = s.GetUtilityState("n1", model.UtilitySub)
	if err != nil || got != nil {
		t.Fatalf("unknown utility: got %+v, err %v", got, err)
	}
}

func TestStore_UtilityIsStale(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.2.3.4:22")

	// Not installed → never stale.
	stale, err := s.UtilityIsStale("n1", model.UtilitySub)
	if err != nil || stale {
		t.Fatalf("not-installed: stale=%v err=%v", stale, err)
	}

	// Stamp BEFORE saving (pipeline contract): fresh after the push.
	rev := s.GetRevision()
	if err := s.SetUtilityState("n1", &model.UtilityState{Name: model.UtilitySub, Installed: true, Revision: rev}); err != nil {
		t.Fatalf("SetUtilityState: %v", err)
	}
	stale, err = s.UtilityIsStale("n1", model.UtilitySub)
	if err != nil || stale {
		t.Fatalf("freshly pushed artifact marked stale (rev %d, store %d): %v", rev, s.GetRevision(), err)
	}

	// An unrelated store write after the push → stale (something changed).
	seedHost(t, s, "n2", "9.9.9.9:22")
	stale, err = s.UtilityIsStale("n1", model.UtilitySub)
	if err != nil || !stale {
		t.Fatalf("artifact must be stale after a newer store write: stale=%v err=%v", stale, err)
	}
}

func TestStore_RevisionPersistsAcrossInstances(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "rev.json")
	s := NewStore(path)
	seedHost(t, s, "n1", "1.2.3.4:22")
	want := s.GetRevision()
	if want == 0 {
		t.Fatal("revision is zero after a write")
	}
	s2 := NewStore(path)
	if got := s2.GetRevision(); got != want {
		t.Fatalf("fresh instance revision = %d, want %d", got, want)
	}
}
