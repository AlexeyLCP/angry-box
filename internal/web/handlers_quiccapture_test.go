package web

import (
	"net/http"
	"net/url"
	"testing"
)

// NOTE: the chain-form CPS tests (quic-live mimicry, capture domain edit/reset)
// were removed in the v0.8 IA refactor: the chain form no longer edits CPS
// fields — the entry INBOUND PROFILE owns the obfuscation material (migrated
// chains keep their captured I1-I5 on the materialized inbound). The live-
// capture UI returns on the Inbounds page (follow-up). The capture-preview
// endpoint itself stays and is still covered below.

func TestHandler_CapturePreview_RejectsEmptyDomain(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/chains/capture-preview", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Invalid capture domain")
}
