package chain

// errors.go — sentinel errors for the chain/store package. Callers can use
// errors.Is(err, chain.ErrHostNotFound) to programmatically distinguish
// not-found cases from other errors (e.g. SSH timeout, rollback failure,
// permission denied) instead of string-matching. CTO-review §2 finding: the
// package previously exported zero sentinel errors, so no caller could tell
// "host not found" from "SSH timeout" programmatically.
//
// The sentinels wrap the descriptive fmt.Errorf messages the store already
// produces: the store returns `fmt.Errorf("store: host %q not found: %w", id,
// ErrHostNotFound)` so the human-readable message is preserved AND the
// sentinel is matchable via errors.Is.

import "errors"

// ErrHostNotFound is returned by GetHost/DeleteHost and friends when the
// requested host ID does not exist in the store.
var ErrHostNotFound = errors.New("host not found")

// ErrChainNotFound is returned by GetChain/DeleteChain when the requested
// chain name does not exist in the store.
var ErrChainNotFound = errors.New("chain not found")

// ErrUserNotFound is returned by GetUser/DeleteUser when the requested user
// ID does not exist in the store.
var ErrUserNotFound = errors.New("user not found")