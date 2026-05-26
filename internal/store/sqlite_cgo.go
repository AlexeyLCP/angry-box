//go:build sqlite_cgo

package store

import (
	_ "github.com/mattn/go-sqlite3"
)
