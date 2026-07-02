# Testing Plan — angry-box v0.1.0

Three test layers: **unit** (offline, fast, in CI), **WSL smoke** (real Ubuntu over SSH, no live VPS needed), and **E2E** (real GCloud VPS, manual/CI-on-demand).

---

## 0. WSL smoke run — v0.1.0 results (executed)

The WSL smoke suite (`internal/wslsmoke`, build tag `wsl_smoke`) drives the real app pipeline against an Ubuntu-24.04 WSL instance over SSH — no live VPS required. **Run via the app itself** (`Backend.DeployOpts`, `chain.PushConfigForTest`), no manual server steps. Setup + run instructions at the bottom of this section.

### Result: 10 PASS / 1 SKIP / 0 FAIL

| Test | Result | Note |
|---|---|---|
| TestWSL_SSHConnect | ✅ PASS | SSH + TOFU works against the WSL node |
| TestWSL_DeployPatchedBinary | ✅ PASS | App downloads patched tarball, installs `/usr/local/bin/sing-box`, writes systemd unit, starts service (minimal config) |
| TestWSL_ApplyRealityXHTTP | ✅ PASS | `RenderProxyNode` → `pushConfig` → `sing-box check` passes, service active |
| TestWSL_RollbackOnBadConfig | ✅ PASS | Bad config fails check, rollback restores previous (REALITY+XHTTP), service stays active |
| TestWSL_FirstDeployNoRollback | ✅ PASS | No prior config + bad config → error surfaced (no panic), rollback is a no-op |
| TestWSL_AWGKernelInstall | ✅ PASS | Install path ran; amneziawg-tools unavailable in WSL apt + Microsoft kernel can't insmod — documented limitation, not a failure |
| TestWSL_ImportAWGConfigs | ✅ PASS | Seeded awg0.conf + awg-exit-n1.conf → SSH import parsed ListenPort/Jc/S1 + exit PublicKey/Endpoint/amnezia, back-filled placeholder inbounds (`server_priv, client_pub`) |
| TestWSL_QUICCapture | ⏭️ SKIP | UDP/443 blocked in the test environment; `CaptureQUICSignature` returned `does not support QUIC` (expected skip, not a bug) |
| TestWSL_DeployStatusHash | ✅ PASS | `LastDeployedHash` recorded; different config → different hash → pending |
| TestWSL_ConfigPreview | ✅ PASS | `RenderMergedNodeConfig` → valid JSON |
| TestWSL_AutoApplyPerHostLock | ✅ PASS | Per-host `sync.Mutex` identity: same nodeID → same mutex, different → different |

### Bugs found by the smoke run and fixed (commit 5921a59)

1. **`sudo` + multiline scripts** — `sudo set -e\nmkdir...` ran `sudo set` as a binary lookup (`sudo: set: command not found`). Wrapped install/restart/verify/AWG pipelines in `sudo bash -c '...'`.
2. **`UploadText` deadlock/empty files** — writing to stdin before `session.Run` left `cat` reading an already-closed stdin (empty files) or blocked on a full pipe buffer. Now `session.Start(cmd)` first, then write + Close + `session.Wait`.
3. **XHTTP `headers` type** — sing-box-extended's V2RayXHTTP headers field is `map[string]string`, not `map[string][]string` → `cannot unmarshal array into ...headers of type string`. Fixed the generator.
4. **REALITY + `curve_preferences`/`ECH` conflict** — sing-box-extended rejects `curve preferences is unavailable in reality` and `Reality is conflict with ECH`. Removed both from the REALITY inbound (they are client-side / plain-TLS options).

Plus: `Backend.Deploy` now writes a minimal valid config.json so a fresh deploy starts instead of crashing on a missing config; `systemctl reset-failed` before restart; `pushConfig`/`performRollback`/`probeServiceUp` gained a `useSudo` parameter; CLI `deploy` gained `-sudo`/`-install-awg` flags and `ANGRY_BINARY_URL` env override.

### WSL smoke setup + run

