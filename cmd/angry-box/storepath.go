package main

// storepath.go — serve-time store-path helpers (upgrade warning).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexeylcp/angry-box/internal/config"
)

// warnLegacyStore prints a one-time upgrade hint when the operator is running
// on the canonical default store (i.e. did NOT pass --file / set store_file),
// that canonical store does not yet exist, but a legacy CWD-relative store.json
// is present in the working directory. This is the upgrade path from versions
// that stored next to the binary.
//
// No auto-migration: the store is at-rest encrypted and its sibling .key must
// move with it; copying blind (or leaving the key behind) is unsafe. We just
// point the operator at the right manual step.
func warnLegacyStore(storePath string) {
	// Only relevant on the canonical default — an explicit --file is the
	// operator's deliberate choice, leave it alone.
	if storePath != config.DefaultStorePath() {
		return
	}
	if _, err := os.Stat(storePath); err == nil {
		return // canonical store already exists, nothing to warn about
	}
	// Look for a legacy store.json in the current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	legacy := filepath.Join(cwd, "store.json")
	if _, err := os.Stat(legacy); err != nil {
		return // no legacy store in CWD
	}
	legacyKey := legacy + ".key"
	fmt.Fprintf(os.Stderr,
		"WARNING: a legacy store.json exists in the current directory (%s) but the canonical default store is %s (and is empty/absent there).\n"+
			"  To keep your existing nodes/users, copy the store AND its key to the canonical location:\n"+
			"    cp %s %s\n", legacy, storePath, legacy, storePath)
	if _, err := os.Stat(legacyKey); err == nil {
		fmt.Fprintf(os.Stderr, "    cp %s %s\n", legacyKey, storePath+".key")
	}
	fmt.Fprintf(os.Stderr,
		"  Or run with an explicit --file to keep using the legacy location. "+
			"(No auto-migration: the store is encrypted and its .key must move with it.)\n")
}
