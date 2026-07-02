package chain

import (
	"strconv"
	"testing"

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

func TestStore_Profile_UniqueName(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProfile(&model.Profile{Name: "alpha", ClientType: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProfile(&model.Profile{Name: "alpha", ClientType: "user"}); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestStore_Profile_DeleteCascadesAssignments(t *testing.T) {
	s := newTestStore(t)
	p := &model.Profile{Name: "p1", ClientType: "user"}
	_ = s.SaveProfile(p)
	_ = s.SaveAssignment(&model.ClientAssignment{ProfileID: p.ID, ClientType: "user", ClientID: "u1"})
	if err := s.DeleteProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	assigns, _ := s.ListAssignmentsForProfile(p.ID)
	if len(assigns) != 0 {
		t.Errorf("assignments should cascade-delete with profile, got %d", len(assigns))
	}
}

func TestStore_Assignment_Unique(t *testing.T) {
	s := newTestStore(t)
	a1 := &model.ClientAssignment{ProfileID: "p1", ClientType: "user", ClientID: "u1"}
	a2 := &model.ClientAssignment{ProfileID: "p1", ClientType: "user", ClientID: "u1"}
	if err := s.SaveAssignment(a1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAssignment(a2); err == nil {
		t.Error("expected uniqueness error for duplicate assignment")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(tempStoreFile(t))
}