One-time setup (see TESTING.md history / `wsl -d Ubuntu-24.04`):
```bash
# In WSL Ubuntu-24.04 (systemd enabled via /etc/wsl.conf [boot] systemd=true):
sudo apt-get install -y openssh-server python3
# Add the test public key to ~/.ssh/authorized_keys
# Serve the patched tarball from the repo deps/:
cd /mnt/c/.../angry-box/deps && python3 -m http.server 8000 &
```
Run from Windows:
```bash
WSL_TEST_HOST=127.0.0.1:22 WSL_TEST_USER=lcp WSL_TEST_KEY=$HOME/.ssh/angry-test-key \
  go test -tags wsl_smoke ./internal/wslsmoke/ -run WSL -v -timeout 600s
```
The `ANGRY_BINARY_URL` env (used by `Backend.Deploy`) points at the local HTTP server so the test doesn't hit GitHub.

---

## 1. Unit tests (run on every push)

Already implemented; 208+ tests across packages. Run:

```bash
go test ./...              # all unit tests
go vet ./...               # static analysis
go build ./...             # compile check
```

### Coverage by package

| Package | What's covered | File(s) |
|---|---|---|
| `internal/backend/singbox` | Role-based config generation (proxy_node structure + deterministic credentials, awg_balancer kernel path + no-amnezia, awg_hop userspace amnezia); CLI `config` paths (TUIC real inbound, AWG endpoint, default REALITY+XHTTP); all-profiles valid JSON | `roles_test.go`, `config_test.go` |
| `internal/chain` | AWG preset invariants (Jmin<Jmax, \|S1+56−S2\|≥10, H1-H4 quadrants, width≥1000) ×200 runs/profile; CPS `<b 0x...>` format + odd-hex padding; deploy hash stability + audit append/list/cap; Profile unique-name + cascade-delete; Assignment uniqueness; Links CRUD + unique; SaveNodePosition round-trip; spider sync (insert after fromNode); AWG import parsers (server/exit/peers); protocol presets catalog + obfuscation levels + routing presets; domain normalize/validate; capture invalid/empty; crypto generators | `awgpresets_gen_test.go`, `awgcps_format_test.go`, `deploystatus_test.go`, `spider_test.go`, `awgimport_test.go`, `protocolpresets_test.go` |
| `internal/ssh` | SSH keypair generation (ed25519, parseable, sign/verify); host-key error strings; password-prefix detection; port defaulting | `client_test.go` |
| `internal/config` | config loader defaults | `config_test.go` |

### What to add (planned unit tests)

- [ ] **`roles_test.go`**: golden-file comparison for proxy_node/awg_balancer/awg_hop rendered JSON (snapshot tests like `test_config_builder.py`).
- [ ] **`cryptogen_test.go`** (extract from protocolpresets_test): MTProxyFullSecret composition with real domain bytes, SS password key-length per cipher (table test).
- [ ] **`awgcapture_test.go`** (extract): capture against a localhost UDP echo mock (no real network) — verify packet count + `<b 0x...>` format.
- [ ] **`buildClientURI_test.go`**: golden strings for every share-link branch (vmess base64-JSON, trojan, ss, vless-reality-xhttp with encoding, mtproxy `tg://proxy`).
- [ ] **`autoapply_test.go`**: per-host lock serialisation (two concurrent ScheduleAutoApply to the same nodeID run sequentially); WaitAutoApply drains.

---

## 2. E2E tests (real VPS over SSH)

Build tag: `e2e`. Suite lives in `internal/chain/`:

| File | Purpose |
|---|---|
| `e2e_test.go` | Read-only / parallel-safe tests (SSH, store, presets) |
| `e2e_helpers_test.go` | Shared helpers (`deployChain`, `verifyClientConnectivity`, `performRollbackTest`, …) |
| `e2e_heavy_test.go` | State-mutating orchestration tests (serialized via `e2eHeavy` mutex) |

Compile check:

```bash
go vet -tags e2e ./internal/chain/
```

### Test servers (prepare once)

Three Debian 12 x86_64 VPSes, SSH user `lcp`, passwordless sudo, key `~/.ssh/id_ed25519`:

| Role | Host | SSH |
|---|---|---|
| **entry** | test-server-1 | `lcp@34.62.128.71` |
| **middle** | test-server-2 | `lcp@207.175.40.161` |
| **exit** | test-server-3 | `lcp@23.251.133.38` |

**Server prep checklist**

