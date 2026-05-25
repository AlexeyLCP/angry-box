package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA.
// All requests that don't match a static file fall back to index.html.
func Handler() http.Handler {
	sub, _ := fs.Sub(distFS, "dist")
	return &spaHandler{fs: http.FS(sub)}
}

type spaHandler struct {
	fs http.FileSystem
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	// Try to open the exact file first
	f, err := h.fs.Open(path)
	if err == nil {
		f.Close()
		http.FileServer(h.fs).ServeHTTP(w, r)
		return
	}

	// SPA fallback: serve index.html for all non-file routes
	r.URL.Path = "/"
	http.FileServer(h.fs).ServeHTTP(w, r)
}
