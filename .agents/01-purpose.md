# 01 — Purpose

Extracted from AGENTS.md. This file is project law.

---

## Core Philosophy

1. **Orchestrator Pattern:** Angry-box is a central orchestrator. It does NOT route traffic itself. It generates configurations centrally and pushes them to remote nodes via SSH.
2. **Node-First Architecture:** The Node (`model.NodeInfo`) is the primary standalone entity. A user can run a node perfectly fine without any chains.
3. **Chains as an Overlay:** Chains (`model.Chain`) are an optional overlay to link nodes together. Chains generate "transport inbounds" under the hood, but these should not permanently overwrite a node's standalone configuration.
4. **Declarative State:** The `internal/chain/store.go` is the single source of truth. The `Applier` reads the state, generates a sing-box config, and forces the remote server into that state.

### Product Focus (scope is frozen — do NOT expand it)

**Ship first:** AWG (kernel + balancer; + AWG 3.0 header-protection as opt-in per-inbound, v0.8.10), VLESS+Reality+XHTTP (inter-node transport + standalone), MTProxy/Telemt, NaiveProxy + Mieru (standalone inbound).

**Paused (do NOT implement, test, or expose in UI for new configs):**
- **TUIC** — user entry + standalone (QUIC/TLS cert hassle + unresolved bugs; .agents/04-current-state.md #6).
- **Hysteria2** — transport + standalone + user entry (same QUIC/TLS cert class as TUIC; builder not written; .agents/04-current-state.md #11/#13).

Existing store entries that already use TUIC/Hysteria2 may remain for display/edit, but new chains/inbounds must be rejected (`internal/chain/frozen.go`).

Remember: this is a premium centralized orchestrator. Code stays clean, UI stays fast, remote nodes are disposable execution environments dictated by orchestrator state.