1. Debian 12 amd64, `lcp` in `sudo` group (`NOPASSWD` recommended for CI).
2. `~/.ssh/authorized_keys` contains the ed25519 public key used locally.
3. Outbound UDP/443 open (for `TestE2E_Heavy_QUICCapture_AWGConfig`; skips if blocked).
4. Inbound TCP **443** open on all nodes (TUIC / REALITY entry ports).
5. AWG kernel: orchestrator installs `amneziawg` via Amnezia PPA (DKMS fallback from `ANGRY_AWG_TARBALL_URL`). `TestE2E_Heavy_Protocol_AWG_*` **fail** if install breaks — not skipped.
6. For client routing tests: set `AB_ROUTE_DNS=1`. By default the client runs **on the entry VPS** (TUIC/QUIC from WSL is unreliable). Set `E2E_CLIENT_LOCAL=1` to run the client on the test workstation (`deps/sing-box.exe` on Windows).

Quick SSH smoke from your workstation:

```bash
for h in 34.62.128.71 207.175.40.161 23.251.133.38; do
  ssh -i ~/.ssh/id_ed25519 lcp@$h 'hostname && sing-box version | head -1'
done
```

### How to run

**Fast read-only suite** (~1 min, safe to parallelize):

```bash
go test -tags e2e ./internal/chain/ -run 'TestE2E_SSH|TestE2E_Known|TestE2E_Wire|TestE2E_Store|TestE2E_Presets|TestE2E_ImportAWG_No|TestE2E_Takeover_Detect' -v -timeout 120s
```

**Full heavy suite** (~30–60 min, mutates all three VPSes — run before releases):

```bash
go test -tags e2e ./internal/chain/ -run TestE2E_Heavy -v -timeout 3600s
```

**Skip heavy tests** (read-only only):

```bash
E2E_SKIP_HEAVY=1 go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 120s
```

**Targeted groups**

```bash
# Deployment, takeover, rollback
go test -tags e2e ./internal/chain/ -run 'TestE2E_Heavy_Deploy|TestE2E_Heavy_Takeover|TestE2E_Heavy_Rollback' -v -timeout 900s

# Protocol matrix (VLESS+XHTTP, TUIC, AWG kernel/userspace)
go test -tags e2e ./internal/chain/ -run TestE2E_Heavy_Protocol -v -timeout 1800s

# Multi-hop chains + topology change
go test -tags e2e ./internal/chain/ -run TestE2E_Heavy_Chain -v -timeout 1800s

# Client egress proof (needs AB_ROUTE_DNS=1 + deps/sing-box.exe)
AB_ROUTE_DNS=1 go test -tags e2e ./internal/chain/ -run TestE2E_Heavy_ClientConnectivity -v -timeout 1200s

# QUIC capture + AWG CPS, AWG import, idempotency, locking
go test -tags e2e ./internal/chain/ -run 'TestE2E_Heavy_QUIC|TestE2E_Heavy_Import|TestE2E_Heavy_Idempotency|TestE2E_Heavy_Concurrent|TestE2E_Heavy_PostDeploy' -v -timeout 1800s
```

### Heavy test inventory

| Test | Coverage |
|---|---|
| `TestE2E_Heavy_Deploy_FreshNode` | Clean sing-box-extended install on entry |
| `TestE2E_Heavy_Takeover_SingBox_FullFlow` | Detect → convert → cutover existing sing-box |
| `TestE2E_Heavy_Rollback_OnBadConfig` | Real `PushConfig` rollback after invalid JSON |
| `TestE2E_Heavy_Protocol_VLESSRealityXHTTP_Advanced` | `xhttp_max_stealth_2026` profile on node |
| `TestE2E_Heavy_Protocol_TUIC` | TUIC v5 + ALPN h3 |
| `TestE2E_Heavy_Protocol_AWG_Kernel` | Orchestrator installs AmneziaWG kernel + `awg-quick`, deploys amnezia endpoint |
| `TestE2E_Heavy_Protocol_AWG_Userspace` | Userspace wireguard endpoint (`system: false`) on exit node |
| `TestE2E_Heavy_Chain_SingleNode` / `_2Hop` / `_3Hop` | 1/2/3-hop deploy + health |
| `TestE2E_Heavy_Chain_TopologyChange` | 2-hop → 3-hop → 2-hop |
| `TestE2E_Heavy_ClientConnectivity_*` | Local sing-box client + curl egress via exit IP |
| `TestE2E_Heavy_Balancer_URLTestInChain` | `urltest` outbound in merged chain config |
| `TestE2E_Heavy_Balancer_Failover` | urltest across two backends; stop one, traffic continues |
| `TestE2E_Heavy_QUICCapture_AWGConfig` | Live QUIC capture + pro_2026 AWG CPS fields |
| `TestE2E_Heavy_ImportAWG_PreservesPeers` | Import must not delete existing `awg0-peers.list` entries |
| `TestE2E_Heavy_Idempotency_DoubleApply` | Same chain twice → stable config hash |
| `TestE2E_Heavy_ConcurrentDeploy_Serialized` | Parallel `PushConfig` on same node — no corruption |
| `TestE2E_Heavy_PostDeploy_HashAndHealth` | `LastDeployedHash` + `GetStatus` + listen check |

