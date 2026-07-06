package web

// htmlx.go provides HTML-escaping helpers for the raw-HTML views that still
// render through simpleHTML (string-built HTML written verbatim to the
// response). Unlike templ components — which escape interpolations
// automatically — simpleHTML writes its payload as-is, so any user-supplied or
// remote-derived text interpolated into such views must be escaped first to
// prevent stored XSS (CTO-review H1).
//
// escHTML delegates to the stdlib html.EscapeString, which escapes the five
// significant characters (`"`, `&`, `'`, `<`, `>`). It is the minimal,
// dependency-free escaping contract these views need.

import "html"

// escHTML returns a HTML-safe representation of s, suitable for interpolation
// into raw HTML markup. It is a thin wrapper over html.EscapeString so the
// behavior matches what templ would produce automatically.
func escHTML(s string) string {
	return html.EscapeString(s)
}

// shortHex returns s if its length is within n bytes, else s truncated to n
// bytes followed by an ellipsis. Used to preview long hex strings (e.g. AWG
// CPS I1-I5 packets) inline without overflowing the UI. Byte-based truncation
// is safe here because the inputs are ASCII hex strings.
func shortHex(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}