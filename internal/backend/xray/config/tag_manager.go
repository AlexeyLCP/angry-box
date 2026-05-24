package config

import "fmt"

const lucxPrefix = "lucx-"

// TagManager generates and manages LucX-namespaced tags.
// All LucX-managed config entries use tags with prefix "lucx-{chainID}-".
type TagManager struct {
	ChainID string
}

func NewTagManager(chainID string) *TagManager {
	return &TagManager{ChainID: chainID}
}

// Entry returns the tag for the entry node inbound.
func (tm *TagManager) Entry() string { return fmt.Sprintf("%s%s-entry", lucxPrefix, tm.ChainID) }

// Hop returns the tag for a hop node inbound.
func (tm *TagManager) Hop(pos int) string { return fmt.Sprintf("%s%s-hop%d", lucxPrefix, tm.ChainID, pos) }

// Exit returns the tag for the exit node inbound.
func (tm *TagManager) Exit() string { return fmt.Sprintf("%s%s-exit", lucxPrefix, tm.ChainID) }

// HopTo returns the outbound tag for routing from one node to the next.
func (tm *TagManager) HopTo(fromPos int) string {
	return fmt.Sprintf("%s%s-hop%d-to-%d", lucxPrefix, tm.ChainID, fromPos, fromPos+1)
}

// IsLucX checks if a tag belongs to LucX (starts with "lucx-").
func IsLucX(tag string) bool { return len(tag) >= 5 && tag[:5] == lucxPrefix }

// LucXTagPattern is used in Xray routing rules to match all LucX tags.
const LucXTagPattern = "lucx-"
