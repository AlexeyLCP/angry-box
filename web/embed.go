package web

import "embed"

// StaticFS holds embedded static assets (CSS, JS, images).
// In production mode these are compiled into the binary.
// In dev mode the filesystem under web/static/ is used instead.
//
//go:embed static/css/* static/js/* static/img/*
var StaticFS embed.FS
