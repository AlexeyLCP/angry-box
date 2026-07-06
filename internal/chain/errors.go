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

// ErrRollbackFailed is returned by the deploy path (pushConfig / pushConfigWithAWG)
// when the config push or service restart failed AND the rollback to the
// previous config also failed. Callers can use errors.Is(err, ErrRollbackFailed)
// to distinguish "node left in a broken state" (needs manual intervention)
// from "deploy failed but node is still running the old config" (retry-able).
// CTO-review §6 finding: the deploy path returned a fmt.Errorf string, so no
// caller could programmatically tell a recoverable failure from a node left
// in a broken state — a critical operational distinction.
var ErrRollbackFailed = errors.New("rollback failed")

// ErrDeployFailed is returned by the deploy path when the config push or
// service restart failed but the rollback succeeded (the node is still
// running the previous, known-good config). Pair with ErrRollbackFailed:
// errors.Is(err, ErrDeployFailed) && !errors.Is(err, ErrRollbackFailed) →
// retry-able; errors.Is(err, ErrRollbackFailed) → node broken, page operator.
var ErrDeployFailed = errors.New("deploy failed")