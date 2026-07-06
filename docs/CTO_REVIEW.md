# `angry-box` CTO Review — 2026-07-06

## Executive Summary

Angry-box — рабочий Go-оркестратор (v0.2.0): генерит sing-box-extended конфиги централизованно и пушит на ноды по SSH с TOFU + graceful rollback. Кодовая база **компилируется и проходит все unit-тесты чисто** (8 ok / 0 FAIL, 632 PASS); e2e на live GCloud VPSes верифицирован (AWG kernel handshake, 2-hop forwarding, full client→internet egress). Подпроекты A (SSH-key UX + баг деплоя), B (унификация клиентов), C2 (QUIC capture UI), C1 (per-protocol пресеты + удаление мёртвых Profiles) — завершены, всё в `main`. Архитектура здоровая: чистое разделение слоёв, persistent transit keys, atomic rollback, frozen-protocol enforcement, kernel-AWG rework live-verified.

Главные риски для перехода к v0.3.0/production: **(1)** отсутствие CI/CD (`.github/workflows/` нет, при false claim в PR-description); **(2)** секреты (SSH privkeys, AWG/Reality/TLS приватные ключи) хранятся в `store.json` в открытом виде — кража файла = компрометация всего флота VPS; **(3)** `mustMarshal`/`cryptogen.Generate*` паникуют в request-path и роняют оркестратор при одном malformed deploy; **(4)** systemd `User=root` + `install.sh`/service bind `0.0.0.0:9080` plain HTTP — control plane экспонирован в сеть без TLS под root; **(5)** store.json без schema versioning — миграции ручные one-shot. Готовность ~**68% app-ready**; до production-grade нужен шифр-секретов-at-rest, CI, вынос паник-генераторов в error-возврат, TLS по умолчанию.

## Application Production Readiness

