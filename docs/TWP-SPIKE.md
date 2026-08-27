# TWP — Telegram Web Proxy (design spike)

Status: **research / not implemented.** This is the Phase-5 spike deliverable:
a concrete design + a build plan for a new user-entry protocol that disguises
proxy traffic as Telegram Web (web.telegram.org) traffic. Nothing here is
coded yet; the goal is to de-risk the two big unknowns before any fork work.

## Why

Every protocol we ship today (AWG, Reality+XHTTP, MTProxy, naive/mieru/
TrustTunnel) has a distinct handshake fingerprint. A censor that has learned
to fingerprint them needs a new evasion shape. TWP aims to look like the most
legitimate, high-volume, hard-to-block flow on the network: a browser talking
to Telegram's own web client. Blocking it cleanly means blocking Telegram Web
itself — a politically expensive move.

This is a **user-entry** protocol (client → entry node), in the same slot as
VLESS-Reality / naive / TrustTunnel. It is NOT an inter-node transport.

## The two open questions (must answer before coding)

1. **Fidelity.** What does a real web.telegram.org session actually look like
   at the TLS/HTTP layer — ALPN, HTTP/2 vs HTTP/3, request paths, header
   order, the MTProto-over-WebSocket/HTTP-transport framing? We need a packet
   capture of a genuine session to imitate. Without it we are guessing and
   the disguise is worthless.
2. **Client.** There is no off-the-shelf client that speaks "Telegram Web"
   framing to a third-party server. We either (a) ship a tiny sing-box
   outbound + a client config (our normal path), or (b) rely on an existing
   client. If (a), the outbound must live in the amnezia-box fork — same
   PATCHES.md rebase discipline as every other fork feature.

If either answer is "we can't make it look real" or "there's no viable
client", **stop** — a half-disguised protocol is worse than none (it adds a
fingerprint the censor can learn for free).

## Proposed shape (pending the fidelity answer)

- Transport: TLS 1.3, ALPN `h2`, HTTP/2 CONNECT-style tunnel to a
  web.telegram.org-mimicking host header. Reuses the acme SAN cert machinery
  from Phase 1 (`CertPaths`) — TWP is one more TLS-utility protocol fronted by
  the caddy SNI router on its own subdomain (`twp.<domain>`).
- Auth: per-user token in the initial request (maps to `User.*Username/
  Password` style creds like naive/mieru/trusttunnel — `EnsureUserCreds`).
- Fake site: required, exactly like the other TLS-utility protocols — the
  domain must serve a plausible page to probes (Phase-1 fakesite utility).

## Integration plan (once the spike validates)

1. **Fork** (`AlexeyLCP/amnezia-box`): implement the `twp` inbound + outbound
   behind a build tag (`with_twp`), commit to the fork tree, rebuild via
   `scripts/build-singbox.sh`, bump `singBoxVersion` + checksum + patchcheck
   ref — the full `docs/PATCHES.md` procedure. This is the expensive step.
2. **angry-box types**: add `twp` to the standalone protocol allowlist
   (`web/inbounds.go` switch + `inbounds.templ` option), a `case "twp"` in
   `buildStandaloneInOut` (merged_config.go) rendering TLS via `CertPaths`,
   and a `case "twp"` share-link builder in `buildClientURI` (users.go).
3. **Caddy + deps**: add `"twp"` to `chain.TLSUtilityProtocols` so the SNI
   router + `ValidateUtilityDeps` gating pick it up automatically; the Phase-1
   machinery then fronts it with zero extra code.
4. **Subscription**: the link flows through `collectUserLinks` → all formats
   (base64/clash/singbox/html) with no new work.
5. **Frozen list**: TWP is NOT added to `frozen.go` (that deny-list is for
   paused work; TWP ships only when ready).

## Effort estimate

- Fidelity capture + client feasibility: 1 focused session (go/no-go gate).
- Fork implementation + rebase: the long pole — same class as the naive/mieru/
  TrustTunnel port (PROGRESS §50/§52).
- angry-box wiring: small, mechanical (steps 2–4 above), well-trodden paths.

## Decision gate

Do NOT start the fork work until a real-client capture exists and we have
confirmed the framing can be reproduced. Record the go/no-go here.