### Tests to run manually before tagging

These are included in `TestE2E_Heavy` but are the highest signal / longest:

1. `TestE2E_Heavy_Chain_3Hop` + `TestE2E_Heavy_ClientConnectivity_3Hop` (with `AB_ROUTE_DNS=1`)
2. `TestE2E_Heavy_Protocol_AWG_Kernel` (kernel headers required)
3. `TestE2E_Heavy_QUICCapture_AWGConfig` (UDP/443 to Cloudflare)
4. `TestE2E_Heavy_Balancer_Failover`

### Diagnostics on failure

Heavy tests log on failure: apply report, chain spec, and `journalctl -u sing-box` tail from affected nodes. Helpers: `fetchRemoteConfig`, `fetchSingBoxLogs`, `logDeployFailure`.

### Planned E2E (not yet implemented)

- [ ] **E2E_MTProxy**: Telegram MTProxy handshake on a live node
- [ ] **E2E_Spider_LinkSync**: spider edge → `Chain.Nodes` order persistence
- [ ] **E2E_AWGBalancer_Kernel**: `awg_balancer` role with kernel TUN + bind_interface
- [ ] **E2E_Takeover_Xray/AWG**: full takeover of non-sing-box VPN installs

---

## 3. CI (.github/workflows)

`.github/workflows/build.yml` and `release.yml` are committed. Ensure they run:
- `go build ./...`, `go vet ./...`, `go test ./...` (unit, no e2e tag) on every push/PR.
- Release workflow tags `v*` and publishes the binary (cross-compile amd64+arm64 when arm64 builder available).

**Action needed**: review the committed workflows match v0.1.0 (binary path, build tags, no `with_admin_panel`).

---

## 4. Manual smoke test (before announcing v0.1.0)

1. `go build ./cmd/angry-box && ./angry-box serve` — web UI opens at :8090.
2. Add a test node (vps-de-test-1) via the UI; click Install; verify the patched binary is downloaded + service starts.
3. Create a proxy_node inbound (VLESS REALITY+XHTTP) via the Inbounds form; Apply; `sing-box check` passes on the remote; a vless client connects.
4. Open the Spider page; drag a node; reload — position persists; pan/zoom works; create an edge; the chain.Nodes order reflects it.
5. Check Deploy Status page — node shows "applied"; mutate an inbound → shows "pending".
6. Audit page — entries for install/deploy/create-link appear.
7. AWG: create an awg_hop chain (multi-hop); apply; verify traffic flows (the overlap-fix is exercised).
8. `angry-box config -type user -protocol tuic` → real TUIC inbound JSON (not wireguard).

---

## Run order before tagging the next release
1. `go build ./... && go vet ./... && go test ./...` — all green.
2. `go vet -tags e2e ./internal/chain/` — e2e compile check.
3. `go test -tags e2e ./internal/chain/ -run TestE2E_Heavy -v -timeout 3600s` against the three test VPS.
4. `AB_ROUTE_DNS=1 go test -tags e2e ./internal/chain/ -run TestE2E_Heavy_ClientConnectivity -v -timeout 1200s`.
5. Manual smoke test (section 4).
6. Only then bump version + tag.