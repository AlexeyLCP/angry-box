package chain

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ─── Node utilities ("spinal cord" bundle) ───────────────────────────────────
// The orchestrator is the only writer of utility state; nodes keep no local
// state beyond the installed files. Lock discipline (AGENTS #2): these
// helpers call GetNodeInfo / SaveNodeInfo SEQUENTIALLY — never from inside a
// locked section.

// GetUtilityState returns the state of a named utility on a node, or nil when
// the utility has never been touched on that node. A missing NodeInfo record
// means "no utilities yet" (same error-swallowing convention as
// SaveNodePosition) — only a corrupt store surfaces as an error via
// GetNodeInfo's non-not-found paths.
func (s *Store) GetUtilityState(nodeID, name string) (*model.UtilityState, error) {
	info, err := s.GetNodeInfo(nodeID)
	if err != nil {
		return nil, nil
	}
	return model.FindUtility(info.Utilities, name), nil
}

// SetUtilityState upserts the state of a named utility on a node, creating
// the NodeInfo record if none exists yet (mirrors SaveNodePosition).
func (s *Store) SetUtilityState(nodeID string, u *model.UtilityState) error {
	if u == nil || u.Name == "" {
		return fmt.Errorf("store: utility state requires a name")
	}
	info, err := s.GetNodeInfo(nodeID)
	if err != nil {
		info = &model.NodeInfo{}
	}
	info.ID = nodeID
	replaced := false
	for i, ex := range info.Utilities {
		if ex != nil && ex.Name == u.Name {
			info.Utilities[i] = u
			replaced = true
			break
		}
	}
	if !replaced {
		info.Utilities = append(info.Utilities, u)
	}
	return s.SaveNodeInfo(info)
}

// UtilityIsStale reports whether the named utility's pushed artifacts lag the
// current store revision ("last config wins" staleness check). A utility that
// is not installed is never stale. Stamps are only meaningful for ARTIFACT
// utilities (sub payloads, Caddyfiles) — binary installs (caddy/acme) track
// Version instead. The pipeline stamps u.Revision = GetRevision() BEFORE
// saving the state; SetUtilityState's own write bumps the revision by one
// more, so freshness tolerates exactly that +1 (anything newer means a real
// store change landed after the push).
func (s *Store) UtilityIsStale(nodeID, name string) (bool, error) {
	u, err := s.GetUtilityState(nodeID, name)
	if err != nil {
		return false, err
	}
	if u == nil || !u.Installed || u.Revision == 0 {
		return false, nil
	}
	return u.Revision+1 < s.GetRevision(), nil
}
