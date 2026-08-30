package bot

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func tempStore(t *testing.T) *chain.Store {
	t.Helper()
	return chain.NewStore(filepath.Join(t.TempDir(), "store.json"))
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{
		0:               "0 B",
		512:             "512 B",
		1024:            "1.0 KiB",
		1536:            "1.5 KiB",
		1 << 20:         "1.0 MiB",
		5 << 20:         "5.0 MiB",
		1 << 30:         "1.0 GiB",
		(1 << 30) + (1 << 29): "1.5 GiB",
	} {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBindCodeShape(t *testing.T) {
	a, b := bindCode(), bindCode()
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("bind code must be 32 hex chars: %q / %q", a, b)
	}
	if a == b {
		t.Fatal("bind codes must be random")
	}
}

func TestUserOnline(t *testing.T) {
	fresh := &model.User{AWGTrafficAt: time.Now().Add(-1 * time.Minute)}
	stale := &model.User{AWGTrafficAt: time.Now().Add(-1 * time.Hour)}
	never := &model.User{}
	if !userOnline(fresh) {
		t.Error("recent traffic must be online")
	}
	if userOnline(stale) {
		t.Error("hour-old traffic must be offline")
	}
	if userOnline(never) {
		t.Error("never-seen user must be offline")
	}
}

func TestEnforceActivationDeadlines(t *testing.T) {
	st := tempStore(t)
	// start_on_first_use, deadline passed, never activated -> must expire.
	overdue := &model.User{
		ID: "u-over", Name: "over", Active: true,
		ExpireStrategy:     "start_on_first_use",
		ActivationDeadline: time.Now().Add(-24 * time.Hour),
	}
	// start_on_first_use, deadline in the future -> untouched.
	pending := &model.User{
		ID: "u-pend", Name: "pend", Active: true,
		ExpireStrategy:     "start_on_first_use",
		ActivationDeadline: time.Now().Add(24 * time.Hour),
	}
	// start_on_first_use, already used -> untouched.
	used := &model.User{
		ID: "u-used", Name: "used", Active: true,
		ExpireStrategy:     "start_on_first_use",
		ActivationDeadline: time.Now().Add(-24 * time.Hour),
		FirstUseAt:         time.Now().Add(-48 * time.Hour),
	}
	for _, u := range []*model.User{overdue, pending, used} {
		if err := st.SaveUser(u); err != nil {
			t.Fatal(err)
		}
	}

	b := &Bot{store: st}
	users, _ := st.ListUsers()
	b.enforceActivationDeadlines(users)

	gotOver, _ := st.GetUser("u-over")
	if gotOver.ComputeStatus() != "expired" {
		t.Errorf("overdue start_on_first_use user must expire, got %s", gotOver.ComputeStatus())
	}
	gotPend, _ := st.GetUser("u-pend")
	if gotPend.ComputeStatus() != "on_hold" {
		t.Errorf("future-deadline user stays on_hold, got %s", gotPend.ComputeStatus())
	}
	gotUsed, _ := st.GetUser("u-used")
	if gotUsed.ComputeStatus() != "active" {
		t.Errorf("already-used user stays active, got %s", gotUsed.ComputeStatus())
	}
}