| Пакет | % Ready | Критические блокеры |
|---|---|---|
| `cmd/angry-box` (CLI) | 75% | `_ = config.Load` молча игнорирует ошибку → битый TOML сбрасывает на defaults; `fmt.Print*` допустим (CLI); нет CI |
| `internal/config` | 70% | Нет валидации значений; `_ = cfg.Save` молча; первый admin-пароль в лог (plaintext); битый TOML → тихий fallback (CRITICAL) |
| `internal/web` (HTTP) | 82% | Нет package doc; смешанный `log`/`slog`; MTProxy secret без escHTML в одном value-attr; errcheck-off скрывает ~5 `_ =` |
| `internal/chain` (applier/store) | 78% | `mustMarshal` panic в deploy-path (CRITICAL); `cryptogen.Generate*` panic в request-path (CRITICAL); `_ = writeStore` в NewStore; takeover `_ = RestoreFile` ломает rollback (#7); 0 sentinel errors |
| `internal/chain/presets` | 65% | `init()` panic на старте всего процесса при любом баге в preset-JSON |
| `internal/backend/singbox` | 75% | `mustMarshal` panic; `_ = err` ×3 (singbox.go:141,187,679) — silent error swallowing |
| `internal/ssh` | 88% | TOFU корректен; `errors.As` используется; cleanup `_ =` обоснован; key material не zeroize'ся (Go limit, LOW) |
| `internal/takeover` | 60% | 4×`_ = json.Unmarshal` молча; `_ = RestoreFile` ломает rollback; `_ = SaveNodeInfo` ×4; cutover не atomic (downtime window) |
| `internal/domain/*` | 90% | Чистые модели, без логики |
| `internal/i18n` | 85% | en/ru; нет package doc; **0 тестов**, асимметрия en ~302 / ru ~520 |
| `scripts/install.sh` | 70% | `rm -rf` без подтверждения store (теряет chains/hosts/SSH-keyrefs); bind `0.0.0.0` plain HTTP |
| `scripts/angry-box.service` | 55% | `User=root`; bind `0.0.0.0:9080` plain HTTP (нарушает security-default config.go loopback); нет hardening (NoNewPrivileges/ProtectSystem) |
| `packaging/keenetic/` | 85% | control+postinst корректны; `packaging/openwrt-aarch64/` заявлен в PR-desc, отсутствует |
| CI/CD | **0%** | `.github/workflows/` ОТСУТСТВУЕТ (PR_DESCRIPTION_v0.2.0.md falsely claims release.yml) |
| `.golangci.yml` | 80% | errcheck OFF (TODO 0.3.0) — пропускает ~60 silent failures |
| Документация (godoc) | 40% | 0 package-level doc-комментариев; AGENTS.md/PROGRESS.md актуальны (сильная сторона) |

**Итого (application-readiness): ~68%.**

## Top-10 блокеров перед v0.3.0

1. **CI полностью отсутствует** — нет build/vet/test/templ-generate-check/release. Все 4 подпроекта (A/B/C1/C2) написаны и протестированы вручную; regression-защита только через `go test ./...` локально. Срочно: минимальный GitHub Actions (build + vet + `make generate` no-diff + `go test ./...`).
2. **Секреты в store.json plaintext** — `SSHKeyEntry.KeyData`, `User.AWGPrivateKey`, `User.MTProxySecret`, Reality PrivateKey, `NodeInbound.TLSPrivateKey` хранятся как открытый JSON. Файл `0o600`, но не зашифрован. Кража файла/бэкапа = полный флот VPS. Нужно шифрование at-rest (master-key из admin password / отдельного unlock-key).
3. **Паники в request-path** — `mustMarshal` (roles.go:387) и `cryptogen.GenerateInboundTag/GenerateTUICPassword/GenerateProxyPassword` (cryptogen.go:119,141,184) вызываются из HTTP-handler/deploy-пути и `panic` роняют весь оркестратор. Должны возвращать `error`.
4. **Битый/отсутствующий TOML молча сбрасывает на defaults** (`main.go:113` `orchCfg, _ = config.Load(configPath)`) — оператор теряет `auth_password_hash`/`listen_addr` без сигнала. + TOML без `DisallowUnknownFields` → silent опечатки.
5. **systemd `User=root` + bind `0.0.0.0:9080` plain HTTP** (angry-box.service / install.sh / S99) — control plane (SSH privkeys, RCE-through-deploy) экспонирован в сеть без TLS, под root. Должно быть loopback + reverse proxy (TLS) + non-root user + hardening.
6. **`handleTrustHostKey` доверяет произвольному fingerprint из POST** (dashboard.go:41) без сверки с фактически наблюдённым `HostKeyError.RemoteFingerprint` — MITM-поддержка через UI (HIGH).
7. **Store без schema versioning** — миграции ручные one-shot (`migrateMtproxyUsers`). При накоплении 5+ migrations неуправляемо. Нужно `SchemaVersion` field + versioned migrators chain.
8. **context.Context не пробрасывается в SSH-push** (applier.go:1031,1044,1058 `context.Background()`) + Store-методы без ctx — UI-отмена deploy не отменяет SSH-таймауты; нет HTTP server timeouts (Slowloris-prone).
9. **i18n асимметрия без теста-стража** — en ~302 / ru ~520 ключей, 0 i18n-тестов. UI на en может рендерить raw-ключи для непереведённых строк.
10. **TUIC E2E активен вопреки AGENTS.md #6 FROZEN** (`e2e_heavy_test.go:115 TestE2E_Heavy_Protocol_TUIC` не skip) — нарушение собственного frozen-rule. + `buildMergedRoute`/`BuildDNSWithDetour` НЕ удалены (gated `AB_ROUTE_DNS=1`), AGENTS.md #2 формулировка «removed» неточна.

## Критические находки по безопасности

**CRITICAL**
- Приватные SSH-ключи + AWG/Reality/TLS секреты в `store.json` plaintext (panel.go:107, panel.go:51, panel.go:219). Кража файла = кража всего флота VPS. Шифрование at-rest отсутствует.

**HIGH**
- `handleTrustHostKey` (dashboard.go:41) доверяет произвольному fingerprint из POST без сверки с наблюдённым — MITM через UI.

**CVE:** `govulncheck` не установлен — автоматическая проверка не выполнена. По публичным данным: golang.org/x/crypto v0.52.0 (Terrapin fixed в v0.17.0+, covered), BurntSushi/toml v1.6.0 (CVE-2024-45337 fixed in v1.4.0, covered), a-h/templ v0.3.1020 (no known CVE), skip2/go-qrcode 2020-commit (no CVE, non-security). Критических unfixed нет, но `govulncheck` обязателен в CI.

## Архитектурные риски (Топ-5)

1. **Нет schema versioning в store.json** — каждое schema-изменение ручной one-shot migration. Нужно `SchemaVersion` + migrators chain.
2. **deps/sing-box-patched — single GitHub release URL** (`singbox.go:41`), нет mirror/backup. При удалении release все новые deploy падают. Tarball не в репо (только checksums + src.tar.gz).
3. **Legacy CLI `Backend.ApplyConfig` standalone-AWG** (`main.go:673` via `RenderAWGHop`) — userspace, расходится с kernel-AWG rework web-UI. Cognitive dissonance CLI vs UI. Deprecate/convert to `pushConfigWithAWG`.
4. **per-client `source_ip_cidr` под TUN-overlay не верифицирован на real VPS** (PROGRESS §7 review #1) — e2e skip stub. Если TUN NAT меняет peer inner IP, primary routing-механизм (AGENTS.md #7) архитектурно несовместим с kernel-AWG. Фундаментальная неопределённость.
5. **patches/ rebasing недокументирован** — sing-box-extended `1.13.14-extended-2.5.0` pinned, патчи не version-tagged, Reality SNI `www.cloudflare.com` hardcoded. При upstream bump — silent breakage (chacha20 overlap может conflict, fingerprint drift).

Дополнительно: `applier.go` 72KB/1969 строк (`ApplyChain` 329 строк) смешивает pure-config-gen + SSH I/O — нарушение AGENTS.md #4 layering, split на `applier_build.go`+`applier_push.go`; `buildMergedNodeConfig` 7 параметров → config-struct; `web/server.go` Register ~60 маршрутов в одном методе — split по ресурсам.

## Deploy pipeline и ресурсный бюджет

**Data path:** UI handler (synchronous, blocks HTTP request) → Store (sync.RWMutex, JSON re-marshal whole file per op) → ApplyChain (sequential node loop, N×latency) → SSH connect (15s timeout, no pool, fresh connection per deploy, ~8 sessions reused within one node) → push (cat>path via stdin, atomic) → systemctl restart → probeServiceUp (~7-9s floor) → rollback on failure (atomic для AWG: оба файла). Takeover: convert → backup old → push sing-box → disable old (non-AWG, downtime window) → rollback-to-old on fail.

**Bottlenecks:**
1. `probeServiceUp` ~7-9s/node — floor деплоя, доминирует над render.
2. Sequential `ApplyChain` node loop — N nodes = N× latency (для ordered multi-hop корректно, для independent-node applies — лимит).
3. Store re-reads/marshals whole file on every op — CPU/alloc hot spot на больших stores.
4. No HTTP server timeouts (Slowloris-prone, stuck deploy holds goroutine).
5. No SSH connection pool + no concurrency cap on autoapply — 100 simultaneous autoapply = 100 SSH connects + 100 fresh Stores (race risk на JSON file, mitigated only by atomic rename, not by read-modify-write serialization).
6. Pre-flight opens N SSH connections only to close them — waste.

### Ресурсный бюджет (цель: оркестрация десятков нод)

| Метрика | Оценка из кода | Бюджет | Статус |
|---|---|---|---|
| RAM (100 chains × 10 nodes, transient) | ~5-20 MB (full store re-read per op, не in-memory) | <200 MB | ✓ |
| CPU idle (autoapply event-driven, не polling) | <1% (SSH-wait bound) | <5% | ✓ |
| Deploy latency (1 нода) | ~10-20s (probe-dominated) | <30 s | ✓ |
| Concurrent deploys (50 distinct nodes) | 50-way via hostlock | serialized per-host | ✓ |
| Store.json marshal per op | ~10-50µs (grows O(file size)) | минимизировать | ~ (re-marshal whole file) |
| HTTP handler p99 | не измерено, но stuck-deploy risk | <100 ms | ✗ (no timeout) |
| autoapply на 100 нод (RAM) | ~50-250 MB transient | bounded | ~ (no cap) |

## Соответствие протоколам и sing-box-extended schema

Из ~40 проверенных контрольных точек **соответствует ~36**, отклонения/замечания — 5 (ни одно не критично для v0.2.x stack):
- SSH TOFU (CheckHostKey, HostKeyError, no bypass), Reality inbound/outbound (no amnezia/ECH/curve_preferences, XHTTP `map[string]string`), AWG kernel .conf (Table=off, MASQUERADE both subnets, rp_filter=0 PostUp, amnezia в [Interface] до [Peer], H1-H4 quadrants width≥1000), криптография (X25519/HKDF RFC 9001/WG/UUID/MTProxy, crypto/rand везде, no math/rand), port-conflict detection, frozen enforcement — **всё соответствует**.
- Замечания: (1) `xhttp_cps.go:55 GenerateRealisticHeaders` возвращает `map[string][]string` (потенциальное нарушение #4, но вне живого REALITY-пути); (2) `buildMergedRoute` НЕ удалён, gated `AB_ROUTE_DNS=1` — AGENTS.md #2 формулировка «removed» неточна; (3) AWG exit-client MTU default 1280 vs #10 fix 1420 — намеренно (double-encapsulation headroom); (4) H1-H4 degenerate в standalone без material (known gap); (5) `splitHostPort`/`splitEndpoint` дублируются (roles.go vs awg_server.go).
- Vendored sing-box: checksum verified at deploy (`checksumForArch` fail-closed), patches applied at build (`build-singbox.sh` git apply, НЕ на VPS), version consistent (1.13.14-extended-2.5.0-patched везде). AmneziaWG: DKMS build на VPS (PPA primary + bundled tarball fallback), gating на `UserProtocol==AWG || Transport==AWG`, deps документированы. Store default path inconsistency: `chains.json` (main.go) vs `store.json` (config.go/install.sh) — баг UX, не архитектуры.

## FROZEN enforcement (TUIC / Hysteria2)

`frozen.go` централизованно: `FrozenTransports{Hysteria2}`/`FrozenUserProtocols{TUIC}`/`FrozenStandaloneProtocols{tuic,hysteria2}` + `Validate*` guards. Wired во все 6 entry points: chain create/edit (`web/chains.go`), spider link (`web/spider.go`), standalone inbound add (`web/nodes.go` `IsFrozenStandaloneProtocol`), default protocol (`web/settings.go`). UI dropdowns: frozen options `<option selected disabled>` в chains.templ:307,318 / nodes.templ:381,388 / users.templ:238,245 / settings.templ:102. Edit-guard nuance (validate only on `!= c.Transport`/`!= c.UserProtocol`) — covered `TestHandler_UpdateChain_PreservedFrozenProtocol` + `TestHandler_UpdateChain_RejectsSwitchToFrozen`. Существующие store-записи остаются для display/edit. **Одно нарушение**: `TestE2E_Heavy_Protocol_TUIC` (`e2e_heavy_test.go:115`) активен, не skip — противоречит AGENTS.md #6 «do NOT run TUIC tests».

## TDD compliance

| Пакет | % покрытия pub API | Качество | Статус |
|---|---|---|---|
| cmd/angry-box | ~60% | B | ✓ |
| internal/chain (store/applier/merged_config/frozen/cryptogen) | ~85-95% | A | ✓ |
| internal/backend/singbox | ~85% | A | ✓ |
| internal/web | ~75% | B | ✓ |
| internal/ssh | ~80% | A | ✓ |
| internal/takeover | ~70% | B | ~ (cutover/rollback-to-old только e2e) |
| internal/i18n | 0% | D | ✗ |
| internal/config | ~90% | A | ✓ |

**Топ непокрытых:** i18n.T (0 тестов, no key-completeness check), takeover.Takeover orchestrator (нет изолированного unit, только e2e), takeover.rollbackToOldVPN (skip manual), applier rollback-failure path, store.readStore на битый JSON, singbox Backend.Remove/Reload, handleSaveSettings full path, audit SaveAuditLog/ListAuditLogs, handleUserConfig MTProxy tg:// links.

**Слабые тесты:** bare status-check без content assert (`TestHandler_SpiderWeb_Empty`, `TestHandler_Clients_Empty`); `TestE2E_Heavy_Protocol_TUIC` — нарушение frozen-rule; 0 table-driven tests (неидиоматично для Go); `coverage.out` не сохранён, нет чисел покрытия; 0 бенчмарков; битый JSON store + rollback-failure не протестированы.

**Общая оценка: TDD частично соблюдён (B+).** Core (store/applier/merged_config/frozen/cryptogen) покрыт тщательно с граничными случаями и rollback; git-гигиена образцовая (conventional commits + Co-Authored-By 72% коммитов + docs-spec-before-code). Слабые: i18n, takeover-оркестратор, table-driven, coverage numbers, бенчи.

## Соответствие AGENTS.md и docs/PROGRESS.md

10 правил: #1 HTMX+i18n (соблюдено, 462 i18n.T в templ), #2 Store Mutex (соблюдено, readStore/writeStore unlocked helpers), #3 SSH TOFU (соблюдено, no bypass), #4 Config gen separation (соблюдено, но applier.go смешивает pure+I/O — candidate split), #5 Persistent transit keys (соблюдено, generateHopParams reuses + SaveChain), #6 No silent failures (в основном, ~60 `_ =` большинство обоснованы, но singbox.go:141,187,679 + takeover `_ = RestoreFile` нарушают), #7 Graceful rollback (соблюдено, atomic для AWG обоих файлов), #8 Port conflict prevention (соблюдено, detectPortConflicts + MTProxy Port=0 fix), #9 Test before reporting (`Makefile build: generate` ПЕРЕД build, но `make test` не зависит от build — нет единой `make ci`), #10 Documentation (PROGRESS.md 65KB актуален, но §1.A «в рабочем дереве, не закоммичен» устарел — rework в HEAD; AGENTS.md Project Structure упоминает несуществующие `internal/sshclient/` (реально `internal/ssh/`) и `internal/web/ui.go` (реально набор файлов)).

Known Issues: #6 TUIC FROZEN (enforced, но e2e-активен — нарушение), #7 per-client routing (AWG primary реализован, TUN-overlay source_ip_cidr НЕ verified real-VPS — e2e skip stub), #8 TransportAWG/Hysteria2 (enforced, Hysteria2 loud-fail), #9 no WG outbound (WireGuardEndpoint+peers[] используется, WireGuardOutbound struct — dead reference), #10 AWG amnezia 4 fixes (SUPERSEDED kernel rework, open sub-items: I1Packet unused, takeover'd AWG no per-client, server-IP collision 10.8.0.1/32), #11 Hysteria2 FROZEN (enforced + edit-guard + тесты), #12 BALANCER (Table=off enforced), #13 egress VERIFIED (MASQUERADE both subnets, tunIncludeInterfaces awg-exit-nX, rp_filter=0), #14 QUIC-only capture (CaptureQUICSignature).

**Документационный дрейф:** PROGRESS.md §1.A «НЕ закоммичен» устарел; AGENTS.md Project Structure (`internal/sshclient/`, `internal/web/ui.go`) расходится с реальной структурой.

## Технический долг (legacy follow-ups)

| Пункт | Статус |
|---|---|
| CLI `Backend.ApplyConfig` standalone-AWG userspace (`RenderAWGHop`, main.go:673) | OPEN — расходится с kernel rework |
| Takeover'd AWG no per-client routing (peers не материализованы как User) | OPEN |
| Server-IP collision 10.8.0.1/32 (chain entry + standalone AWG inbound) | OPEN — per-inbound allocation |
| I1Packet parsed but unused | OPEN (low) |
| DNS/Route disabled (sing-box 1.13 detour bugs) + multi-node Route/DNS re-enable | OPEN (blocked upstream) |
| Profile/ClientAssignment удалены (C1) | CLOSED — legacy store.json загружается (Go ignores unknown keys) |
| `buildMergedRoute`/`BuildDNSWithDetour` gated `AB_ROUTE_DNS=1` | OPEN — AGENTS.md #2 «removed» неточно, реально gated |
| per-client source_ip_cidr под TUN-overlay real-VPS verify | OPEN (e2e skip stub) |
| Client-side `RenderClientAWGConf` не эмитит PostUp rp_filter=0 | OPEN (low, test-artifact) |
| `ip rule 10.8.0.0/24 → table 2022` на entry не эмитится deploy-flow | OPEN (low) |
| Пустые `internal/backend/xray/`, `internal/node/`, `web/handlers/` | OPEN — zombie-каталоги |
| `WireGuardOutbound` struct dead reference | OPEN (low) |
| `var _ = fmt.Sprintf` zombie-импорт (mtproxy.go:98) | OPEN (low) |
| MTU=1420 / subnets magic numbers без констант | OPEN (low) |
| errcheck disabled (TODO 0.3.0) | OPEN |

## CI/CD статус

`.github/workflows/` **ОТСУТСТВУЕТ** (PR_DESCRIPTION_v0.2.0.md falsely claims `release.yml` + `.ipk` artifacts + `packaging/openwrt-aarch64/`). `.golangci.yml`: gofmt/govet/staticcheck(all)/unused/ineffassign/typecheck включены, errcheck ОТКЛЮЧЁН (TODO 0.3.0). Makefile: `generate`/`build`/`test`/`vet`/`test-coverage` + cross-build targets (linux amd64/arm64/armv7, keenetic mipsel, opkg) + install/systemd — полный набор, но `make test` не зависит от `build`/`generate` (нет единой `make ci`). Рекомендация: минимальный CI = `go build ./...` + `go vet ./...` + `make generate` (check no diff) + `go test ./...` (non-e2e) + `govulncheck ./...`. Cross-build CI на Keenetic mipsel — не тестируется, может silent rot.

## Рекомендуемый порядок действий

### 1. Немедленно (blockers v0.3.0)
- **CI**: минимальный GitHub Actions (build + vet + templ-generate-check + go test + govulncheck). Закроет regression-защиту после 4 подпроектов.
- **Шифрование секретов at-rest в store.json** — master-key из admin password / unlock-key. Сейчас кража файла = полный флот.
- **Убрать паники из request-path**: `mustMarshal` → return error; `cryptogen.Generate*` → `(string, error)`.
- **`main.go:113`**: не молча игнорировать `config.Load` error — log.Fatal или явный fallback с предупреждением. + TOML `DisallowUnknownFields`.
- **`handleTrustHostKey`**: сверять submitted fingerprint с наблюдённым `HostKeyError.RemoteFingerprint` (pending-state).
- **systemd**: `User=angry-box` (non-root) + bind loopback + reverse proxy (TLS) + `NoNewPrivileges`/`ProtectSystem`/`ProtectHome`. install.sh — не bind `0.0.0.0` plain HTTP.
- **Skip `TestE2E_Heavy_Protocol_TUIC`** (AGENTS.md #6 compliance).

### 2. До v0.3.0
- **Store schema versioning**: `SchemaVersion` field + versioned migrators chain (вместо ручных one-shot).
- **i18n-тест**: key-completeness en/ru + паритет (закроет асимметрию ~302/~520).
- **HTTP server timeouts**: `ReadTimeout`/`WriteTimeout`/`IdleTimeout` + `http.TimeoutHandler` (Slowloris).
- **context.Context в SSH-push + Store** — UI-отмена deploy отменяет SSH-таймауты.
- **Sentinel errors**: хотя бы `ErrHostNotFound`/`ErrSSHTimeout`/`ErrRollbackFailed` для программного различения.
- **Документационный дрейф**: PROGRESS §1.A «НЕ закоммичен» → обновить; AGENTS.md Project Structure (`internal/sshclient/`→`internal/ssh/`, `ui.go`→набор файлов).
- **Удалить zombie**: `internal/backend/xray/`, `internal/node/`, `web/handlers/`, `WireGuardOutbound` struct (или пометить deprecated), `var _ = fmt.Sprintf` (mtproxy.go:98).
- **Takeover cutover/rollback-to-old unit-тесты** + rollback-failure path + битый JSON store.
- **`coverage.out`** коммитить после `make test-coverage` (числа покрытия).
- **error wrapping `%w`** в rollback-путях (applier.go:1049-1080, singbox.go:657-681 — 12 `%v` теряют цепочку).
- **store default path**: `chains.json` (main.go) vs `store.json` (config.go/install.sh) — унифицировать.

### 3. До v1.0 (production-grade)
- **deps/sing-box mirror/backup** — alternate host или git-lfs, не single GitHub release URL.
- **patches/ rebasing doc** — version-tag patches, регрессион-тест на SNI/патч-применимость при upstream bump.
- **per-client source_ip_cidr под TUN-overlay real-VPS verify** — фундаментальная неопределённость (AGENTS.md #7 primary механизм).
- **applier.go split** — `applier_build.go` (pure) + `applier_push.go` (SSH I/O); `buildMergedNodeConfig` → config-struct; `web/server.go` Register split по ресурсам.
- **SSH connection pool** + **concurrency cap на autoapply** (semaphore, не unbounded goroutines).
- **forced password change on first login** + индикатор temporary-пароля.
- **store.json schema validation при load** (port ranges, required fields, type checks).
- **Path traversal валидация** `ResolveKey` system-key `fileName` (base only, no `..`).
- **JS-escape в hx-confirm** (`u.Name` в JS-контексте, users.templ:141).
- **Metrics `NodeMetrics` eviction** (keep last N per host).
- **`hostlock.go` cleanup** (periodic removal unused mutexes).
- **Table-driven тесты** (Go idiomatic) + бенчмарки (store marshal, RenderMergedNodeConfig).
- **Keenetic mipsel cross-build CI** (не тестируется регулярно).
- **MTProxy QR через POST / no-referrer meta** (secret не утекает в browser history/referer).

### 4. Operational debt
- sing-box-extended pin updates + amneziawg-src version pin + patch rebases — документировать процесс.
- Reality SNI/fingerprint drift monitoring при обновлении sing-box.
- Audit log retention (capped 5000, но store.json растёт — separate audit storage?).
- `install.sh --uninstall` `rm -rf` без подтверждения store — добавить prompt или backup.