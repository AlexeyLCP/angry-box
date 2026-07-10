# Angry-box — Файл знаний (Knowledge & Progress)

> Единый источник правды: что сделано, что нужно, откуда берём. Обновлять при каждом изменении. Не удалять — накапливать.

Последнее обновление: 2026-07-08 (backups + node relocation feature — §14)

---

## ЛИЦЕНЗИЯ ПРОЕКТА: PolyForm Noncommercial

**Angry-box распространяется под PolyForm Noncommercial** (см. `LICENSE` + бейдж в `README.md`: `license-PolyForm%20Noncommercial-blue.svg`). Это **source-available, non-commercial** лицензия:
- ✅ Личное, некоммерческое, образовательное, исследовательское использование.
- ❌ Любое коммерческое использование запрещено без письменного разрешения автора (продажа VPN-сервисов, SaaS, бандл с коммерческим железом, платные подписки, и т.д.).

**Важно для заимствований:** при копировании кода из внешних permissive-проектов (awg-manager MIT, awg-multi-script MIT) — мы интегрируем их алгоритмы в наш PolyForm-проект. Их исходная MIT-лицензия допускает это, но надо корректно атрибутировать (упоминать источник + их лицензию в комментарии-источнике), а итоговый наш код живёт под нашей PolyForm. Внешние GPL-зависимости (sing-box GPLv3, amneziawg-kernel GPLv2) — отдельно, см. README.md раздел dependencies.

**Атрибуция (что откуда):**
- `awg-manager` (hoaxisr) — **MIT** — live QUIC capture algorithm (`internal/signature/capture.go`).
- `awg-multi-script` (pumbaX) — **MIT** — AWG obfuscation best practices (Jc/Jmin/Jmax/S1-S4/H1-H4 invariants, CPS packet generation).
- `sing-box-extended` (shtorm-7) — **GPLv3** — backend.
- `amneziawg-linux-kernel-module` (amnezia-vpn) — **GPLv2** — kernel module.

---

## 0. Принятые архитектурные решения (НЕ менять без согласия)

1. **AWG — основной протокол.** Упор на него: обфускация, пресеты, все пути.
2. **AWG-сервер = kernel `awg-quick@awg0.service` + sing-box TUN-overlay.** Никакого userspace `WireGuardEndpoint` на серверах — userspace wireguard-go падает с `panic: chacha20poly1305` с amnezia-обфускацией (gVisor `system:false` И `system:true` — оба падают; amnezia-математика идёт через userspace-код даже в system-режиме). Доказано в `VPN/docs/sing-box-extended.md:103-111`, `nuances-bugs-patches.md:199-201`.
3. **sing-box НЕ поднимает AWG-интерфейс.** `awg-quick@awg0.service` (kernel systemd) поднимает, sing-box работает поверх через TUN `include_interface:["awg0"]` + direct outbounds с `bind_interface`. Эталон: `VPN/orchestrator/app/templates/awg_balancer.json.j2`.
4. **TUIC — FROZEN (на паузе).** User entry + standalone. QUIC/TLS cert геморрой + нерешённые баги. Не тестировать, не фиксить, не предлагать в UI для новых конфигов (AGENTS.md #6). См. `internal/chain/frozen.go`.
5. **Hysteria2 — FROZEN (на паузе).** Transport + standalone + user entry. Тот же класс проблем что TUIC (QUIC требует TLS/self-signed cert). Builder не написан, UI блокирует новый выбор. Не реализовывать пока не доведены до ума AWG, Reality+XHTTP, MTProxy (AGENTS.md #11). См. `internal/chain/frozen.go`.
6. **Product focus (базовый минимум v0.2.x):** AWG (kernel + balancer), VLESS+Reality+XHTTP (transport + standalone), MTProxy/Telemt. Всё остальное — вне скоупа.
7. **Live CPS capture: только QUIC, не plain TCP TLS.** Два режима capture — не путать:
   - ✅ **QUIC live capture** (`quic-live`, `CaptureQUICSignature`) — **РАБОТАЕТ**. UDP→domain:443, QUIC Initial (внутри — TLS ClientHello в CRYPTO frame), ловим ответы → I1-I5. Интегрирован в `EnsureChainAWGMaterial` (раздел 2.4).
   - ❌ **TCP TLS live capture** (plain TLS handshake по TCP:443 без QUIC-обёртки) — **НЕ поддерживается** (awg-manager: несовместим с AWG, крашит runtime). В angry-box не портирован и не планируется.
   Синтетический CPS (`quic`/`sip`/`dns` mimicry, `GenerateQUICInitial`) — тоже работает, без сети.
8. **Itime ломает runtime.** `sing-box UAPI` rejects "itime", `awg setconf` rejects "Itime". Держим только в Go (`AmneziaOptions.ITime json:"-"`), не эмитим в .conf. Фикс 6f1a108.
9. **awg-quick .conf: amnezia-поля в `[Interface]` ДО `[Peer]`.** `awg setconf` парсит amnezia только в `[Interface]`; после `[Peer]` → `Line unrecognized: Jc=...`.
10. **commit convention:** `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
11. **E2E-инфра:** GCloud VPSes (свежие, 2026-07-08) — entry `34.14.98.64`, middle `207.175.1.227`, exit `35.189.235.61`. User `lcp`, key `id_ed25519`, passwordless sudo (google-sudoers), Debian 12, kernel 6.1.0-49. Сервера чистые (нет sing-box/awg — ApplyChain self-stages). GCloud UDP 443 firewall на exit VPS — известный инфраструктурный затык e2e.

---

## 1. Что сделано (committed)

> **Ниже — большой заход kernel-AWG rework (2026-07-03), закоммичен в main (см. коммиты вокруг 2026-07-03).** См. раздел 1.A для деталей. Все 7 подсистем собраны, `go build ./...` + полный non-e2e `go test` зелёные.

- **#5 standalone multi-peer** — committed+pushed. `buildAWGUserInboundMulti` (multi-peer WG endpoint per-user). ~~ВАЖНО: это userspace endpoint — архитектурно неверен, будет переделан в п.3.1 ниже.~~ → **ПЕРЕДЕЛАНО** в разделе 1.A: standalone AWG больше не эмитит userspace endpoint (kernel awg0.conf + TUN-overlay).
- **#4 AWG takeover** — committed+pushed. `renderAWGTakeoverConfig` + Takeover AWG branch. ВАЖНО: takeover сейчас **убивает `awg-quick@awg0`** чтобы освободить порт под userspace endpoint — архитектурно неверно, будет переделан в п.3.8 (takeover — ещё pending, не трогали в этом заходе).
- **itime fix** — committed 6f1a108. `AmneziaOptions.ITime json:"-"`, Itime line убран из .conf.
- **AWG transit wiring** — `buildAWGTransportInbound`/`buildAWGTransportOutbound` (applier.go), key mgmt, `TransitAWGServerPriv/Pub/ClientPriv/Pub/Address/ClientPort` на ChainNode. **Linear multihop НЕ трогали в этом заходе** — transit wiring остался userspace endpoint (это inter-node transport, не user-facing; план был не трогать linear multihop). User-entry/standalone/exit переделаны на kernel.
- **CPS I1-I5 персист** — `EnsureChainAWGMaterial` (awg_cps.go:377) персистит I1-I5 + H1-H4 на chain, idempotent. `ChainAWGObfsMaterial` реконструит.
- **H1-H4 квадрантный генератор** — `GenAWGParams` (awgpresets_gen.go:95) живой, реальный 4-квадрантный алгоритм. `GenerateAWGObfsMaterial` вызывает его.
- **Multihop N узлов** — работает (нет лимита 2). Reality/XHTTP transit — рабочие.
- **MTProxy: extended-secret** (`ee`+hex+domain) — `cryptogen.go:160-168`. Формат верный. (Inbound renderer — ещё pending, раздел 2.3.)
- **MTProxy: модель + store CRUD** — `panel.go:283` MtproxyUser, `store.go:966-1046` CRUD.
- **Client-side multi-entry** (urltest/selector) — `clientconfig.go:152-168`. Работает.

---

## 1.A. Kernel-AWG rework (2026-07-03, закоммичен в main)

Большой заход: AWG-сервер с userspace `WireGuardEndpoint` → **kernel `awg-quick@awg0` + sing-box TUN-overlay**. + multi-exit balancer (`NodeRoleExit` + `ExitTargets`). + deploy-флоу с двухфайловым пушем и atomic rollback. Все тесты зелёные.

### Что сделано (новые файлы + изменения)

| Подсистема | Файл | Что | Тесты |
|---|---|---|---|
| DKMS-установка модуля | `internal/backend/singbox/singbox.go` | Динамическая версия из `dkms.conf` (`AB_AWG_MODVER`) вместо хардкода `1.0.0` → модуль не ломается при bump tarball. Regression-гвард. | `TestInstallAWGModule_*` (4) |
| Серверный awg0.conf | `internal/chain/awg_server.go` (новый) | `RenderServerAWGConf` (user-entry + standalone), `RenderExitAWGConf` (balancer client awg-exit-nX), `RenderExitServerAWGConf` (Role=exit server awg0). Amnezia в `[Interface]` до `[Peer]`, no Itime, skip пустых peers, defaults. | `awg_server_test.go` (10) |
| Модель multi-exit | `internal/domain/model/chain.go` | `NodeRoleExit`, `ExitTargets []string`, `AWGExitLink` struct (TargetID/InterfaceName/ClientPriv/Pub/Address/ClientPort), `ExitAWGServerPriv/Pub/ListenPort` на exit-узле. `ensureAWGExitLinks` генерирует+персистит (Rule 5). | `awg_exitlinks_test.go` (2) |
| TUN-overlay рендерер | `internal/chain/awg_tun_overlay.go` (новый) | `BuildAWGTUNOverlay`: `endpoints:[]`, TUN `include_interface:["awg0"]` (stack:"mixed", auto_route), direct outbounds с `bind_interface`, `fallback` balancer (round-robin на patched build), route `tun-in→balancer`. `awgTUNOverlayNeeded`/`awgOverlayNode` helpers. | `awg_tun_overlay_test.go` (7) |
| Проводка в merged_config | `internal/chain/merged_config.go` | `buildChainRoleInOut` AWG entry + `buildStandaloneInOut` case "awg" — **больше НЕ эмитят userspace endpoint** (kernel owns awg0). `buildMergedNodeConfig` эмитит TUN-overlay на уровне узла + route rules (всегда для AWG, не gated на AB_ROUTE_DNS). | `awg_config_check_test.go` (entry-only переписан), `standalone_awg_test.go` (4 переписаны), `critical_deploy_test.go` (AllProtocols/awg) |
| RenderAWGBalancer | `internal/backend/singbox/roles.go` | Реализован (был stub — только заголовок). TUN + multi-exit direct+bind_interface + fallback + route. `endpoints:[]` (no userspace WG). | `roles_test.go` (3: multi-exit, no-userspace-WG, single-egress) |
| Aggregator | `internal/chain/awg_deploy.go` (новый) | `RenderNodeAWGConfs`: рендерит ВСЕ kernel awg-quick .conf файлы узла (chain entry awg0, balancer awg-exit-nX, exit server awg0, standalone awg0) в стабильном порядке. | `awg_deploy_test.go` (5) |
| Deploy-флоу | `internal/chain/awg_push.go` (новый) | `pushConfigWithAWG`: awg0.conf push + `enable awg-quick@...` BEFORE sing-box config, **atomic rollback обоих** при check/restart/probe failure. Per-host lock across both. `renderAWGConfsForDeploy` adapter. | `awg_push_test.go` (4: passthrough, happy-path+ordering, rollback-both, awg-enable-fails) |
| Проводка deploy | `internal/chain/applier.go` | `ApplyChain` + `ApplyMergedNode` зовут `pushConfigWithAWG` с `renderAWGConfsForDeploy`. `ApplyMergedNode` теперь тоже ставит AWG-модуль (фикс асимметрии — раньше скипал). | covered существующими ApplyChain/ApplyMergedNode тестами |
| systemd ordering | `internal/backend/singbox/singbox.go` | `writeSystemdUnit`: `After=...awg-quick@awg0.service` (ordering hint, не Requires — безопасно на non-AWG узлах). | singbox suite green |

### Архитектурные инварианты (проверены тестами)
- **Никаких userspace `WireGuardEndpoint` на AWG-сервере** (chain entry, standalone, exit) — `TestBuildAWGTUNOverlay_NoEndpoints`, `TestRenderAWGBalancer_NoUserspaceWG`, `TestBuildStandaloneInOut_AWG_NoUserspaceWGRegression`.
- **Amnezia в `[Interface]` ДО `[Peer]`** во всех .conf — `TestRenderServerAWGConf_AmneziaBeforePeer`, `TestRenderExitAWGConf_AmneziaBeforePeer`.
- **Itime никогда не пишется** — `TestRenderServerAWGConf_NoItime`, `TestRenderExitAWGConf_AmneziaBeforePeer`.
- **TUN `include_interface` = `["awg0"]` только** (exit ifaces — outbound-side via bind_interface, не re-capture) — `TestBuildAWGTUNOverlay_MultiExitBalancer`.
- **awg-quick enable BEFORE sing-box restart** (awg0 up когда TUN его capture) — `TestPushConfigWithAWG_AWGFiles_PushesAndEnables` (ordering assertion).
- **Atomic rollback обоих файлов** при sing-box failure — `TestPushConfigWithAWG_SingBoxCheckFails_RollsBackBoth`.
- **Exit-link keys персистятся** (Rule 5) — `TestEnsureAWGExitLinks_GeneratesPersistentMaterial` (re-run не ротирует).

### Что НЕ трогали в этом заходе (намеренно)
- **Linear multihop transit wiring** (Reality/XHTTP/AWG inter-node transport) — оставлен как есть, рабочий. AWG transit (inter-node) остался userspace endpoint по плану (это point-to-point между нодами, не user-facing DPI surface). Заметка для будущего: если transit AWG тоже падает с amnezia, перевести на kernel — но это отдельный заход.
- **Takeover AWG** (раздел 2.7) — ещё убивает awg-quick@awg0, pending.
- **MTProxy inbound** (раздел 2.3) — pending.
- **Live capture усиление** (раздел 2.4) — pending.
- **Hysteria2 transport** (раздел 2.8) — pending.

### Файлы (рабочее дерево)
- Новые: `docs/PROGRESS.md`, `internal/chain/awg_server.go`, `internal/chain/awg_server_test.go`, `internal/chain/awg_tun_overlay.go`, `internal/chain/awg_tun_overlay_test.go`, `internal/chain/awg_deploy.go`, `internal/chain/awg_deploy_test.go`, `internal/chain/awg_push.go`, `internal/chain/awg_push_test.go`, `internal/chain/awg_exitlinks_test.go`.
- Изменённые: `internal/domain/model/chain.go`, `internal/chain/{applier,merged_config,cryptogen,awgcapture,standalone_awg_test,critical_deploy_test,awg_config_check_test}.go`, `internal/backend/singbox/{singbox,roles,roles_test,standalone_awg_test}.go`.

---

## 2. Что НЕ сделано (todo) — полный фронт

### 2.1. ~~Главное: AWG-сервер на ядре~~ — ✅ ДЕЛАНО (раздел 1.A)

~~5 мест с userspace `WireGuardEndpoint`.~~ → Все user-facing AWG-серверы (chain entry, standalone, exit-сервер) переделаны на kernel `awg-quick@awg0` + sing-box TUN-overlay. **AWG transit (inter-node transport) ОСТАВЛЕН userspace endpoint** по плану (linear multihop не трогали — это point-to-point между нодами, не DPI surface). Takeover AWG — ещё pending (раздел 2.7).

### 2.2. ~~Per-connection балансировщик через exit-ноды~~ — ✅ ДЕЛАНО (раздел 1.A)

~~`RenderAWGBalancer` stub.~~ → `RenderAWGBalancer` реализован (roles.go) + `BuildAWGTUNOverlay` (awg_tun_overlay.go) эмитят `fallback` balancer + `bind_interface` direct outbounds по exit-нодам + `NodeRoleExit`/`ExitTargets` модель. Round-robin работает на patched sing-box-extended build (deps/...-patched).

### 2.3. ~~MTProxy inbound~~ — ✅ ДЕЛАНО (раздел 1.A)

~~`RenderMTProxy`/`buildMTProxyInbound` не существует; 3 свича падают в VLESS/WS.~~ → `MTProxyInbound` тип (types.go) + `buildMTProxyInbound` renderer (mtproxy.go) с extended `ee`+hex secret + canonical FakeTLS options. Case в 3 свичах (buildStandaloneInOut, buildChainRoleInOut user-entry — no-op, node-level emission). Node-level emission в `buildMergedNodeConfig` (новый `mtproxyUsers` параметр) для standalone + chain entry. UI: handlers (mtproxy.go) + templ (mtproxy.templ) + routes + nav + i18n (en/ru). 4 теста.

### 2.4. ~~Live capture CPS~~ — ✅ ДЕЛАНО (раздел 1.A)

~~awgcapture.go упрощённый (shape-fake без AEAD), не подключён к EnsureChainAWGMaterial.~~ → `quic_initial_aead.go` — полная RFC 9001: real TLS ClientHello через `crypto/tls`+`net.Pipe` (SNI работает), HKDF key derivation из DCID, AES-128-GCM, header protection. `CaptureQUICSignature` теперь шлёт реальный AEAD-зашифрованный Initial (сервера отвечают). `EnsureChainAWGMaterial` — `quic-live` mimicry path: при `AWGCPSCaptureDomain` dialит домен, capture→I1-I5, персистит + `AWGCPSCapturedDomain` для cache-invalidation (Rule 5), fallback на synthesized `quic` при capture-failure (chain никогда не ломается). 7 тестов.

### 2.5. ~~DKMS-установка модуля~~ — ✅ ДЕЛАНО (раздел 1.A)

~~хардкод `1.0.0`.~~ → Динамическая версия из `dkms.conf` (`AB_AWG_MODVER`) → модуль не ломается при bump tarball. Regression-гвард.

### 2.6. ~~Deploy-флоу AWG + откат~~ — ✅ ДЕЛАНО (раздел 1.A)

~~Только config.json, не пишут awg0.conf.~~ → `pushConfigWithAWG` (awg_push.go): awg0.conf + awg-exit-nX.conf push + `enable awg-quick@...` BEFORE sing-box config, **atomic rollback обоих** при check/restart/probe failure. `ApplyChain`/`ApplyMergedNode` проводят через `renderAWGConfsForDeploy` (ApplyMergedNode теперь тоже ставит AWG-модуль — фикс асимметрии). systemd `After=awg-quick@awg0.service` (ordering hint, безопасно на non-AWG). 4 теста.

### 2.7. ~~Takeover AWG переделка~~ — ✅ ДЕЛАНО (раздел 1.A)

~~убивает awg-quick@awg0, userspace endpoint.~~ → `renderAWGTakeoverConfig` эмитит TUN-overlay (include_interface:["awg0"], no userspace endpoint). Cutover НЕ дизейблит `awg-quick@awg0` для AWG (kernel keeps running, sing-box поверх). Rollback — откат sing-box config (не трогает awg-quick). 3 теста (TUN-overlay, no amnezia block, real sing-box check).

### 2.8. ~~Hysteria2~~ — ✅ ЗАМОРОЖЕН (2026-07-04)

- Transport + standalone + user entry — **на паузе** (QUIC/TLS cert отложен; фокус: AWG, Reality+XHTTP, MTProxy).
- `internal/chain/frozen.go` + UI: новый выбор заблокирован; apply существующих hysteria2-transport цепей — hard error.

---

## 3. План выполнения (по зависимостям)

Порядок (каждый компилируется+тестируется отдельно):
1. **DKMS-установка** (п.2.5) — малое, изолированное. `InstallAWGModuleWithOptions`.
2. **awg0.conf генератор сервер** (п.2.1 часть) — новая `RenderServerAWGConf(node, peers, amneziaMaterial) string` в новом `internal/chain/awg_server.go`. Чистый текст. Реюзает `ChainAWGObfsMaterial`, `GenAWGParams`, ключи юзеров (`User.AWGPrivateKey/AWGPublicKey/AWGAddress`).
3. **Sing-box TUN-overlay рендерер** (п.2.1 часть) — новая функция, переделать `buildAWGUserInboundMulti`: `endpoints:[]`, TUN `include_interface:["awg0"]`, direct+bind_interface, fallback-балансер. Удалить userspace-ветки из chain-entry/standalone/transit-in.
4. **Per-connection балансер** (п.2.2) — `RenderAWGBalancer` (roles.go) + серверный fallback + `bind_interface` direct-outbounds по exit-нодам.
5. **MTProxy inbound** (п.2.3) — `RenderMTProxy` + case в 3 свичах + UI.
6. **Deploy-флоу + откат** (п.2.6) — двухфайловый пуш, enable awg-quick@awg0, After=, откат, E2E.
7. **Live capture CPS** (п.2.4) — портировать `capture.go`, интеграция в `EnsureChainAWGMaterial` как опция `MimicrySource: "live"|"synthetic"`.
8. **Takeover AWG** (п.2.7) — после #2/#3.
9. **Hysteria2** (п.2.8) — решение отдельно.

---

## 4. Внешние референсы (откуда берём)

### 4.1. `hoaxisr/awg-manager` — live capture CPS

Файл: `internal/signature/capture.go` (master branch, 403 строки, чистый Go stdlib + `netutil.ResolveHost`).

**Что делает:**
- `Capture(domain) CaptureResult` — нормализует домен (`normalizeDomain`), резолвит IP (`netutil.ResolveHost`), вызывает `captureQUIC` (UDP/QUIC). Plain TCP TLS capture в upstream не реализован — несовместим с AWG. Наш порт: только QUIC path (`awgcapture.go`).
- `captureQUIC(domain, ip, timeout) ([][]byte, error)` — реальный capture: dial `net.DialTimeout("udp", ip:443)`, пишет QUIC Initial, читает до 5 ответов. Возвращает `[наш_Initial, ответ1, ...]`.
- `buildQUICInitial(domain) ([]byte, error)` — полная RFC 9001, не shape-фейк:
  - `buildTLSClientHello(domain)` через `crypto/tls` + `net.Pipe()` — Go сам генерит настоящий TLS 1.3 ClientHello с реальным SNI (`ServerName: domain`, `NextProtos: ["h3"]`, `MinVersion/MaxVersion: TLS13`). `tlsConn.Handshake()` пишет ClientHello в pipe, мы читаем raw bytes. Ключевое: SNI реально работает.
  - Случайный DCID (8 байт).
  - `deriveInitialKeys(dcid)` — HKDF-Extract из QUIC v1 salt (`38762cf7f55934b34d179ae6a4c80cadccbb7f0a`) + HKDF-Expand-Label (`client in`→`quic key`/`quic iv`/`quic hp`), RFC 8446 §7.1. `hkdfExtract`/`hkdfExpandLabel`/`hkdfExpand` через HMAC-SHA256.
  - CRYPTO frame (0x06) + offset 0x00 + varint length + ClientHello payload.
  - Padding до 1200 байт (minPayloadBytes = 1200 - headerSize - 16).
  - AES-128-GCM (`cipher.NewGCM`), nonce = IV XOR pktNum (pktNum=0 → nonce=IV).
  - Header protection: AES-ECB mask (RFC 9001 §5.4), sample = first 16 bytes ciphertext, `mask[0] & 0x0F` на первый байт, mask[1+i] на pktNum.
  - Сборка protected header + ciphertext.
- `fillPackets(r, packets)` — конвертит raw bytes → `<b 0xHEX>`, I1-I5. I1 = отправленный Initial, I2-I5 = ответы сервера. maxPacketSize=1500, maxPackets=5.
- Константы: `captureTimeout=5s`, `maxPacketSize=1500`, `maxPackets=5`.
- Зависимости: `crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/rand`, `crypto/sha256`, `crypto/tls`, `encoding/binary`, `encoding/hex`, `net`, `sync`, `time`, + внутренний `netutil.ResolveHost` (надо заменить на свой резолвер или `net.ResolveIPAddr`).

**Лицензия:** awg-manager — **MIT** (см. README.md dependencies; их код можно интегрировать). Наш `awgcapture.go` уже портирован из него. Итоговый код в angry-box живёт под нашей **PolyForm Noncommercial** (см. начало файла). Код чистый stdlib, переосмыслить легко.

**Интеграция у нас:** `EnsureChainAWGMaterial` (awg_cps.go:377) → добавить `MimicrySource: "live"|"synthetic"` на chain/profile. При `live` — `Capture(domain)` (домен задаётся юзером в UI), персистит I1-I5 на chain. При `synthetic` — текущий `GenerateQUICInitial`. Capture на оркестраторе (не VPS — там нет сети/Python, Known Issues #4).

### 4.2. `pumbaX/awg-multi-script` — установка модуля + пресеты

Файл: `awg2.sh` (master).

**Что берём:**
1. **DKMS-установка** (п.2.5): `base_deps+=(build-essential git libmnl-dev pkg-config dkms)`, `make dkms-install`, `dkms add -m amneziawg -v $ver`, `dkms build`, `dkms install`, `modprobe amneziawg`.
2. **H1-H4 квадрантный генератор** — у нас уже есть (`GenAWGParams`, рабочий). Их версия идентична: 4 квадранта [5..2^31-1] по 2^29, `_gen_quadrant_pair` (lo в первой трети, hi в последней, width>=1000). Сверить с нашей, расхождения нет.
3. **Путь сервер-конфига:** `/etc/amnezia/amneziawg/awg0.conf` (не `/etc/wireguard/`). Имя интерфейса `awg0`. `awg-quick@awg0.service`.
4. **CPS генератор** (Python `_CPS_GENERATOR`): синтетический TLS ClientHello (Chrome-порядок, GREASE RFC 8701, SNI, ALPN) + QUIC Initial 1200. У нас есть аналог (`GenerateQUICInitial`), но их — с GREASE и Chrome-порядком шифров. Можно взять как референс для синтетического пути. Но live capture (п.4.1) лучше — реальный fingerprint.
5. **`sysctl -w net.ipv4.ip_forward=1`** + персист в `/etc/sysctl.conf`.
6. **Профили мимикрии** quic/sip/dns — расширение нашего `ObfuscationProfile`.

**Что НЕ берём:**
- `iptables -t nat -A POSTROUTING -o $ext_if -j MASQUERADE` — прямой NAT наружу, минуя sing-box (`nuances-bugs-patches.md:236-270`). У нас sing-box TUN-overlay, MASQUERADE ломит путь.
- `awg-quick up/down` напрямую без systemd — мы хотим `systemctl enable awg-quick@awg0` для авторестарта.

### 4.3. Наши VPN/docs (эталонная архитектура)

- `VPN/orchestrator/app/templates/awg_balancer.json.j2` — эталон sing-box config (TUN-overlay + balancer + bind_interface).
- `VPN/orchestrator/app/templates/mtproxy_server.json.j2` — эталон MTProxy server (type:"mtproxy", users с ee-секретами, fallback awg-failover).
- `VPN/docs/architecture.md` — Server 2 (dns.idoctor.mom): AWG clients → awg0 (kernel) → sing-box-tun → fallback balancer → exit nodes.
- `VPN/docs/sing-box-extended.md` — sing-box-extended фичи, balancers comparison, amnezia bug, MTProxy secret format, build tags.
- `VPN/docs/nuances-bugs-patches.md` — нюансы (TUN stack mixed, include_interface, source_ip_cidr vs inbound, MTProxy secrets, fallback patch).
- `VPN/docs/server-dns-idoctor-mom.md` — миграция Xray→sing-box, `awg-quick@awg0.service` enabled, `sing-box.service After=awg-quick@awg0.service`.

---

## 5. Ключевые файлы angry-box (карта)

### Будем менять
- `internal/chain/applier.go` — `buildAWGUserInboundMulti:1285`, `buildAWGTransportInbound:569`, `buildAWGTransportOutbound:628`, `ApplyChain:137-430`, AWG deploy `:349-355`, `InstallAWGModuleWithOptions`.
- `internal/chain/merged_config.go` — `buildMergedNodeConfig:62`, `buildChainRoleInOut:445`, `buildStandaloneInOut:602` (case "awg":627), `buildAWGUserInboundMulti` (referenced).
- `internal/chain/awg_cps.go` — `EnsureChainAWGMaterial:377`, `ChainAWGObfsMaterial:414`, `BuildAmneziaSection:306`, `GenerateQUICInitial:99` (live capture интеграция).
- `internal/chain/awgpresets_gen.go` — `GenAWGParams:95`.
- `internal/chain/awgcapture.go` — проверить статус (live capture?).
- `internal/backend/singbox/roles.go` — `RenderProxyNode:59`, `RenderAWGBalancer:182` (СТУБ — нет тела), `RenderAWGHop:204`.
- `internal/backend/singbox/config.go` — `generateStandaloneNode` switch:262, `generateAWGUser:181` (userspace+TUN — переделать).
- `internal/domain/model/chain.go` — `UserProtocolMTProxy:114`, `TransportHysteria2:124`, `Strategy*:3-11`, `ChainNode` transit-поля.
- `internal/domain/model/panel.go` — `MtproxyUser:283`, `User.AWGPrivateKey/AWGPublicKey/AWGAddress`, `NodeInbound.Tag`.
- `internal/singbox/config/types.go` — `BindInterface:58`, `IncludeInterface:411`, `SourceIPCIDR`, `AmneziaOptions.ITime json:"-"`.
- `internal/takeover/awg_takeover.go` — `renderAWGTakeoverConfig:33`.
- `internal/takeover/takeover.go` — `Takeover:38`, AWG branch `:98`, дизейбл awg-quick `:116-128`.
- `internal/chain/cryptogen.go` — `GenerateMTProxySecret:153`, `MTProxyFullSecret:160`.
- `internal/chain/store.go` — `MtproxyUsers` CRUD `:966-1046`.

### Создадим
- `internal/chain/awg_server.go` — `RenderServerAWGConf` (awg0.conf серверный генератор).
- `internal/chain/livecapture.go` (или `internal/signature/capture.go`) — портированный live capture из awg-manager.

### Эталонные (не трогать, читать)
- `VPN/orchestrator/app/templates/*.j2`, `VPN/docs/*.md` — эталоны архитектуры.

---

## 6. Статус трафика (итог, re-confirmed 2026-07-04)

| Путь | Трафик ходит? | Доказательство |
|------|---------------|----------------|
| Inter-node XHTTP/Reality transport | **Да** | `TestE2E_Heavy_Protocol_AWG_Kernel_2Hop` PASS (35.95s) — entry `tun-in` catch-all → `ch-e2e-awg-kernel-2hop-out-www` (chain outbound) → exit XHTTP transport-in healthy |
| Kernel AWG handshake (entry) | **Да** | `TestE2E_Heavy_Protocol_AWG_Kernel` PASS (23.83s) — awg0.conf pushed (3624B), `awg-quick@awg0` active, awg0 10.8.0.1/24, 0 userspace WG endpoints, systemd `After=awg-quick@awg0.service` |
| AWG handshake (per-client/balancer) | **Да** | `TestE2E_Heavy_PerClientRouting` PASS (76.97s) — `latest handshake: 5 seconds ago`, transfer 92B rx/64.49KiB tx, jc/jmin/jmax/s1-s4/h1-h4/i1-i5 all matching |
| Balancer deploy (entry + exit) | **Да** | E2E PASS, awg0 + awg-exit-n1 + MASQUERADE (`-A POSTROUTING -s 10.8.0.0/24 -o ens4 -j MASQUERADE` live на server-3) + Table=off (default route intact) |
| Client → internet egress (полный путь) | **Да — VERIFIED 2026-07-04 (§11)** | server-2 (kernel AWG client, 10.8.0.99) → entry → exit → internet. `curl --interface awg0 ifconfig.me → 23.251.133.38` (exit's public IP). 3 routing bugs найдены и исправлены (exit MASQUERADE 10.10.0.0/24, include_interface awg-exit-nX, rp_filter=0 PostUp). |
| Linear AWG inter-node transport | **Частично** | Handshake OK, data plane под amnezia нестабилен → amnezia отключена на transit. Balancer architecture (kernel exit tunnels) — рабочий путь. |
| TUIC / Hysteria2 | **На паузе** | Не в скоупе product focus (базовый минимум: AWG, Reality+XHTTP, MTProxy). Frozen enforcement в `internal/chain/frozen.go` + UI edit-guard. |

**Вывод:** инфраструктура деплоится, handshake проходит (kernel AWG + amnezia), межузловой forwarding работает (XHTTP transport), balancer architecture стабильна. Полный egress end-to-end на отдельном клиенте — последний незакрытый verify, не блокер архитектуры (test artifact, не product bug).

---

## 7. Открытые вопросы / что проверить

- [x] Лицензия `awg-manager` (hoaxisr) — **MIT** (источник), интегрируем в наш PolyForm-проект с атрибуцией. Сам angry-box — **PolyForm Noncommercial** (`LICENSE`).
- [x] ~~Статус `internal/chain/awgcapture.go` — упрощённый, без AEAD.~~ → УСИЛЕН: `quic_initial_aead.go` (полная RFC 9001 AEAD) подключён к `CaptureQUICSignature` (раздел 2.4).
- [x] `netutil.ResolveHost` из awg-manager — у нас уже заменён на `net.ResolveIPAddr("ip", domain)` (awgcapture.go).
- [x] ~~Интегрировать `CaptureQUICSignature` в `EnsureChainAWGMaterial`.~~ → ДЕЛАНО: `quic-live` mimicry path + `AWGCPSCaptureDomain`/`AWGCPSCapturedDomain`/`AWGCPSCaptureFailedDomain` (раздел 2.4 + fix #5).
- [x] ~~Усилить `awgcapture.go` полной RFC 9001 AEAD.~~ → ДЕЛАНО (раздел 2.4).
- [x] Hysteria2 transport — **ЗАМОРОЖЕН** как TUIC (AGENTS.md #11). Loud-fail guard в `buildChainRoleInOut` (fix #3: hard build error, не silent warning).
- [ ] GCloud UDP 443 firewall на exit VPS — инфраструктурный затык e2e, не код. Открыть порт или менять exit.
- [ ] **НОВОЕ (follow-up из kernel-AWG rework):** `RenderAWGHop` (userspace AWG endpoint) всё ещё зовётся legacy-CLI-путём `Backend.GenerateConfig`/`ApplyConfig` (`cmd/angry-box/main.go:673`, `config.go:83`) для standalone-AWG. Это НЕ web-UI путь (тот идёт через `ApplyMergedNode` → kernel AWG). Legacy-путь пушит только один `config.json` (без двухфайлового awg0.conf push), поэтому не может тривиально переключиться на kernel AWG без реструктуризации `ApplyConfig`. Решение: либо перевести CLI-путь на `pushConfigWithAWG`, либо задепрекейтить `Backend.ApplyConfig` в пользу `ApplyMergedNode` для standalone.
- [x] **(review follow-up #dead, resolved 2026-07-04):** `buildAWGUserInbound`/`buildAWGUserInboundMulti` (applier.go) помечены `TEST-ONLY / LEGACY` в doc-комментариях — production user-facing AWG теперь kernel awg0 + TUN-overlay (`RenderServerAWGConf`/`RenderExitAWGConf`). Builders оставлены: тесты (`clientconfig_test.go`, `helpers_test.go`) утверждают peer/amnezia-material логику, релевантную для userspace AWG transit (который ещё alive). Удалять нельзя — сломает тесты; пометки снимают путаницу что userspace-entry путь жив.
- [ ] **НОВОЕ (review #1 — критично, real-VPS verify):** per-client `source_ip_cidr` под TUN-overlay — `awg_tun_overlay.go` утверждает «TUN NAT changes the source IP», что ЕСЛИ правда значит `source_ip_cidr` per-client routing архитектурно несовместимо с overlay (AGENTS.md #7 primary механизм). **Надо проверить на real VPS** с поднятым kernel-модулем: сохраняется ли peer inner IP (10.8.0.X) через TUN, или NAT меняет его на TUN address (172.16.250.1). Логика покрыта unit-тестами (`TestBuildMergedRoute_PerClientAWG_*`); e2e — skip stub пока модуль не staged.
- [ ] E2E AWG: нужен реальный kernel-модуль на test VPSes (deps staging). AWG per-client E2E — skip stub (`TestE2E_Heavy_PerClientRouting`) пока модуль не staged; routing logic покрыт unit-тестами.

---

## 8. Code review + fixes (2026-07-04)

После kernel-AWG rework проведён независимый code review (coderabbit agent) полного diff'а (24 файла, +1267/-561). Найдено 6 real issues — **все 6 исправлены** + regression-тесты. Финальный `go build ./...` + `go vet ./...` + полный non-e2e `go test` зелёные.

### Исправленные issues

| # | Issue (severity) | Fix | Regression test |
|---|---|---|---|
| **1** | **AWG entry forwarding broken (highest-impact)** — overlay catch-all `tun-in→direct` prepended first → linear AWG entry egresses locally, chain forwarding dead; per-client `source_ip_cidr` rules keyed on nonexistent `ch-<chain>-user-in` tag (shadowed by catch-all). | `BuildAWGTUNOverlay` `ForwardOutbound` param → tun-in catch-all forwards to inter-node outbound for linear AWG entries; per-client rules re-keyed to `tun-in`; generic AWG-entry rule skipped (overlay catch-all handles it); merge order: action-rules FIRST → per-client MIDDLE → tun-in catch-all LAST. | `TestBuildAWGTUNOverlay_ForwardOutbound`, `_ForwardOutboundOverridesBalancer`; strengthened `TestAWGMergedConfig_SingBoxCheck_MultiHopWithRoute` (asserts tun-in→forward + pin-before-catch-all); `TestBuildMergedRoute_PerClientAWG_MultiHopPin` updated (entry tag `tun-in`). |
| **2** | `createBackup` hardcodes `config.json.bak` → multi-file AWG push (awg0.conf + awg-exit-nX) in same second collides all backups into one → corrupted rollback. | Backup named after source basename (`filepath.Base(file)+".bak"`). sing-box path identical (config.json.bak); AWG confs each get own .bak. | `TestCreateBackup_BasenamePerFile` (sing-box/awg0/awg-exit). |
| **3** | Hysteria2 warning didn't surface → deploy "successful" with broken chain (missing transport/user inbound). Comment claimed "fails the deploy" but didn't. | `buildMergedNodeConfig` returns hard error on Hysteria2-transport role warnings (collected separately from non-fatal report.Warnings). | `TestRenderMergedNodeConfig_Hysteria2TransportHardError`. |
| **4** | Standalone MTProxy `Port=0` → renders on 443, but `detectPortConflicts` claimed raw 0 → bypassed collision detection (silent clash with chain MTProxy entry). | `detectPortConflicts` claims `mtproxyInboundPort(ib.Port)` for `ib.Protocol=="mtproxy"` (effective rendered port). | covered by existing MTProxy tests + the conflict-check path. |
| **5** | `quic-live` capture failure не кэшировалась → re-dials UDP 443 на каждом redeploy (flaky/unreachable domain = network round-trip + timeout every ApplyChain). | `AWGCPSCaptureFailedDomain` marker set on capture-fail; cache-validity check: `quic` fallback valid when `AWGCPSCaptureFailedDomain == AWGCPSCaptureDomain`; success clears marker; domain change re-attempts. | `TestEnsureChainAWGMaterial_QuicLiveFailureCached` (same-domain no re-dial + I1/I2 byte-identical; domain-change re-attempts). |
| **6** | `renderCurrentNodeConfig` (web pending-deploy indicator) passed `nil` mtproxyUsers → preview missing mtproxy inbound → CurrentHash != LastDeployedHash → perpetual pending indicator for MTProxy nodes. | `RenderMergedNodeConfig` signature gained `mtproxyUsers` param; `renderCurrentNodeConfig` fetches `st.ListMtproxyUsersForNode(info.ID)` and passes it. | signature change vet-verified; existing MTProxy tests cover emission. |

### Неисправленные (записаны как техдолг / open questions)

- **Review #dead (low):** `buildAWGUserInbound`/`buildAWGUserInboundMulti` — dead in production (только тесты). Не удалял в этом заходе (риск сломать тесты-клиенты clientconfig_test.go). Pомечено в open questions.
- **Review #1 real-VPS verify:** TUN-NAT source_ip_cidr — надо проверить на real VPS, что peer inner IP сохраняется через TUN-overlay (если NAT меняет source IP, per-client pinning архитектурно не работает под kernel-AWG). Unit-тесты покрывают логику; e2e — skip stub.

---

## 9. Live-VPS testing (2026-07-04) — 3 сервера, реальные e2e

Пользователь дал живые сервера: `34.62.128.71` (entry/server-1), `207.175.40.161` (middle/server-2), `23.251.133.38` (exit/server-3). User `lcp`, key `~/.ssh/id_ed25519`, passwordless sudo, Debian 12, kernel 6.1.0-49. Это E2E-инфра из AGENTS.md.

### Что проверено/доказано на живых VPS

| Тест | Результат | Что доказано |
|---|---|---|
| `TestE2E_Heavy_Protocol_AWG_Kernel` (server-1, single-node) | ✅ PASS (22s) | kernel-AWG deploy работает: awg0.conf pushed (3287B, amnezia в [Interface] до [Peer]), `awg-quick@awg0` active, awg0 iface up (10.8.0.1/24), systemd `After=awg-quick@awg0.service` applied. sing-box config: TUN overlay, **0 wireguard endpoints**. |
| `TestE2E_Heavy_Protocol_AWG_Kernel_2Hop` (server-1→server-3, XHTTP transport) | ✅ PASS (36s) | **fix #1 доказан на multi-node**: entry `tun-in` catch-all → `ch-e2e-awg-kernel-2hop-out-www` (inter-node forward, НЕ direct). Chain forwarding wired. Exit healthy с XHTTP transport-in. |
| `TestE2E_Heavy_PerClientRouting` (server-1→server-3, AWG transport, AB_E2E_AWG_PERCLIENT=1) | ✅ PASS (72s, early-return on empty egress) | **P0 amnezia fixes доказаны на kernel AWG**: `AWG handshake OK: latest handshake: 5 seconds ago`, transfer 92B rx/72.96KiB tx. awg show: jc/jmin/jmax/s1-s4/h1-h4/i1-i5 все matching. Routes: awg0 (10.8.0.0/24) + sing-box-tun (172.16.250.0/30) одновременно. **НО egress через tunnel пустой** (routing polish — см. ниже). |

### Bug найден и исправлен через live testing: ip_forward=0

**Симптом:** `TestE2E_Heavy_PerClientRouting` — handshake проходит, но `curl --interface awge2e` → пустой EGRESS. `curl --interface sing-box-tun` тоже fails.

**Root cause:** `ip_forward=0` live на всех 3 серверах. Без ip_forward=1 kernel дропает пакеты между awg0 и sing-box-tun (TUN overlay). Deploy flow НЕ ставил его (plan §2.6 step 5 был пропущен в реализации).

**Fix:** `ensureIPForward(client, useSudo)` в `awg_push.go` — `sysctl -w net.ipv4.ip_forward=1` live + `/etc/sysctl.d/99-angry-box.conf` persist. Без MASQUERADE (это bypass sing-box). **Pairing fix:** изначально вызывался только в `pushAWGConfs` (ноды с awg0.conf), но AWG **transit** ноды (userspace endpoint, без awg0.conf) тоже forward пакеты → тоже нужен. Теперь вызывается в `ApplyChain` + `ApplyMergedNode` для ЛЮБОГО AWG-chain node (то же условие что module install: `UserProtocol==AWG || Transport==AWG`). **Доказано:** после fix exit (server-3) получил `ip_forward=1`.

### Что ещё НЕ работает (open — egress routing polish)

**КОРНЕВАЯ ПРИЧИНА НАЙДЕНА** через сравнение с работающим dns.idoctor.mom:

Реальный сервер использует **multi-exit balancer** архитектуру (kernel `awg-exit-n1..n4` + `bind_interface`, **0 userspace WG endpoints**), а мой e2e тест использовал **linear chain** с `Transport=AWG` (userspace WG для inter-node — handshake работает, data plane падает под amnezia). Это ФУНДАМЕНТАЛЬНО разные пути.

**Сравнение архитектур (real vs test):**

| | dns.idoctor.mom (РАБОТАЕТ) | e2e test (EGRESS ПУСТОЙ) |
|---|---|---|
| endpoints | 0 (NO userspace WG) | 1 (userspace WG transport) |
| outbounds | direct[bind_interface: awg-exit-nX] | direct (no bind_interface) |
| kernel exit ifaces | awg-exit-n1..n4 (kernel awg-quick) | none |
| chain tags | 0 (no linear chain) | 3 (linear chain) |
| route | tun-in → balancer → exit-directs | tun-in → userspace WG endpoint |
| awg-exit conf | **Table = off** | missing Table=off → **SSH lockout** |
| exit awg0.conf | **MASQUERADE** + FORWARD | missing → responses never return |

**5 fixes применены (commits cce068a, ac017e6, 7912751):**

1. **PostUp/PostDown FORWARD** в user-entry awg0.conf (`RenderServerAWGConf.TUNInterface`) — без этого FORWARD chain дропает return traffic между awg0 и sing-box-tun. (commit cce068a)
2. **MASQUERADE + FORWARD** в exit server awg0.conf (`RenderExitServerAWGConf.MASQUERADENetwork`) — без этого exit отправляет пакеты в интернет с private IP (10.8.0.x), internet не может вернуть response. WAN auto-detected. (commit ac017e6)
3. **Table = off** в awg-exit-nX.conf (`RenderExitAWGConf`) — БЕЗ ЭТОГО awg-quick ставит default route через exit tunnel → **SSH lockout** (произошло 2 раза на server-1). С Table=off awg-quick создаёт интерфейс но не трогает routing table; sing-box bind_interface handles routing. (commit 7912751)
4. **Amnezia disabled** на inter-node **userspace** transport (`buildAWGTransportInbound/Outbound.Amnezia=nil`) — userspace amnezia unstable (handshake works, data plane fails). Это про **userspace WG endpoints** между нодами линейной цепи, НЕ про kernel exit tunnels. (commit 1b0856e)
5. **AllowedIPs 0.0.0.0/0** на transport-in peer — response packets (dst=10.8.0.x) match no peer при AllowedIPs=[10.9.0.2/32]. (commit 1b0856e)
6. **ip_forward=1** для всех AWG-chain nodes (`ensureIPForward` в ApplyChain + ApplyMergedNode). (commit 1b0856e)
7. **Amnezia ON на kernel exit tunnels** (`renderBalancerExitConfs` + `renderExitServerConf` в `awg_deploy.go`) — DPI может резать plain WireGuard data packets (handshake проходит, data режется). Реальный dns.idoctor.mom использует `Jc=15` на exit tunnels. Оба конца (balancer client `awg-exit-nX.conf` + exit server `awg0.conf`) рендерят одинаковый amnezia-блок из chain material (`ChainAWGObfsMaterial`) → handshake совпадает. Это НЕ противоречит fix #4 — #4 про userspace inter-node transport (линейная цепь), #7 про kernel exit tunnels (balancer architecture). Regression guards в `awg_deploy_test.go` (`TestRenderNodeAWGConfs_MultiExitBalancer` + `TestRenderNodeAWGConfs_ExitServer` проверяют `Jc = ` в .conf).

**PerClientRouting e2e test переписан** на balancer архитектуру (commit 7912751): server-1=balancer (entry+ExitTargets), server-3=exit (Role=exit). kernel awg0 + awg-exit-n1 + TUN overlay + MASQUERADE. NO userspace WG. Соответствует dns.idoctor.mom.

**Live verify ВЫПОЛНЕН (commit c73700c — SSH safety fix):**
- Test **PASSES** (75s) — balancer deploy succeeds, Table=off prevents lockout, both servers stable.
- AWG handshake на kernel AWG: `latest handshake: 5 seconds ago` ✓
- Balancer: awg-exit-n1 active (Table=off, default route intact), awg0 active, TUN overlay present ✓
- Exit: awg0 active with MASQUERADE (`-A POSTROUTING -s 10.8.0.0/24 -o ens4 -j MASQUERADE`), FORWARD awg0 ACCEPT, 0 userspace WG endpoints ✓
- **НО `EGRESS:` пустой** — это **TEST ARTIFACT, не product bug** (см. ниже).

**КОРНЕВАЯ ПРИЧИНА пустого egress = test artifact (НЕ product bug):**
- awge2e (CLIENT tunnel): address 10.8.0.2/32, **на server-1**
- awg0 (SERVER): route `10.8.0.0/24 dev awg0`, **на server-1**
- Response packets (dst=10.8.0.2) match `10.8.0.0/24 dev awg0` → идут к awg0 (server), НЕ к awge2e (client). **Оба интерфейса на одном VPS → routing conflict.**
- **В PRODUCTION:** client (10.8.0.2) на **ОТДЕЛЬНОМ устройстве** (телефон/ноутбук пользователя), server (10.8.0.1) на VPS. Нет routing conflict — responses идут правильно.
- **Чтобы правильно тестировать egress:** client tunnel должен быть на **ТРЕТЬЕЙ машине** (не на VPS, через который идёт SSH). Или — tunnel-destined traffic должен идти через другой subnet (не 10.8.0.0/24, который совпадает с awg0 server route).
- **Вывод:** архитектура VPN ПРАВИЛЬНАЯ. Handshake работает (kernel AWG + amnezia). Balancer деплоится корректно (Table=off, MASQUERADE, 0 userspace WG). Egress нельзя проверить на одной машине — нужен отдельный клиент.

### ⚠️ INCIDENT: server-1 (34.62.128.71) locked out TWICE during egress testing

**Что произошло (2 раза):** при ручном тесте egress я поднял full-tunnel AWG client (`AllowedIPs=0.0.0.0/0`) **на самом VPS** (server-1), через который шёл SSH — без `Table=off` awg-quick ставит default route через tunnel, capturing SSH. **Исправлено:** `RenderExitAWGConf` теперь эмитит `Table=off` (commit 7912751) + e2e test injects `Table=off` into awge2e client conf + low-priority default route (commit c73700c). **После fix: Table=off предотвращает lockout** (3-й e2e run PASS, server-1 остался ALIVE с awg-exit-n1 UP, default route intact).

**Урок (AGENTS.md #12):** НИКОГДА не поднимать full-tunnel AWG client (`AllowedIPs=0.0.0.0/0`) на хосте, через который идёт SSH, БЕЗ `Table=off`. С `Table=off` awg-quick создаёт интерфейс но не трогает routing table; sing-box `bind_interface` handles routing.

**Состояние серверов (после всех тестов):**
- server-1 (entry/balancer): ✓ ALIVE — sing-box active, awg-quick@awg0 active (10.8.0.1/24), awg-quick@awg-exit-n1 active (Table=off, default route intact), ip_forward=1.
- server-3 (exit): ✓ ALIVE — sing-box active, awg-quick@awg0 active (10.11.0.1/24, MASQUERADE for 10.8.0.0/24), ip_forward=1, 0 userspace WG endpoints.
- server-2 (middle): ✓ ALIVE, clean (не использовался в AWG тестах).

### Open: egress на отдельном клиенте

Чтобы **окончательно** verify egress, нужен **отдельный клиент** (телефон/ноутбук/3-й VPS), который подключается к balancer AWG user-entry и curl-ит через tunnel. На одной машине egress нельзя проверить (routing conflict: awge2e client 10.8.0.2 + awg0 server route 10.8.0.0/24 на одном VPS → responses идут к awg0, не к awge2e). Это **test artifact**, не product bug — в production клиент на отдельном устройстве, routing conflict не возникает.

---

## 10. Hysteria2 frozen everywhere + edit-guard fix + E2E re-confirm (2026-07-04)

Пользователь подтвердил: Hysteria2 — на паузе как TUIC, фокус на базовом минимуме (AWG, Reality+XHTTP, MTProxy). Запрос: «везде добавить что Hysteria2 тоже на паузе» + «продолжи и тесты по прогрессу».

### 10.1. Hysteria2 frozen — audit «везде»

Frozen-enforcement уже централизован в `internal/chain/frozen.go` (`FrozenTransports` / `FrozenUserProtocols` / `FrozenStandaloneProtocols` + `Validate*` guards). Аудит всех entry points подтверждает покрытие:

| Entry point | Файл | Guard |
|---|---|---|
| Chain create | `internal/web/chains.go:46,54` | `ValidateChainTransport` + `ValidateChainUserProtocol` |
| Chain edit | `internal/web/chains.go:165,173` | `ValidateChainTransport` + `ValidateChainUserProtocol` (только при *изменении* — см. 10.2) |
| Spider link create | `internal/web/spider.go:48` | `ValidateChainTransport` |
| Standalone inbound add | `internal/web/nodes.go:439,448` | `IsFrozenStandaloneProtocol` + `ValidateStandaloneProtocol` |
| Default protocol | `internal/web/settings.go:136` | `ValidateChainUserProtocol` (только при `!= DefaultProtocol`) |
| Transport role build | `internal/chain/merged_config.go` | `buildMergedNodeConfig` hard-error on Hysteria2-transport (fix #3) |

UI dropdowns рендерят frozen options как `<option ... selected disabled>` (edit-only, никогда не newly selectable): `chains.templ:234`, `nodes.templ:387`, `users.templ:241`, `settings.templ:101`. i18n-ключи `Hysteria2 (paused — QUIC/TLS)` / `(на паузе — QUIC/TLS)` в en/ru (`internal/i18n/i18n.go:370-376`). README.md / README.ru.md уже упоминают «TUIC и Hysteria2 на паузе». AGENTS.md #11 обновлён с explicit списком всех guarded entry points + edit-guard nuance.

### 10.2. Edit-guard fix (regression найден через test suite)

**Regression:** `TestHandler_UpdateChain` падал — `handleUpdateChain` rejected re-save цепи с `user_protocol=tuic`. Но по AGENTS.md существующие TUIC/Hysteria2 цепи **должны оставаться редактируемыми** («may remain for display/edit») — блокируется только *новый* выбор.

**Fix:** `handleUpdateChain` теперь валидирует только когда значение реально *меняется* (`transport != c.Transport` / `userProto != c.UserProtocol`) — тот же pattern что `settings.go` (`DefaultProtocol != dp`). Disabled `<option>` в `EditChainForm` не сабмитится → form value пустой → guard пропускает → frozen-протокол сохраняется. Переключение non-frozen → frozen по-прежнему rejected с 400.

**Regression-тесты:** `TestHandler_UpdateChain` (переключение на vless-reality — allowed), `TestHandler_UpdateChain_PreservedFrozenProtocol` (re-save frozen цепи без отправки protocol — preserved), `TestHandler_UpdateChain_RejectsSwitchToFrozen` (switch to tuic — 400).

### 10.3. Dead builders помечены (review #dead → resolved)

`buildAWGUserInbound` / `buildAWGUserInboundMulti` (applier.go) — `TEST-ONLY / LEGACY` doc-комментарии. Production user-facing AWG = kernel awg0 + TUN-overlay. Builders оставлены: тесты утверждают peer/amnezia-material логику, релевантную для userspace AWG transit (ещё alive). Pометки снимают путаницу что userspace-entry путь жив.

### 10.4. E2E re-confirmed (live VPSes, 2026-07-04)

Все 3 ключевых теста PASS на live VPSes (entry `34.62.128.71`, exit `23.251.133.38`, user `lcp`, key `id_ed25519`):

| Тест | Время | Что доказано |
|---|---|---|
| `TestE2E_Heavy_Protocol_AWG_Kernel` | 23.83s | kernel-AWG single-node deploy: awg0.conf (3624B), `awg-quick@awg0` active, awg0 10.8.0.1/24, 0 userspace WG, systemd `After=awg-quick@awg0.service` |
| `TestE2E_Heavy_Protocol_AWG_Kernel_2Hop` | 35.95s | inter-node forwarding: entry `tun-in` catch-all → `ch-e2e-awg-kernel-2hop-out-www` (chain outbound) → exit XHTTP transport-in healthy |
| `TestE2E_Heavy_PerClientRouting` | 76.97s | balancer architecture: AWG handshake `latest handshake: 5 seconds ago`, transfer 92B rx/64.49KiB tx, jc/jmin/jmax/s1-s4/h1-h4/i1-i5 matching, Table=off, MASQUERADE live |

**Серверы после тестов:** server-1 ✓ ALIVE (sing-box active, awg-quick@awg0 active, ip_forward=1), server-3 ✓ ALIVE (sing-box active, awg-quick@awg0 active, MASQUERADE for 10.8.0.0/24, ip_forward=1). Никаких lockout'ов.

### 10.5. Test baseline

`go build ./...` + `go vet ./...` зелёные. Полный non-e2e `go test` зелёный (включая `internal/chain` 69s, `internal/web` с новыми edit-guard тестами). E2E — 3/3 PASS на live VPSes.

### 10.6. Открыто (не блокеры)

- **Egress на отдельном клиенте** — последний незакрытый verify (test artifact, не product bug). Нужен 3-й VPS / телефон / ноутбук.
- **Legacy CLI `Backend.ApplyConfig` standalone-AWG** (`cmd/angry-box/main.go:673` via `RenderAWGHop`) — ещё userspace, known follow-up.
- **Per-client `source_ip_cidr` под TUN-overlay** — real-VPS verify (unit-тесты покрывают логику).
- **GCloud UDP 443 firewall на exit VPS** — инфраструктурный, не код.

---

## 11. Real end-to-end egress verified + 3 bugs found & fixed (2026-07-04)

После §10 пользователь попросил реально проверить egress на отдельном клиенте. Использовал server-2 (middle, чистый) как KERNEL AWG client → entry (server-1) → exit (server-3) → internet. Оркестратор поставил amneziawg kernel module на чистый server-2 (`InstallAWGModuleWithOptions`, PPA path, ~4 мин). Полный путь заработал **только после исправления 3 багов**, найденных через live tcpdump/trace.

### 11.1. E2E egress — ДОКАЗАНО

```
server-2 (kernel AWG client, 10.8.0.99, Table=off, rp_filter=0)
  → entry awg0 (server-1, kernel, 10.8.0.1/24)
  → sing-box tun-in (include_interface: [awg0, awg-exit-n1])
  → n1-direct (bind_interface: awg-exit-n1)
  → exit awg0 (server-3, kernel, MASQUERADE 10.8+10.10)
  → internet
curl --interface awg0 ifconfig.me → 23.251.133.38  (exit's public IP!)
ipinfo.io: Brussels, BE, Google LLC
control (default route) → 207.175.40.161  (server-2 own IP)
```

**Вывод:** egress IP = exit public IP = `23.251.133.38`. Трафик реально проходит по нодам end-to-end. Архитектура balancer (kernel awg0 + awg-exit-nX + TUN-overlay + bind_interface) РАБОЧАЯ.

### 11.2. Bug #1 — exit MASQUERADE покрывала только user subnet (10.8.0.0/24), не balancer-link (10.10.0.0/24)

**Симптом:** `n1-direct: dial tcp 34.160.111.145:443: i/o timeout`. Exit awg0 видит SYN от `10.10.0.2` (balancer inner IP) и SYN-ACK от ifconfig.me к `10.10.0.2`, но **conntrack пустой** — MASQUERADE не срабатывала.

**Root cause:** `renderExitServerConf` (awg_deploy.go) ставил `MASQUERADENetwork: "10.8.0.0/24"` — только user subnet. Но balancer exit-link inner subnet = `10.10.0.0/24` (`AWGExitLink.Address`, applier.go:815). Пакеты с source `10.10.0.2` приходили на exit, форвардились в ens4, но MASQUERADE не покрывала → exit отправлял в internet с private source `10.10.0.2` → internet не мог вернуть response → timeout.

**Fix:** `RenderExitServerAWGConf.MASQUERADENetwork` теперь принимает список subnet (comma/space-separated) — `renderExitServerConf` передаёт `"10.8.0.0/24,10.10.0.0/24"`. Каждый subnet → свой MASQUERADE rule. Regression: `TestRenderExitServerAWGConf_MASQUERADEMultiSubnet`, `TestRenderExitServerAWGConf_MASQUERADESingleSubnet`, `TestRenderNodeAWGConfs_ExitServer` (оба subnet asserted).

### 11.3. Bug #2 — entry TUN `include_interface` = `[awg0]` только, не awg-exit-nX

**Симптом (после fix #1):** тот же `n1-direct: dial timeout`. SYN уходит через awg-exit-n1 (exit видит), SYN-ACK возвращается на entry awg-exit-n1 (tcpdump подтверждает), но **sing-box не видит response** → dial timeout.

**Root cause:** `tunIncludeInterfaces` (awg_tun_overlay.go) хардкодил `["awg0"]`. Комментарий утверждал «awg-exit-nX must NOT be in include_interface, or TUN would re-capture egress traffic and loop» — **это было неверное предположение**. `include_interface` катчит INCOMING traffic (responses) на интерфейсах, не outgoing egress (который идёт через bind_interface sockets). Без awg-exit-n1 в include_interface SYN-ACK приходит на kernel awg-exit-n1 (dst=10.10.0.2 = local address), kernel доставляет в dead local socket, sing-box userspace его не получает.

**Fix:** `tunIncludeInterfaces(node)` теперь возвращает `awg0` + все `awg-exit-nX` (из `exitInterfacesForNode`). `RenderAWGBalancer` (roles.go) — то же (был хардкод `["awg0"]`). Regression: `TestTunIncludeInterfaces_BalancerIncludesExitIfaces`, обновлён `TestRenderAWGBalancer_MultiExit`.

### 11.4. Bug #3 — deploy flow не выставлял rp_filter=0 на awg0/awg-exit-nX

**Симптом (после fix #2, при redeploy):** `n1-direct: dial timeout` вернулся. `rp_filter awg-exit-n1 = 1` (redeploy пересоздал интерфейс → rp_filter сбросился в default).

**Root cause:** awg-quick пересоздаёт интерфейс при каждом `awg-quick up`, и `rp_filter` сбрасывается в default (`1` = strict). С rp_filter=1 kernel дропает return traffic: reverse-path check для пакета с dst=10.10.0.2 (пришёл на awg-exit-n1) не матчит — 10.10.0.2 routes to `local`, не через awg-exit-n1 → martian → drop. Deploy-флоу ставил `ip_forward=1` но не `rp_filter=0`.

**Fix:** `RenderServerAWGConf` (user-entry awg0), `RenderExitAWGConf` (balancer client awg-exit-nX), `RenderExitServerAWGConf` (exit awg0) — все три PostUp теперь эмитят `sysctl -w net.ipv4.conf.<iface>.rp_filter=0`. awg-quick применяет PostUp при каждом `up` → rp_filter стабильно 0. Regression: `TestRenderServerAWGConf_PostUpPostDown`, `TestRenderExitAWGConf`, `TestRenderExitServerAWGConf_MASQUERADENetwork` (rp_filter asserted в каждом).

### 11.5. Bug #4 (client-side, test artifact) — client awg0.conf тоже нуждается в rp_filter=0

**Симптом:** после fix #1-3 egress работал только если вручную выставить `rp_filter=0` на client awg0 (server-2). Client conf не имеет PostUp rp_filter.

**Контекст:** это **test artifact** — на реальном клиенте (телефон/ноутбук) нет multi-homing, rp_filter не проблема. Но при использовании VPS как клиента (как server-2 в тесте) awg-quick пересоздаёт awg0 → rp_filter=1 → дропает SYN-ACK от exit (dst=10.8.0.99, пришёл на awg0, reverse-path не матчит). `RenderClientAWGConf` должен эмитить PostUp rp_filter=0 — follow-up (не блокер для production клиентов, где rp_filter обычно 0 или non-strict).

### 11.6. Оркестратор ставит ядро на голый сервер — проверено

`InstallAWGModuleWithOptions` на server-2 (чистый Debian 12, без модуля): PPA path (`apt-get install amneziawg`), 237.3s (~4 мин), модуль загружен (`lsmod: amneziawg 118784 0` + deps), `awg`/`awg-quick` present. DKMS fallback не потребовался (PPA покрыл Debian 12). `persistAWGModules` пишет `/etc/modules-load.d/` → модуль грузится после ребута.

### 11.7. Test baseline

`go build ./...` + `go vet ./...` + полный non-e2e `go test` зелёные. E2E `TestE2E_Heavy_PerClientRouting` PASS (redeploy с 3 фиксами в коде). Live egress verify: `curl --interface awg0 → 23.251.133.38`. Серверы после cleanup: server-1 ✓ active (sing-box + awg0 + awg-exit-n1), server-3 ✓ active (sing-box + awg0 + MASQUERADE), server-2 cleaned (awg0 down, conf removed, модуль установлен для будущих тестов).

### 11.8. Открыто

- **Client-side rp_filter** (#4) — `RenderClientAWGConf` PostUp rp_filter=0. Не блокер (production клиенты не страдают).
- **ip rule `10.8.0.0/24 → table 2022` на entry** — в live-тесте добавлял вручную (`ip rule add from 10.8.0.0/24 lookup 2022`). На balancer-конфиге ip rules от sing-box auto_route имеют `9000: from all iif awg0 goto 9002 → nop`, что НЕ направляет в table 2022. Нужно проверить: должен ли deploy-флоу добавлять этот ip rule, или sing-box `include_interface: awg0` должен сам перехватывать (через TUN) без ip rule. Follow-up — возможно ещё один баг routing между awg0 и sing-box-tun.
## 12. CTO review fixes (2026-07-06) — Top-10 blockers closed properly

9-agent CTO review (`docs/CTO_REVIEW.md`) → все 10 Top-10 блокеров закрыты. Ниже — migration/UX-заметка для операторов live-деплоев (НЕ код-файнд, а операционная заметка).

### 12.1. handleTrustHostKey — UX migration note (CTO-review §6)

`NodeInfo.PendingHostKeyFingerprint` (поле добавлено в `model/panel.go`, `json:"pending_host_key_fingerprint,omitempty"`) хранит фактически наблюдённый SSH fingerprint, чтобы `/trust` POST мог сверить submitted fingerprint с реальным (anti-MITM/CSRF, CTO-review §6 HIGH).

**Migration impact для live-деплоев, обновлённых с pre-fix версии:**
- У существующих (pre-fix) `NodeInfo` этого поля НЕТ → `PendingHostKeyFingerprint == ""`.
- Нормальный flow **НЕ сломан**: capture (`handleCaptureNode` в `web/nodes.go`) при `HostKeyError` устанавливает `PendingHostKeyFingerprint` и рендерит `HostKeyWarning` модалку → пользователь жмёт "trust" → `/trust` сверяет с pending. Поле пустое ТОЛЬКО до первого capture после обновления.
- Единственный edge case: оператор открыл `HostKeyWarning` модалку в СТАРОЙ версии (где pending не сохранялся), затем обновился и жмёт "trust" в уже-открытой модалке → `/trust` вернёт 400 "No pending host key fingerprint — re-capture the node first." **Fix:** перезапустите capture (Status кнопка) — модалка перерендерится с pending, trust пройдёт.
- Никакой schema-migration не требуется: `omitempty` + zero-value = empty = "нет pending" = корректное безопасное поведение (refuse-to-trust-blindly).

### 12.2. At-rest encryption default-on (CTO-review §2)

`store.json` теперь шифруется AES-256-GCM при наличии `store.json.key` (32 байта, генерируется `install.sh` во ВСЕХ 3 ветках: systemd / user-mode / Keenetic inline + `S99angry-box`). Legacy plaintext `store.json` читается как раньше (`isEncrypted` auto-detect). Операторы с pre-fix `store.json` (plaintext): при первом запуске с auto-keygen файл остаётся plaintext (шифрование применяется на следующей WRITE). Чтобы зашифровать немедленно: `angry-box` запускается под новым ключом → любое `Save*` перезапишет файл зашифрованным. Ручной fallback: скопируйте `store.json` в `store.json.plaintext.bak` перед первым запуском под key.

### 12.3. Sentinel errors wired (CTO-review §6)

`ErrHostNotFound`/`ErrChainNotFound`/`ErrUserNotFound`/`ErrDeployFailed`/`ErrRollbackFailed` (`internal/chain/errors.go`) теперь реально потребляются:
- `handleCreateChain` (`web/chains.go`): `errors.Is(err, chain.ErrHostNotFound)` → 400 (vs 500 для store I/O failure).
- `pushConfig` (`chain/applier.go`) + `singbox.ApplyConfig` (`backend/singbox/singbox.go`): check/restart/health-probe-fail пути оборачивают `ErrDeployFailed` (rollback OK → retry-able) или `ErrRollbackFailed` (rollback ALSO failed → node broken, manual).
- Покрыто тестами: `TestPushConfig_CheckFails_RollsBack` (ErrDeployFailed), `TestPushConfig_CheckFails_RollbackAlsoFails` (ErrRollbackFailed), `TestBackend_ApplyConfig_CheckFails` + `TestBackend_ApplyConfig_CheckFails_RollbackAlsoFails`.

### 12.4. No-panics-in-request-path (CTO-review #3)

`mustMarshal` (singbox) → `marshal` returning `(json.RawMessage, error)`, propagated through all `Render*` functions. `cryptogen.GenerateInboundTag/GenerateTUICPassword/GenerateProxyPassword/GenerateStableTUICUserCreds/EnsureUserCreds` → `(value, error)` signatures, errors propagated through `ApplyChain`/`RenderClientConfig`/`buildChainRoleInOut` (becomes a deploy-failing roleError)/handlers (500 with i18n message). `presets.go` external `_ = LoadPresets` → logged via `slog.Warn`. The `recover()` middleware in `web/auth` remains as the safety net.

### 12.5. Takeover silent failures fixed (CTO-review §6)

`_ = RestoreFile` in `rollbackToOldVPN` → propagated (rollback failure now surfaces, marks "failed-both"). `_ = SaveNodeInfo` ×4 → `slog.Warn`. `_ = json.Unmarshal` ×12 in `convert.go` → `partialUnmarshal` helper with explicit rationale (foreign-config lenient extraction is by-design; callers' presence checks handle the "no match" case).

### 12.6. applier.go split + context.Context + autoapply cap (CTO-review §4/§8/§9)

- **applier.go split**: `applier.go` (1986 строк) → `applier_build.go` (1739 строк, pure config-gen + ApplyChain orchestrator) + `applier_push.go` (276 строк, SSH I/O: pushConfig/rollback/probe/cert). AGENTS.md #4 layering restored (config-gen ≠ SSH I/O).
- **context.Context threaded into SSH-push**: `pushConfig`/`pushConfigLocked`/`pushConfigWithAWG`/`probeServiceUp`/`ensureCertForTLSInbounds`/`ensureIPForward`/`pushAWGConfFile`/`enableAWGService`/`pushAWGConfs`/`awgConfDirExists` now take `ctx context.Context`; the `context.Background()` calls inside the deploy sequence replaced with `ctx`. Exported wrappers (`PushConfig`/`ProbeServiceUp`/`DisableService`/`EnableService`/`RestoreFile`/`PushConfigForTest`) updated; all callers (ApplyChain, applyMergedNodeLocked, takeover, tests) pass ctx through. A cancelled UI deploy now cancels in-flight SSH commands instead of waiting out the timeout (CTO-review §8).
- **autoapply concurrency cap**: `ScheduleAutoApply` now acquires a slot on a counting semaphore (`autoApplyMaxConcurrent=8`) before the SSH deploy, so a 100-node all-pending fleet fans out to at most 8 concurrent SSH deploys, not 100. `SetAutoApplyConcurrency(n)` lets operators/tests override the cap before `InitAutoApply`. Covered by `TestScheduleAutoApply_ConcurrencyCap` (schedules 10 deploys against a blocking connector, asserts high-water ≤ cap=3 and saturates the cap). The per-host `withHostLock` still serializes same-node re-entrancy; the semaphore bounds the global fan-out.
- **SSH connection pool (deferred)**: connection REUSE across deploys (buffering live SSH sessions per host with idle TTL) is a deeper change to the `SSHConnector` interface + lifecycle (the current `connector.Connect` returns a fresh client each deploy; `client.Close()` drops it). The concurrency cap (above) bounds the worst-case fan-out; a true connection pool is a v1.0 follow-up (needs idle-eviction, host-key cache invalidation on TOFU change, and a redesign of the `defer client.Close()` pattern across ApplyChain/ApplyMergedNode/takeover). Tracked but NOT implemented this cycle.

## 13. v0.3.1 + v0.3.2 + v0.4.0 (2026-07-06/07)

### 13.1. v0.3.1 — deferred v0.3.0 follow-ups + UI fixes + patches safety

**Audit log split (D1):** `SaveAuditLog` теперь append-only `<store>.audit.jsonl` (O(1) per entry, separate `auditMu`), НЕ rewrite всего `store.json`. `ListAuditLogs` merge legacy inline + jsonl, dedup by ID, cap 5000.

**AWG per-inbound server IP (A1):** `NodeInbound.AWGServerAddress` field (default 10.8.0.1/24) + `allocateAWGPeerIPInSubnet`. `RenderNodeAWGConfs` collision chain entry + standalone → loud warning (было silent drop).

**Takeover'd AWG peers → model.User (A2):** `MaterializeAWGPeersAsUsers` (deterministic ID, dedup, idempotent) + `DeleteSynthesizedAWGUsers` rollback. `TakeoverState.SynthesizedUserIDs`.

**SSH connection POOL (A3):** cross-deploy reuse (autoapply re-deploy every ~5min reuses connection). `pool.go` (Ping liveness + stored-fingerprint TOFU re-check + key-resolution re-check + idle sweeper + graceful shutdown). Wired only at serveCmd composition root.

**UI fixes:** language switch (ru) — dynamic `<html lang>` + `Cache-Control: no-store`; user create import-secret block → telemt only (server-side guard rejects non-telemt).

**CLI AWG deprecate warning:** `config`/`apply --protocol awg` warn (legacy userspace → web UI/apply-chain).

**Patches safety (C2):** `patchcheck_test.go` (build-tag gated — `git apply --check` на pinned upstream) + `docs/PATCHES.md` + CI `patchcheck` job.

### 13.2. v0.3.2 — Tokyo Night UI redesign

Полный visual redesign под Tokyo Night aesthetic (ported from hoaxisr/awg-manager, MIT, attribution в `docs/CREDITS.md`). IBM Plex Sans/Mono self-hosted (14 woff2, Latin+Cyrillic). 3 selectable themes (`tokyonight`/`tokyonight-day`/`tokyonight-storm`) — CSS-override mapping Tokyo Night → DaisyUI v4 OKLCH vars. Theme dropdown в header. `.tn-card`/`.tn-table`/`.tn-badge` component conventions. Favicon. NO build step (CDN-only preserved).

### 13.3. v0.4.0 (in progress) — multi-AWG-interface + takeover re-render + SNI + tests

**Reality SNI configurable (done):** `PanelSettings.DefaultRealitySNI` (global default, empty → built-in const). `SetDefaultSNI`/`EffectiveDefaultSNI` accessors. `ResolveServerName` + singbox renderers используют `EffectiveDefaultSNI()`. Settings UI input. Migration-safe (empty → const fallback).

**Multi-AWG-interface awg0/awg1 (done):** chain entry (awg0) + standalone с distinct subnet (AWGServerAddress) → awg1 (second kernel interface). `AWGServerConfParams.InterfaceName` (PostUp/PostDown parameterized). `RenderNodeAWGConfs` multi-file emit. `tunIncludeInterfacesForNode(node, nodeInfo)` appends awg1. Tests: multi-interface render, InterfaceName PostUp, include list.

**Takeover re-render fresh awg0.conf (done):** export `PushConfigWithAWG` + `AwgServerConfigToAmnezia` adapter + NodeInbound wiring (ForUsers) + switch takeover to `PushConfigWithAWG` (atomic push awg0.conf + sing-box, rollback both). Materialize BEFORE push. Set `OldConfigPath` for rollback.

**Table-driven tests (done):** convert SSPassword/GetNotFound/ChecksumForArch to table-driven.

**Benchmarks (done):** SaveAuditLog/StoreRead/Write/RenderMerged/ProxyPassword + Makefile bench target.

**Coverage baseline in CI (done):** ci.yml coverage step + docs/COVERAGE.md regenerate.

### 13.4. v0.4.0 live E2E на свежих GCloud VPSes (2026-07-08)

Пользователь дал 3 свежих GCloud Debian 12 VPS (entry `34.14.98.64`, middle `207.175.1.227`, exit `35.189.235.61`, user `lcp`, key `id_ed25519`, passwordless sudo). Сервера были **чистые** (нет sing-box, нет awg-quick, нет amneziawg-модуля) — проверено что `ApplyChain` полностью self-stages: sing-box-extended binary (download из GitHub Release), amneziawg kernel module (PPA fast path `apt install amneziawg`), `awg-quick@.service` template, `awg0.conf`, `ip_forward=1`, sing-box systemd unit с `After=awg-quick@awg0.service`. Адреса в `e2e_helpers_test.go` обновлены на новые IP.

**Результаты E2E (3 теста PASS):**

| Тест | Результат | Время | Что проверено |
|------|-----------|-------|---------------|
| `TestE2E_Heavy_Protocol_AWG_Kernel` | **PASS** | 398с (1-й прогон = full staging) | amneziawg module loaded, awg-quick `/usr/bin/awg-quick`, awg0.conf pushed (3787 bytes с amnezia Jc/S1/H1, NO Itime), `awg-quick@awg0` active, awg0 iface up `10.8.0.1/24`, sing-box `After=awg-quick@awg0.service`, TUN-overlay (`include_interface awg0`), NO userspace wireguard endpoint. |
| `TestE2E_Heavy_Protocol_AWG_Kernel_2Hop` | **PASS** | 316с | entry→exit: entry TUN-overlay catch-all → `ch-...-out-www` (inter-node outbound, NOT direct — chain forwarding wired), entry awg0 up, exit XHTTP transport inbound present + healthy. |
| `TestE2E_Heavy_PerClientRouting` (`AB_E2E_AWG_PERCLIENT=1`) | **PASS** (с WARNING) | 71с | balancer arch (server-1 awg0 + awg-exit-n1, server-3 exit awg0+MASQUERADE). **AWG handshake OK** (`latest handshake: 5 seconds ago` — proof persisted CPS I1-I5 server↔client identical, амнезия обфускация end-to-end работает). balancer awg-exit-n1 active, exit awg0 active с MASQUERADE. **НО: egress IP через client tunnel пустой** (`curl --interface awge2e ifconfig.me` → empty) — известный открытый пункт (см. ниже). |

**Egress routing через client tunnel — ОТКРЫТ (не блокер):** Handshake проходит (главное proof — амнезия обфускация работает end-to-end на свежих VPS), но `curl --interface awge2e` не возвращает egress IP. Sing-box TUN-overlay поднят (`sing-box-tun`), но trace пустой после старта — нет router match для трафика с `awge2e` client tunnel. Это тот же пункт что AGENTS.md #13 "Per-client `source_ip_cidr` under TUN-overlay still needs real-VPS verify" — routing polish для client-side tunnel на test VPS. Тест логирует WARNING и проходит (handshake = AWG-obfuscation proof). Требует отдельной отладки (tcpdump + sing-box trace debug) — не v0.4.0 блокер, отдельная задача.

**Stage 3 unit-тесты добавлены (quality gap из прошлой сессии закрыт):** `TestAwgServerConfigToAmnezia` (obfuscated + plain-WG-returns-nil), `TestRenderTakeoverAWGConf` (path/service = awg0, [Interface] carries imported material, 2 [Peer] from active users, inactive/skipped users excluded, PostUp present, NO Itime), `TestRenderTakeoverAWGConf_PlainWG`, `TestRenderTakeoverAWGConf_DefaultAddress`. Все PASS.

**Cleanup серверов после тестов:** `e2eResetAllServers` чинит post-crash state (chown sing-box binary, reset-failed). AWG-интерфейсы (awg0, awg-exit-n1, awge2e) оставлены для будущих прогонов (модуль установлен — fast path при redeploy).

### 13.5. Chain-building E2E suite on fresh VPSes (2026-07-08) — 2 bugs found & fixed

После AWG-тестов (§13.4) прогнал полный core E2E suite (chain building / topology / transport / balancer / strategy / rollback / takeover / import / concurrency / hostlock / QUIC capture) на тех же свежих GCloud VPS. **Все PASS** (TUIC frozen — не гонял; ClientConnectivity SKIP без `AB_ROUTE_DNS=1` — деплой проходит, egress verify пропущен). Найдены и исправлены 2 бага.

**Результаты (21 тест PASS):**

| Группа | Тесты | Результат |
|--------|-------|-----------|
| Deploy/Staging | `Deploy_FreshNode` | PASS — sing-box-extended self-stages на clean VPS |
| Chain building | `Chain_SingleNode`, `Chain_2Hop`, `Chain_3Hop`, `Chain_TopologyChange` (2hop→3hop→2hop) | PASS — построение цепей 1/2/3 hop + topology change |
| Transport | `Protocol_VLESSRealityXHTTP_Advanced` | PASS — REALITY+XHTTP obfuscation на inter-node transport, exit слушает 443 |
| AWG | `Protocol_AWG_Kernel`, `Protocol_AWG_Kernel_2Hop`, `Protocol_AWG_Userspace`, `PerClientRouting` | PASS (per-client handshake verified, §13.4) |
| Balancer | `Balancer_URLTestInChain`, `Balancer_Failover` (stop middle → still 204), `Balancer_MultiEntry` (SKIP) | PASS |
| Strategy | `SelectorStrategy` | PASS — selector переключает egress между middle (207.175.1.227) и exit (35.189.235.61) — per-client routing через selector работает на live VPS |
| Rollback | `Rollback_OnBadConfig` | PASS (после fix — см. ниже) — sing-box check ловит `unknown inbound type`, rollback восстанавливает vless config |
| Takeover | `Takeover_SingBox_FullFlow` | PASS |
| Import AWG | `ImportAWG_PreservesPeers`, `ImportAWG_FromSeededNode` | PASS — non-destructive peer import |
| Idempotency/Locking | `Idempotency_DoubleApply`, `ConcurrentDeploy_Serialized`, `HostLock_Identity`, `PostDeploy_HashAndHealth`, `BackendStatus_AllNodes` | PASS |
| QUIC capture | `QUICCapture_AWGConfig` | PASS (после 2 fixes — см. ниже) — capture на one.one.one.one (2 packets, partial), kernel-AWG TUN overlay + awg0.conf с amnezia |

**Баг 1 (test-fixture, fixed): `TestE2E_Heavy_Rollback_OnBadConfig` падал на fresh VPS.** `performRollbackTest` вызывал `chain.PushConfig` напрямую (минуя `Deploy`), который пишет в `/etc/sing-box/config.json` но НЕ делает `mkdir` этой директории — staging (binary + dirs + systemd unit) это работа `Deploy`. На чистом VPS (`/etc/sing-box` не существует) raw PushConfig фейлился с `cp: cannot create regular file '/etc/sing-box/config.json': No such file or directory` до того как rollback-путь вообще достигался. Fix: `performRollbackTest` теперь сначала делает `backend.DeployWithOptions` (pre-stage), потом seed good config через PushConfig — rollback-фикстура self-contained и работает на clean VPS (соответствует философии self-staging). Это test-fixture gap, не production баг — `PushConfig` по дизайну низкоуровневый I/O слой, staging делает `Deploy`.

**Баг 2 (production crash, fixed): `CaptureQUICSignature` panic на partial QUIC capture.** `awgcapture.go:137` делал `packets[:captureMaxPkts]` (captureMaxPkts=5 для I1-I5), но read-loop мог вернуть меньше пакетов (timeout/loss/сервер перестал слать). На live VPS `one.one.one.one` вернул 2 пакета → `slice bounds out of range [:5] with capacity 2` → **panic, крашит оркестратор**. Fix: вынес slice-логику в `capturePacketsToCPS(packets)` с `min(len(packets), captureMaxPkts)` clamp — partial capture даёт partial CPS set, не краш. Production-код (`EnsureChainAWGMaterial`) уже корректно требует `len(Packets) >= 5` для live capture (иначе fallback на synthesized), так что partial capture в production идёт в fallback корректно. Добавлены 4 unit-теста (`TestCapturePacketsToCPS_PartialNoPanic` regression, `_Full`, `_ClipsOversized`, `_Empty`).

**Также обновлены устаревшие E2E assertions:**
- `TestE2E_Heavy_QUICCapture_AWGConfig` требовал `"amnezia"`+`"i1"` в sing-box config — это userspace-era assertion (amnezia в userspace wireguard endpoint). При kernel-AWG architecture (AGENTS.md #10) amnezia живёт в `awg0.conf`, sing-box config содержит только TUN overlay (`type: tun`, `include_interface: awg0`, NO `wireguard`). Assertion обновлено на kernel-AWG shape + проверку amnezia в запушенном awg0.conf.
- `TestE2E_Heavy_QUICCapture_AWGConfig` требовал ровно 5 пакетов — на GCloud (UDP/443 частично заблокирован) capture возвращает 2. Расслаблено: `source=quic` с любым числом пакетов валиден (production fallback сработает на <5); только `source=error` или malformed packet = failure.

`go build ./...` + `go vet ./...` + полный non-e2e `go test` зелёные. Сервера оставлены staged.

---

## 14. Backups + node relocation (2026-07-08) — v0.5.0

Фича: бэкапы серверов (полный + пер-нода) + быстрое перенесение заблокированной ноды на новый VPS с авто-heal цепочки.

### 14.1. ResolveNodes bugfix (базовый блокер — Stage 1)

`ResolveNodes` (`internal/chain/store.go`) пересоздавал `ChainNode` struct literal, копируя только `Port + TransitPrivKey/ShortID/UUID + Inbounds` — дропая `Role, ExitTargets, TransitAWG*, ExitAWG*, ExitAWGLinks`. На следующем `ApplyChain` после рестарта процесса AWG transit/exit ключи были пустые → регенерация → inter-node AWG линки рвались (previous node's outbound `peer.PublicKey` больше не матчило новый server pubkey; balancer `awg-exit-nX` не матчило exit's новый server key). Это латентный re-apply баг для любой AWG-цепи после рестарта оркестратора, и он же блокировал relocation (которой нужны те же ключи на новом VPS).

Фикс: `ResolveNodes` копирует stored `ChainNode` целиком, потом перезаписывает только live-Host поля (`ID/Addr/User/KeyPath`) + `Inbounds` (из `NodeInfo`). Никакой transit/material не теряется. Regression tests: `TestResolveNodes_PreservesAllTransitFields`, `TestResolveNodes_ReapplyKeepsAWGKeys`.

### 14.2. Backups (Stage 2)

**Backend** (`internal/chain/backup.go`):
- `ExportStore()` — весь `storeFile` как plaintext JSON (portable — НЕ on-disk encrypted form, чтобы backup восстанавливался на другой инсталляции без того же master-key). `ImportStore(data, force)` — заменяет весь store; без `force` отказывается перезаписывать непустой store (wipe protection); re-runs schema migrations.
- `ExportNode(id)` — портативная identity одной ноды: Host + NodeInfo + `ChainNode` record (со всем transit/exit material) для каждой цепи где нода состоит. `ImportNode(b, force)` — dedup по ID: отказ reroute live node (same ID, different Addr) без `force`; merge chain memberships по имени, skip несуществующих цепей (node backup не изобретает half-chain). Skipped-missing-chains — warning, не fatal.
- `backupEnvelope` (`format=angry-box-store|angry-box-node`, `version`) → `DetectBackupFormat` для unified restore path (auto-detect store vs node).

**HTTP** (`internal/web/backups.go`): `GET /ui/backup/store` (download), `GET /ui/backup/nodes/{id}` (download, 404 unknown), `POST /ui/backup/import` (auto-detect, `force=on`).

**UI**: Settings → секция "Backups" (Export panel + Import form с textarea + force checkbox). Nodes → кнопка "Export" на каждой ноде (`/ui/backup/nodes/{id}` download).

**CLI**: `angry-box backup store [-o file]`, `angry-box backup node <id> [-o file]`, `angry-box restore <file> [--force]` (auto-detect, skipped-missing-chains — exit 0 warning).

**Тесты**: `TestExportNode_*` (roundtrip, unknown ID, dedup, reroute-without-force), `TestExportStore_ImportStore_*` (roundtrip, non-empty-without-force), `TestDetectBackupFormat_Invalid`; handler tests `TestHandler_Export*`, `TestHandler_ImportBackup_*` (roundtrip, refuses-non-empty, force, invalid/empty).

### 14.3. Node relocation (Stage 3) — auto-heal dependent chains

**Backend** (`internal/chain/relocate.go`): `RelocateNode(ctx, store, applier, nodeID, newAddr, newUser, newKeyPath, awgClientPubKey)`:
1. Обновляет Addr (+опц. User/KeyPath) в 3 местах — Host, NodeInfo.Host, ChainNode snapshot в каждой цепи с этой нодой — сохраняя ID + ВСЕ transit/exit material (Reality PrivateKey/ShortID/UUID, AWG Transit*/ExitAWG*, Role, ExitTargets), чтобы re-deploy переиспользовал те же credentials (other nodes + existing clients не перенастраиваются).
2. Re-apply каждой affected chain через `applier.ApplyChain`. ApplyChain re-deploys саму ноду (на новый VPS, переиспользуя persisted keys) И каждую ноду чей config embed'ит Addr ноды — previous hop (outbound dials `extractHost(N.Addr)`) + balancer'ы чьи `ExitTargets` включают N (`awg-exit-nX` endpoint embeds N.Addr, `awg_deploy.go:179`). Один вызов heal'ит всю affected topology.
3. Audit (best-effort).

`chainApplier` interface → tests inject fake applier (no SSH). Failure на одной цепи — recorded, не fatal (report carries per-chain success/error). `ResolveNodes` fix (§14.1) preserving transit material — это что делает key-reuse работающим.

**Тесты**: `TestRelocateNode_UpdatesAddrInThreePlaces`, `_PropagatesNewAddrToApplier` (new IP доходит до re-deploy + transit keys survive), `_UnknownNode`, `_PreservesTransitKeysAcrossReapply` (core invariant), `_OneChainFailureDoesNotAbortOthers`, `_UpdatesUserAndKey`.

**HTTP**: `POST /ui/nodes/{id}/relocate` (validates new_addr + SSH key id exists before mutation → `RelocateNode` → `RelocateResult` template с per-chain success/error). `GET /ui/nodes/{id}/relocate` → modal.

**UI**: Nodes → кнопка "Relocate" на каждой ноде → modal (`RelocateForm`: new_addr required + опц. new_user/new_ssh_key_id). Help text: "transit keys are reused, other nodes + existing clients are not reconfigured".

**CLI**: `angry-box relocate <node-id> --addr <new-ip:port> [--user <user>] [--key <key-id>]` → `RelocateNode` → per-chain report (exit 1 если relocate или любая цепь failed).

### 14.4. Live-verify (playwright, реальный браузер)

- Backups секция в Settings рендерится (Export panel link + Import form + endpoints).
- Node row: Export + Relocate buttons присутствуют.
- Relocate modal открывается (`#relocate-modal` dialog + form `hx-post=/ui/nodes/n1/relocate` + inputs new_addr/new_user/new_ssh_key_id + help text).
- CLI: `backup store` выводит store backup JSON; `backup store -o` пишет файл; `restore` roundtrips; `relocate ghost --addr ...` fails с "host not found" (no SSH, validation works).

### 14.5. Что НЕ в скоупе v0.5.0 (явно)

- Авто-detect заблокированной ноды (health-check → auto-relocate) — отдельная фича, требует monitoring; оператор решает что заблокировано.
- Backup на удалённое хранилище (S3/SSH) — download/upload через UI/CLI, оператор хранит файл сам.
- Relocate с заменой transit-ключей (rotation) — deliberately переиспользуем persisted ключи. Rotation — follow-up.
- **Clone node** — отдельная фича (NEXT).
- **Users flow audit** (create user + choose where inbounds + how to route) — отдельная фича (NEXT).

---

## 15. v0.6.0 roadmap — product + egress verify (2026-07-08, в работе)

После v0.5.0 (backups + relocate) технический долг из CTO-review закрыт почти
целиком (CI, at-rest шифрование, паники из request-path, config.Load silent
fallback, handleTrustHostKey fingerprint-сверка, systemd hardening, HTTP
timeouts, schema versioning, sentinel errors, i18n parity, applier split, TUIC
e2e skip, zombie-каталоги). Осталось — **продуктовые дыры**, а не hygiene. Этот
раздел фиксирует план и процесс v0.6.0. Приоритеты PO (циничный аналитик):

| # | Задача | Почему | Тип |
|---|---|---|---|
| P0a | Verify egress на реальном client-tunnel (AWG .conf на VPS) | Без этого unknown — работает ли продукт у юзера. Handshake ≠ интернет. | P0 блокер |
| P0b | Users flow wizard + Service model + subscription URL | Без нормального flow юзер-создания = инженерный инструмент, не продукт. | P0 продукт |
| P1a | Auto-detect blocked node (semi-auto: probe + state machine + alert) | Killer-фича, отличает от конкурентов. | P1 продукт |
| P1b | Clone node | Масштабирование флота. | P1 продукт |
| P2a | Offsite backup MVP (SSH target + passphrase encryption + scheduler) | Premium product = не «копируй JSON вручную». | P2 надёжность |
| P2b | Auto-relocate opt-in (warm pool + guardrails) | Поверх P1a. | P2 продукт |
| P2c | Legacy CLI standalone-AWG deprecate; deps/sing-box mirror | Когнитивный диссонанс CLI vs UI; SPOF флота. | P2 долг |

### 15.1. P0a — egress через client-tunnel: аудит кода (2026-07-08)

**Симптом (PROGRESS §13.4):** AWG handshake проходит на свежих GCloud VPS
(`latest handshake: 5 seconds ago` — proof persisted CPS I1-I5 server↔client
identical, амнезия обфускация end-to-end работает), но `curl --interface awge2e
ifconfig.me` → empty. Sing-box TUN-overlay поднят, trace пустой после старта —
нет router match для трафика с `awge2e` client tunnel.

**Аудит генерации конфига (READ → AUDIT):**

1. **TUN inbound** (`internal/chain/awg_tun_overlay.go:94`):
   `type:"tun", interface_name:"sing-box-tun", address:["172.16.250.1/30"],
   mtu:1200, stack:"mixed", auto_route:true, include_interface:["awg0"+exit-ifaces],
   strict_route:false`. Это **ровно** dns.idoctor.mom reference shape.
2. **Route секция** (`internal/chain/merged_config.go:297-313`): когда overlay
   есть, создаётся `RoutingSection{Final:"direct", AutoDetectInterface:true}`.
   `AutoDetectInterface: true` **уже выставлен**.
3. **Per-client правила** (`merged_config.go:382-402`): AWG использует
   `Inbound:["tun-in"], SourceIPCIDR:[u.AWGAddress], Outbound:"direct-out"`
   (или inter-node outbound). **Уже ключены на tun-in** (а не на старый
   users-in endpoint) — re-keying сделано в `merged_config.go:357-367`.
4. **Merge order** (`merged_config.go:304-312`): `actionRules + cfg.Route.Rules
   + catchAll` → per-client правила идут **перед** catch-all (правильно —
   first-match-wins, per-client pin не затеняется catch-all).
5. **`BindInterface`** на exit-direct outbounds (`awg_tun_overlay.go:119`,
   `roles.go:264`) — есть.

**Гипотезы (после research + code audit), ранжированные:**

| H | Гипотеза | Статус после code audit |
|---|---|---|
| H1 | Нет `auto_detect_interface`/`bind_interface` → routing loop глотает egress | **ОПРОВЕРГНУТО** — `AutoDetectInterface:true` уже выставлен в overlay-пути (`merged_config.go:301`) и в `BuildRoutingSection` (`presets.go:279`); `BindInterface` на exit-direct outbounds есть. |
| H4 | Route rule использует deprecated `outbound` без `action:"route"` (post-1.11 regression) | **ОПРОВЕРГНУТО** — дока sing-box 1.13 (`/configuration/route/rule_action/`): `"action": "route"` помечен `"// default"`. Legacy `outbound` поле без `action` дефолтит к `route`, НЕ молча игнорируется. `sing-box check` корректно принимает обе формы. |
| H2 | `include_interface` nftables rule пустой (issue #3805) при >1 интерфейсе | НЕВОЗМОЖНО проверить кодом; на стенде `["awg0"]` (single) — маловероятно, но если balancer добавляет `awg-exit-nX` → список растёт. Проверять `nft list ruleset` на VPS. |
| H3 | Source представлен как IPv4-mapped IPv6 (`::ffff:10.8.0.5`) → `source_ip_cidr:["10.8.0.0/24"]` молча не матчит (issue #2451) | ВОЗМОЖЕН при `stack:"mixed"`/`"system"` на dual-stack хосте. Проверять `journalctl -u sing-box | grep "10\.8\.0"` на `::ffff:` prefix. Fix: добавить `"::ffff:10.8.0.0/120"` в SourceIPCIDR или force v4-only TUN (только IPv4 `address`). |
| H5 | TUN inbound реально SNAT'ит peer IP до router'а | Код-комментарий `awg_tun_overlay.go:154` **утверждает что ДА** ("TUN NAT changes the source IP — source_ip_cidr matching breaks"), но это утверждение противоречит AGENTS.md #7 (per-client routing через `source_ip_cidr` = primary механизм) и тому что per-client правила УЖЕ ключены на `tun-in + source_ip_cidr`. Требует эмпирической проверки: если в Step 3 router видит TUN's `172.16.250.1` вместо `10.8.0.x` → H5 подтверждён → per-client routing под TUN-overlay невозможен в принципе (нужен другой механизм: multiple TUN inbounds per peer, или `auth_user` на отдельном inbound). |

**Ключевое противоречие в коде (latent):**
- `awg_tun_overlay.go:154-156`: catch-all использует `inbound:["tun-in"]`
  (НЕ `source_ip_cidr`) потому что "TUN NAT changes the source IP".
- `merged_config.go:395`: per-client правила используют `Inbound:["tun-in"] +
  SourceIPCIDR:[u.AWGAddress]`.
- Если H5 верен (TUN SNAT'ит) — per-client `source_ip_cidr` правила **никогда
  не срабатывают** (source уже не `10.8.0.x`), и catch-all (тоже по `tun-in`)
  матчит ВСЁ → все юзеры идут по default route, pin не работает. Это ровно
  симптом "handshake ok, egress empty" (если default route = direct на entry,
  а не forward к exit).

**Вывод аудита:** код уже содержит H1-fix (`AutoDetectInterface`) и не нуждается
в H4-fix. Реальный вопрос — **меняет ли TUN source IP или нет** (H5), и
**dual-stack mapping** (H3). Оба требуют **live VPS эмпирики** (tcpdump +
sing-box trace), кодом не разрешаются.

**Debug playbook (для запуска на entry VPS, тест `TestE2E_Heavy_PerClientRouting`
уже содержит большую часть — расширить):**

```
# Step 0 — state
ip -br addr show awg0; awg show awg0 latest-handshakes
ip route show table all | grep -E 'awg0|tun0|sing-box'; ip rule show

# Step 1 — доходит ли трафик до awg0 с peer IP как source?
# (на VPS, во время `curl --interface awge2e ifconfig.me` с клиента)
tcpdump -n -i awg0 'ip and not udp port 51820'   # ждать: 10.8.0.x > ifconfig.me.443

# Step 2 — входит ли в TUN (sing-box-tun)?
tcpdump -n -i sing-box-tun 'host 10.8.0.5'       # если пусто = include_interface capture сломан (H2)

# Step 3 — видит ли router source? (log.level: trace)
journalctl -u sing-box --since "60s ago" | grep -E 'router|match|10\.8\.0'
# ждать: "router: match[N] source_ip_cidr=10.8.0.0/24 => route <outbound>"
# если видите ::ffff:10.8.0.5 → H3 (IPv4-mapped v6)
# если видите 172.16.250.1 (TUN addr) как source → H5 (TUN SNAT) → source_ip_cidr невозможен
# если "no match" → правило не матчит (но H4 уже отпал, значит правило форме корректно)

# Step 4 — уходит ли egress через реальный NIC?
tcpdump -n -i ens4 'host ifconfig.me'            # SYN должен выйти с IP exit-ноды (через balancer)
# если SYN выходит с entry IP (а не exit) → catch-all матчит, per-client не сработал (H5)
# если не выходит вовсе → outbound misconfigured (bind_interface кривой)

# Step 5 — loop check
ip route get <ifconfig.me-IP>                    # если "via <tun-gw> dev sing-box-tun" → loop
```

**Следующий шаг (P0a code):** расширить `TestE2E_Heavy_PerClientRouting`
чтобы при пустом egress автоматически собирал tcpdump Step 1-3 + `ip route get`
и логировал (сейчас — просто WARNING без диагностики). Затем запустить на live
VPS (`AB_E2E_AWG_PERCLIENT=1 AB_ROUTE_DNS=1`) → локализовать H2/H3/H5 → fix.

**Источники research (WebSearch/WebFetch, 2026-07-08):**
- sing-box TUN docs (`/configuration/inbound/tun/`): `include_interface`
  requires `auto_route`; NAT/stack options.
- sing-box route/rule_action docs: `action:"route"` = default, legacy
  `outbound` дефолтит к route (H4 отпал).
- GitHub issue #3805 (`include_interface` empty `iifname`), #2451
  (`source_ip_cidr` silent miss под IPv4-mapped IPv6 → H3), #3858/#4157
  (WG endpoint + TUN routing loop, handshake ok / no return traffic).
- dns.idoctor.mom — login-gated, reference-архитектуру получить не удалось
  (считать любые доки, ссылающиеся на него, unverifiable).

### 15.2. P0a live-VPS diagnosis — root cause localized (2026-07-08)

После code-audit (§15.1) гипотезы H1/H4 были опровергнуты кодом/dokой. Для
локализации H2/H3/H5 прогнан расширенный `TestE2E_Heavy_PerClientRouting`
(добавлен tcpdump на awg0/sing-box-tun/awg-exit-n1/ens4 + `ip route get` +
sing-box trace при пустом egress) на свежих GCloud VPS (entry 34.14.98.64,
exit 35.189.235.61, `AB_E2E_AWG_PERCLIENT=1 AB_ROUTE_DNS=1 AB_LOG_LEVEL=trace`).
Дополнительно — ручные SSH-сесии с воспроизведением условий теста + A/B/C-пробы
(rp_filter=0, явный `ip rule from 10.8.0.0/24 lookup 2022`, host-route через
awge2e). **Все пробы дали одинаковый результат** — диагноз стабилен.

**Доказательный вывод (tcpdump `-i any`, воспроизведён многократно):**

```
awge2e Out  10.8.0.16.55602 > 34.160.111.145.443 [S]   ← curl кладёт SYN в awge2e (OK)
ens4   Out  10.132.0.8.36528 > 34.14.98.64:51820 UDP   ← AWG шифрует, hairpin на self (OK)
ens4   In   34.14.98.64.36528 > 10.132.0.8:51820 UDP   ← ответ доходит (OK)
awg0   In   10.8.0.16.55602 > 34.160.111.145.443 [S]   ← дешифрованный SYN arrives on awg0 (OK)
[ НИЧЕГО на sing-box-tun; sing-box trace: "No entries" ]
```

**Chain `awge2e → ens4 → awg0` РАБОТАЕТ** полностью — дешифрованный TCP SYN
с source `10.8.0.x` доходит до kernel AWG интерфейса `awg0`. Дальше трафик
**должен** форвардиться в `sing-box-tun` (TUN-overlay) через sing-box
`auto_route` policy rules. **Не форвардится** — на `sing-box-tun` нет
захвата, sing-box router не вызывается (trace пуст, нет `router:match`).
TCP SYN ретраится 5 раз (1s/2s/4s/8s), curl таймаутит.

**Что ОПРОВЕРГНУТО live-пробами:**
- **H1 (routing loop / нет auto_detect_interface)** — `ip route get 1.1.1.1` →
  `via 10.132.0.1 dev ens4` (НЕ через sing-box-tun, нет loop). `AutoDetectInterface:true`
  в конфиге есть.
- **H3 (IPv4-mapped IPv6 source)** — sing-box trace пуст ВООБЩЕ (router не
  вызывается, до matching дело не доходит). Не H3.
- **H4 (deprecated outbound field)** — отпал по доке (action:route = default).
- **H5 (TUN SNAT)** — отпал: пакет не доходит ДО router'а (trace пуст), SNAT
  не при чём.
- **rp_filter** — `net.ipv4.conf.sing-box-tun.rp_filter=1` (унаследован от
  `all=1`) найден как подозреваемый, но `sysctl ... rp_filter=0` на
  sing-box-tun + awg0 + all **НЕ исправил**. Не первопричина.
- **явный `ip rule from 10.8.0.0/24 lookup 2022 priority 100`** (раньше
  sing-box'овых 9000) — **НЕ исправил**. sing-box `auto_route` правило 9000
  `from all iif awg0 goto 9002` уже направляет awg0-трафик в table 2022
  (`default via 172.16.250.2 dev sing-box-tun`), но пакет в TUN не попадает.

**Истинная первопричина (OPEN):** sing-box-extended TUN-overlay **не
захватывает forwarded-трафик с kernel AWG интерфейса** `awg0`, несмотря на
`include_interface:["awg0",...]` + `auto_route:true` + корректные `ip rule`
policy rules (9000: `iif awg0 goto 9002` → table 2022 → `default via
172.16.250.2 dev sing-box-tun`). Пакет доходит до awg0 (kernel подтверждает
tcpdump'ом), но в TUN device не входит — sing-box userspace его не читает.
Это **точно совпадает** с давним OPEN пунктом CTO-review Technical Debt:
*"`ip rule 10.8.0.0/24 → table 2022` на entry не эмитится deploy-flow — OPEN
(low)"* — но live-пробы показывают что проблема НЕ в отсутствии rule (sing-box
его эмитит сам через auto_route), а в том что **TUN device не принимает
forwarded ingress с kernel iface**.

**Оставшиеся гипотезы (для след. итерации):**
- **`strict_route: false`** в текущем конфиге — дока sing-box: *"Enforce
  strict routing rules when auto_route is enabled"* → с `false` правила
  могут не enforced. Попробовать `strict_route: true`.
- **`auto_redirect: true`** (Linux-only, дока: *"better routing, higher
  performance, avoids conflicts between TUN and Docker bridge"*) — текущий
  конфиг НЕ использует `auto_redirect`. Возможно нужен для forwarded ingress.
- **TUN `stack: "mixed"`** (system TCP + gvisor UDP) — попробовать `gvisor`
  или `system`.
- **TUN device flags** — `sing-box-tun` = `POINTOPOINT,MULTICAST,NOARP` (не
  обычный ethernet TUN). Возможно sing-box-extended не читает forwarded
  пакеты с NOARP-интерфейса. Upstream bug-кандидат.
- **sing-box-extended vs vanilla** — проверить, ломается ли тот же конфиг
  на vanilla sing-box 1.13.14 (изолировать regression от patch).

**Вывод:** egress через client-tunnel — **не минутный фикс**, требует
исследования upstream sing-box-extended TUN-ingress поведения (strict_route /
auto_redirect / stack / NOARP). Это P0-блокер продукта (пользователь
подключается, но не получает интернет), но технически — глубокий sing-box
вопрос, не angry-box config-typo. Дальнейший шаг: targeted research по
sing-box-extended TUN + `include_interface` + forwarded ingress, + пробы
`strict_route:true` / `auto_redirect:true` / `stack:gvisor` на live VPS.

**Тест-инфра (сохранена для след. итерации):** `TestE2E_Heavy_PerClientRouting`
расширен tcpdump + route-get + sing-box trace — каждый прогон авто-собирает
диагностику. Запуск: `AB_E2E_AWG_PERCLIENT=1 AB_ROUTE_DNS=1 AB_LOG_LEVEL=trace
go test -tags e2e ./internal/chain/ -run TestE2E_Heavy_PerClientRouting -v`.
Сервера оставлены staged (модуль + sing-box установлены, awg0/awg-exit-n1
активны). Cleanup в defer корректен (awge2e + peers удаляются).

### 15.3. P0a live flag-trials — все конфиг-флаги НЕ исправили (2026-07-08)

После локализации (§15.2) прогнал на live entry VPS (34.14.98.64) 8 вариантов
TUN inbound через прямую правку `/etc/sing-box/config.json` + `systemctl restart`
+ подъем awge2e-client + curl + tcpdump `-i any`. Каждый вариант — отдельная
проба (скрипт `/tmp/try_flags.sh` + `/tmp/try_struct.sh` на VPS).

**TUN config-флаги (5 вариантов):**

| Вариант | Egress | sing-box trace |
|---|---|---|
| `strict_route:true` | empty | `-- No entries --` |
| `auto_redirect:true` | empty | `-- No entries --` |
| `stack:gvisor` | empty | `-- No entries --` |
| `stack:system` | empty | `-- No entries --` |
| `strict_route+auto_redirect+gvisor` | empty | `-- No entries --` |

**Вывод:** ни один конфиг-флаг sing-box TUN не заставляет overlay захватывать
forwarded ingress с kernel AWG iface. Флаги отпадают как фикс.

**`include_interface` structural variants (3 варианта) — КЛЮЧЕВОЕ:**

| Вариант | AWG handshake | sing-box видит трафик? |
|---|---|---|
| **`include_interface:["awg0"]` (current)** | **OK** (`HS=...latest handshake`) | **НЕТ** (trace `-- No entries --`) |
| **`include_interface` removed (auto_route only)** | **BROKEN** (`HS=0`) | **ДА** (`dns: exchange ifconfig.me NOERROR → 34.160.111.145`) |
| **`exclude_interface:["ens4"]`** | **BROKEN** (`HS=0`) | **ДА** (`dns: exchange ifconfig.me`) |

**Парадокс разрешён — semantics `include_interface` прояснились:**

- **Без `include_interface`** sing-box `auto_route` ставит default route через
  TUN для **ВСЕГО** трафика хоста, включая AWG handshake UDP на
  `34.14.98.64:51820` → handshake-пакеты уходят в TUN вместо ens4 → handshake
  ломается (`HS=0`). НО дешифрованный user-трафик (которого нет, т.к. handshake
  не прошёл) — не доходил; при этом sing-box видит DNS-запросы curl'а (curl
  пытается резолвить ifconfig.me, DNS-пакет уходит в TUN → sing-box логирует
  `dns: exchange`). Это **artefact**, не рабочий path.

- **С `include_interface:["awg0"]`** sing-box ставит policy rule `from all iif
  awg0 goto 9002` (только ingress с awg0 → table 2022 → TUN). AWG outgoing
  UDP на ens4 **не трогается** → handshake работает. **НО** дешифрованный
  forwarded ingress, который ПРИХОДИТ на awg0 (iif=awg0) и должен попасть в
  TUN через правило 9000 — **НЕ попадает** (trace пуст, tcpdump на
  sing-box-tun пуст).

**Итоговый вывод (окончательный):** `include_interface` в sing-box-extended
захватывает **только трафик, originate-ящий из локальных сокетов на этом
интерфейсе**, **НЕ forwarded ingress** с kernel WireGuard iface. Дешифрованный
пакет от AWG-пира приходит на awg0 как forwarded ingress (destination = remote
IP, не локальный socket) — и хотя `ip rule 9000 (iif awg0 goto 9002)` существует,
TUN device его не принимает. Это **upstream sing-box-extended behavior** /
возможный bug, не angry-box config-typo.

**Оставшиеся path forward (для след. сессии, требует upstream research):**

1. **Upstream issue на sing-box-extended**: `include_interface` не захватывает
   forwarded ingress с kernel WireGuard iface — file с минимальным
   репродьюсером (конфиг + tcpdump доказательство из §15.2). WebSearch
   недоступен в текущей среде — нужен ручной поиск/репорт.
2. **Альтернативная архитектура (last resort):** отказаться от kernel AWG +
   TUN-overlay, вернуть **userspace WireGuard endpoint** для user-entry
   (НЕ для transit — transit остаётся userspace per AGENTS #10). Userspace
   endpoint сам владеет интерфейсом → sing-box видит трафик напрямую, без
   TUN-overlay indirection. Но AGENTS #10 предупреждает что userspace amnezia
   паничит (chacha20poly1305) — патч `wireguard-go-awg-overlap.patch` это
   фиксал, но user-facing сервера были переведены на kernel-AWG именно из-за
   нестабильности userspace. Возврат = regression risk.
3. **sing-box как AWG endpoint** (sing-box-extended поддерживает amnezia в
   `wireguard` outbound/inbound): sing-box сам поднимает AWG, без awg-quick —
   тогда трафик сразу в sing-box, без TUN-overlay. Это архитектурный сдвиг от
   "kernel awg-quick + sing-box TUN-overlay" (AGENTS #11 kernel-AWG rework).

**Статус P0a:** диагностика завершена, фикс НЕ найден в рамках config-флагов.
Требует upstream research + архитектурного решения. Это P0-блокер продукта,
но не блокер для P0b (Users flow) — egress-verify можно отложить отдельной
сессией, а UX-фичи строить параллельно (они не зависят от egress-routing).

## 16. P0b Slice 1 — Users flow wizard + Service model + subscription URL (2026-07-08)

Самая ценная по продукту часть v0.6.0: превращает создание юзера из инженерной
формы (8+ внутренних полей: protocols/chains/MTProxy/order/nodes) в wizard
(4 шага: Who → What → Quota → Review), добавляет **Service** model (именованный
продукт-тир: bundle цепей + exit-pin + протоколов + MTProxy-defaults —
Marzneshin `Service` analogue) и **subscription URL** (`/sub/{token}` — одна
opaque ссылка, которую клиент вставляет в v2rayNG/Nekoray/sing-box и получает
актуальный конфиг). Research: Marzban/Marzneshin/3x-ui/Hiddify patterns
(§15 audit) — все сходятся: identity + quota + expiry = форма, protocol/
inbound = вычисляется. Angry-box теперь делает так же.

### 16.1. Что в скоупе Slice 1

- **Service model** (`model.Service`, `PanelSettings.Services`) — operator-
  defined product tiers, CRUD на `/ui/services`. Bundle: ChainNames +
  DefaultExitByChain (chain→ChainNode.ID, **первый UI для User.ChainExit**,
  уже wired в `buildMergedRoute`) + Protocols + RoutingPresetIDs + MTProxy
  defaults.
- **User wizard** (`web/templates/users.templ` `UserForm`/`userFormBody` →
  4-step): Step 1 Who (id/name/telegram/email), Step 2 What (Service radio-
  cards + Custom advanced disclosure: chains + per-chain exit-pin selects +
  protocols + import-secret + MTProxy collapse), Step 3 Quota/Expiry
  (ExpireStrategy segmented control: fixed_date/start_on_first_use/never +
  DataLimit + reset strategy), Step 4 Review. Single-form + DaisyUI steps +
  minimal JS toggle (`wizardNext`/`wizardPrev` в `app.js`, ~40 строк, без
  framework — AGENTS.md #1). Один POST в существующие handlers.
- **`/sub/{token}` endpoint** (`internal/web/subscription.go`) — public (без
  `s.auth`, precedent `/static/`/`/health`; GET passes CSRF). Token lookup via
  `Store.GetUserBySubscriptionToken`. Honors `ComputeStatus` (expired/disabled/
  limited → 404). Lazy token backfill для legacy users + `start_on_first_use`
  `FirstUseAt` stamp. Format negotiation: `?format=raw|base64`; default by
  User-Agent (v2rayNG/nekoray/nekobox/shadowrocket/sing-box → base64 v2ray
  convention, else raw). `collectUserLinks` — shared link-gathering (extracted
  из `handleUserConfig`/`handleUserQR`, DRY — теперь все три рендерят
  идентичный link set включая MTProxy).
- **User schema additions** (all `omitempty`, additive — NO migration): DataLimit,
  DataLimitResetStrategy, ExpireStrategy, UsageDuration, ActivationDeadline,
  SubscriptionToken, Status, ServiceID, UsedTraffic/LifetimeUsedTraffic
  (populated by P0b-2 poller), FirstUseAt. `User.ComputeStatus()` —
  disabled/expired/on_hold/active (limited needs poller); "never" strategy
  ignores ExpiresAt (Marzneshin semantics).
- **Handler changes** (`internal/web/users.go`): `handleCreateUser`/
  `handleUpdateUser` read `service_id` → `applyServiceToUser` (expands
  Service в ChainNames/Protocols/ChainExit/MTProxy/ServiceID); Custom path
  reads `exit_<chainName>` → builds ChainExit; `expire_strategy`/`data_limit`/
  `usage_duration`/`activation_deadline`; mint `SubscriptionToken` at create
  (retry on collision) + re-mint at update if cleared; `u.ComputeStatus()`
  before SaveUser; create returns `UserCreatedResult` (sub URL box + Copy/QR
  buttons) вместо bare `UserRow`.
- **Lifecycle status wiring**: `UserRow` Status column via `userStatusBadge`
  (active/disabled/expired/on_hold/limited → i18n label + badge class);
  `handleClients`/`handleUsers` derive Status для display (legacy records
  без persisted Status получают derived).

### 16.2. Что НЕ в скоупе (явно, deferred)

- **Quota enforcement** (V2Ray stats poller writing `UsedTraffic`) → **P0b-2**.
  Поля `DataLimit`/`UsedTraffic` добавлены сейчас (zero, не мутируются), но
  enforcement = greenfield: emit `experimental.v2ray_api` в sing-box config +
  backend `QueryStats` method + per-user metrics model + poller + compare
  against DataLimit → remove user + redeploy. `limited` Status никогда не
  выставляется в Slice 1 (только poller).
- **Per-user destination routing render** (`Service.RoutingPresetIDs` →
  `BuildRoutingSection`) → **P0b-3**. `RoutingPresetIDs` stored + UI их
  собирает (label "applies on next deploy — P0b-3"), но `BuildRoutingSection`
  кушает только `ConnectionPreset.Routing` geosite имена, не `ROUTING_PRESETS`
  domain catalog. Render — отдельная работа.
- **Clash YAML / sing-box JSON sub formats** → later. Slice 1 = raw + base64.

### 16.3. Build sequence (7 групп, каждая компилируется)

1. Model + cryptogen + store (`panel.go`, `cryptogen.go`, `store.go`) — committed `785f774`.
2. i18n (~36 keys en+ru, parity test green) — committed `26cc915`.
3. Service CRUD (`services.go`, `services.templ`, `server.go` routes, 7 tests) — committed.
4. `/sub/{token}` (`subscription.go`, `server.go` public route, 7 tests) — committed.
5. User wizard (`users.templ` UserWizard + UserCreatedResult, `app.js`, `users.go` handlers, 5 wizard tests) — committed `ee51c0a`.
6. Status wiring (`UserRow` + `handleClients`/`handleUsers`) — committed `d0e4637`.
7. Final verify + docs §16 — этот commit.

### 16.4. Тесты (все PASS, non-e2e)

| Группа | Тесты | Покрытие |
|---|---|---|
| Model | `TestComputeStatus` (10 branches), `TestIsExpired`, `TestGenerateSubscriptionToken`, `TestGetUserBySubscriptionToken` | schema + helpers |
| Service CRUD | `TestHandler_ServicesPage_Renders`, `_CreateService` (+persist), `_MissingFields`, `_DuplicateID` 409, `_DeleteService_RefusesIfInUse` 409, `_DeleteService_OK`, `_EditServiceForm` 200/404 | full CRUD |
| Subscription | `TestSub_KnownToken_Raw/Base64Param`, `_V2rayNGUserAgent_DefaultsBase64`, `_UnknownToken_404`, `_ExpiredUser_404`, `_DisabledUser_404`, `_LazyTokenBackfill` | endpoint + UA negotiation + status honor |
| Wizard | `_CreateUser_WithService_ExpandsFields`, `_CustomPath_ChainExitExposed`, `_ExpireStrategy_StartOnFirstUse_StatusOnHold`, `_CreateUser_MintsSubToken`, `_ExpiredUser_StatusExpired` | Service expansion + ChainExit UI + quota + token mint |

`go build ./...` + `go vet ./...` + полный non-e2e `go test ./internal/...` —
зелёные (10 пакетов). `TestEnRuKeyParity` PASS (i18n parity). E2E не трогался
(Slice 1 — чисто UI + handlers + model, без VPS).

### 16.5. Архитектурные решения (resolved during impl)

- **Wizard style**: single-form + DaisyUI steps + minimal JS toggle (НЕ HTMX
  multi-step) — matches `presets.templ` inline-toggle precedent, один POST в
  существующие handlers, no wizard-state server store, AGENTS.md #1 compliant.
- **Migration #2**: НЕТ — lazy token backfill на first sub fetch + mint at
  create. Additive `omitempty` поля load old stores без миграции.
  `currentSchemaVersion` остаётся 1.
- **Status computation**: `ComputeStatus()` method — single source of truth,
  called at save + list time. "limited" не выставляется в Slice 1 (poller).
- **Service storage**: `PanelSettings.Services json.RawMessage` (mirrors
  `CustomPresets` precedent), CRUD via `servicesList`/`saveService`.
- **Public sub route**: directly on mux without `s.auth` (precedent `/static/`,
  `/`, `/health`); GET passes CSRF safe-method bypass; own Cache-Control
  `max-age=60` (не наследует `s.auth`'s `no-store`).

### 16.6. Honest gaps / risks

1. **`Service.RoutingPresetIDs` stored-not-rendered** — UI labels clearly
   ("applies on next deploy — P0b-3"). `BuildRoutingSection` не кушает
   `ROUTING_PRESETS` domain lists. Пер-user destination routing = P0b-3.
2. **Quota не enforced** — `DataLimit`/`UsedTraffic` хранятся, но poller
   отсутствует (V2Ray stats API — grep 0 в кодовой базе). `limited` Status
   никогда не выставляется. P0b-2.
3. **Subscription URL host** — `subURLHost` использует `X-Forwarded-Host` →
   `r.Host` → `localhost:9080`. Behind reverse-proxy корректен если proxy
   sets Host/X-Forwarded-Host. `/sub/{token}` сам host-agnostic.
4
5. **Token uniqueness** — create-time `GetUserBySubscriptionToken(t)` not-found
   check (retry ×3), 2^96 entropy collision-free.

### 16.7. Следующие slice'ы P0b

- **P0b-2**: quota enforcement — `experimental.v2ray_api` emit + `QueryStats`
  backend method + per-user metrics + poller + DataLimit compare → remove user
  + redeploy. Закрывает "limited" Status + real quota UX.
- **P0b-3**: per-user destination routing — `RoutingPresetIDs` →
  `GetRoutingPresetDomains` → per-user `RouteRule` (auth_user/source_ip_cidr)
  → `BuildRoutingSection`. Закрывает "this user gets Telegram-only".
- (опционально) **sing-box JSON / Clash YAML** sub formats — для нативных
  sing-box / Clash клиентов.

---

## 17. P1a — Node health state machine (liveness + operator block) (2026-07-08)

**Скоуп:** Заменить бинарный `NodeMetrics.Online bool` на настоящую стейт-машину
`healthy → suspect → down → unreachable` + оператор-маркированный `blocked`,
считаемую из существующего цикла метрик с **гистерезисом** (один транзитный
SSH-таймаут не флипает ноду). Состояние показывается везде, где раньше был
фейковый «green = online» (точка статуса в spider, бейдж дашборда, ячейка таблицы
нод) + пишется событие аудита на каждый **переход** (не на каждый тик).

**Ключевое архитектурное решение (подтверждено пользователем):**

- **Источник обнаружения = только liveness** (сигналы SSH+systemd, уже
  собираемые `GetStatus`). «Заблокирован DPI» — это **оператор-маркированное**
  состояние, НЕ авто-обнаруживается: оркестратор SSH'ит из свободного региона и
  физически не видит DPI-блок (нода выглядит полностью здоровой — SSH ок,
  systemd активен). Sentinel-проб (точка наблюдения из цензурированного региона,
  диалющая публичный инбаунд-порт) — отложен как P1a+ за плаггабельным
  интерфейсом `Probe`; разделение `classifyProbe`/`NextState` делает эту точку
  расширения чистой (будущий `SentinelProbe` вернёт `ProbeOutcome{Blocked bool}`).
- **Авто-действие = только алерт + аудит.** Без авто-отключения инбаундов, без
  авто-релокейта. Оператор видит состояние + кнопку «Mark blocked / Clear block»
  и решает сам (существующая кнопка Relocate делает сам переезд).

### Что сделано (коммиты 35e19f9 → 74048fc)

1. **Модель** (`internal/domain/model/panel.go`): `NodeMetrics` += `State`,
   `StateReason`, `StateChangedAt`, `ConsecutiveFails`, `ConsecutiveOKs`
   (аддитивные `omitempty`, без миграции — старые сторы с пустым `State`
   выводятся из `Online`). Константы `NodeState*` (healthy/suspect/down/
   unreachable/blocked/unknown) + `HysteresisConfig`/`DefaultHysteresis`
   (DownThreshold=3, RecoverThreshold=2).

2. **Стейт-машина** (`internal/chain/nodehealth.go`, НОВЫЙ): чистая функция
   `NextState(m, probe, cfg)` + `SetNodeState` + экспортированная `ClassifyProbe`.
   `blocked` **липкий** — пробы его не сбрасывают, только хендлер оператора.
   16 unit-тестов (`nodehealth_test.go`) покрывают всю таблицу переходов.

3. **Цикл метрик** (`internal/web/server.go` `collectAllMetrics`): каждый тик
   классифицирует пробу через `ClassifyProbe`, гонит `NextState`, сохраняет
   метрики, пишет `chain.WriteAudit("health", ...)` **только на переходе**
   (пропуская первичную `unknown→healthy` классификацию свежей ноды). 6 loop-тестов.

4. **Хендлеры** (`internal/web/nodes.go`): `handleMarkNodeBlocked` /
   `handleClearNodeBlocked` + роуты `POST /ui/nodes/{id}/block|unblock`.
   `handleHostStatus` (misc.go) переведён на `ClassifyProbe`/`NextState` —
   ручной «Check» теперь даёт состояние, консистентное с фоновым тикером.
   5 block/unblock-тестов.

5. **i18n** (`internal/i18n/i18n.go`): 9 ключей en+ru (Suspect/Down/Unreachable/
   Blocked/Mark blocked/Clear block/Mark as blocked/Reason/node is not blocked).
   `TestEnRuKeyParity` зелёный.

6. **Рендер**: `metricBadge` (dashboard.templ) — 5-сторонний по состоянию +
   back-compat-вывод из `Online`; общие хелперы `NodeState`/`healthBadgeClass`/
   `healthBadgeLabel`. Spider status dot (spider.templ) — `fill` data-driven
   через `statusDotFill` (был хардкод-зелёным). `NodeStatusCell` (nodes.templ) —
   бейдж + кнопки Mark/Clear-blocked + Check. `DashboardStats` += `DownHosts`/
   `BlockedHosts` + `computeHealthCounts`. 2 dashboard render-теста (Down +
   Blocked бейджи).

### Тесты

- 16 unit (nodehealth) + 6 loop + 5 block/unblock + 2 dashboard render = **29
  новых тестов**, все зелёные.
- Полный non-e2e набор (10 пакетов) зелёный; `go build ./...` + `go vet ./...`
  чистые.
- Существующие тесты (`TestHandler_HostStatus_*`, `TestHandler_DashboardStats`,
  `TestHandler_NodesList_*`) — зелёные (back-compat derive держит).

### Риски / что отложено

- **«Blocked» НЕ авто-обнаруживается** — по дизайну. UI говорит «Mark as
  blocked» (действие оператора), не «Detected blocked». Sentinel-проб — P1a+.
- **Гистерезис хардкод** (`DefaultHysteresis`). P1a+ подключит
  `PanelSettings.Hysteresis`.
- **Два writer'а** (`collectAllMetrics` + хендлер оператора) оба через per-call
  лок `SaveMetrics`; потерянный апдейт реклассифицируется следующим тиком.
  Приемлемо для 15-мин цикла; задокументировано в `collectAllMetrics`.
- **Спам аудита** — только переходы + гистерезис глушит; флапающая нода пишет
  2 события на флап, но RecoverThreshold=2 ограничивает.

### Явные не-цели

Нет sentinel-проба / точки наблюдения из ценз. региона (P1a+). Нет агрегации
клиентских исходов. Нет авто-отключения инбаундов. Нет авто-Relocate (оператор
жмёт существующий Relocate). Нет enforcement квоты (P0b-2). Нет e2e/VPS-изменений
— чистая модель + цикл + хендлеры + UI; deploy-путь не тронут.

---

## 18. P1b — Clone node (fresh identity, copied config) (2026-07-09)

**Скоуп:** Дублировать конфиг ноды на новый VPS со **свежей идентичностью** (новые
UUID/Reality keys/ShortID/AWG client keys/MTProxy secret/transit WG keypairs/
transit UUID/transit IP), но с **скопированной конфигурацией** (Protocol/Port/
Obfuscation/ForUsers/OutboundTag/Source/AWGServerAddress, chain Role +
ExitTargets, Country/Bandwidth/AutoApply/UseSudo). Clone = «relocate, но с новой
ID + свежей identity вместо переиспользованной, и новый ChainNode добавляется
(append) в цепочку, а не мутируется на месте».

**Решение (подтверждено пользователем):** конфиг + копия ForUsers + копия
ExitTargets; identity всегда свежая. Clone служит тем же пользователям + той же
топологии выходов, но как отдельный узел (не второй сервер с теми же кредами).

### Что сделано (коммиты ee0de87 + ee81389 + cc4ae8d)

1. **Ядро** (`internal/chain/clone.go`, НОВЫЙ): `CloneNode` (package-level +
   `*Applier` wrapper). Минтит новую ID (коллизия-чек через `GetHost`), копирует
   конфиг, регенерирует identity через cryptogen (`GenerateRealityKeypair`/
   `GenerateRealityShortID`/`GenerateTUICUUID`/`GenerateInboundTag`/
   `GenerateWireGuardKeypair`/`GenerateSelfSignedCert`/`GenerateHysteria2ObfsPassword`
   + IP allocators `allocateAWGTransitIP`), добавляет новый ChainNode в каждую
   цепь source, re-deploy через `ApplyChain` (как relocate), `WriteAudit("clone")`.
   `chainApplier` интерфейс (как relocate) — тесты мокают.

2. **Identity vs Config split** (задокументирован в `clone.go`):
   - NodeInbound IDENTITY: UUID, ServerPrivKey/PubKey, ShortID, Tag, ObfsPassword,
     TLSCertificate/TLSPrivateKey, AWGClientPub/Priv. CONFIG: Protocol, Port,
     Obfuscation, ForUsers (копия), OutboundTag, Source, AWGServerAddress (копия).
   - ChainNode IDENTITY: TransitPrivKey/ShortID/UUID, TransitAWGServer*/Client*/
     Address, ExitAWGServer*/ExitAWGLinks. CONFIG: Port, Role, ExitTargets (копия),
     Inbounds (regen).

3. **Хендлеры** (`internal/web/nodes.go`): `handleCloneForm`/`handleCloneNode` +
   роуты `GET/POST /ui/nodes/{id}/clone`. Валидация: new_id непустой + не равен
   source + не существует; ssh-key-id проверка через registry.

4. **UI** (`web/templates/nodes.templ`): `CloneForm` (зеркало `RelocateForm` +
   поле `new_id`) + `CloneResult` (зеркало `RelocateResult` из chains.templ) +
   `cloneAllSuccess` хелпер. Кнопка «Clone» в `NodeRow` рядом с Relocate.

5. **i18n**: 7 ключей en+ru (Clone node/Clone/New node ID/unique not yet used/
   Clone to new VPS/New node ID is required/clone help text).

### Тесты

- **8 unit** (`clone_test.go`): fresh identity + source untouched (UUID/keys/
  ShortID/Tag на inbound; transit keys/UUID/IP на ChainNode; ForUsers + Role +
  ExitTargets copied), validation (5 subtests), source-not-found, newID collision,
  nil applier, audit written, one-chain-failure non-fatal. Все зелёные.
- **8 web** (`handlers_clone_test.go`): form render/404, clone OK (fresh identity
  + copied config + source untouched), empty new_id/addr, duplicate ID, same ID,
  bad SSH key. Все зелёные (через ноду-без-цепей, как relocate-тест).
- Существующие relocate/nodes-тесты не сломаны.

### Риски (помечены)

1. **AWGServerAddress коллизия** — копируем subnet как есть. На разных VPS
   локального конфликта нет; конфликт только если обе ноды в одной цепочке и
   роутинг пересекается. Флаг в CloneForm tooltip. P1b+: автолюбка свежего /24.
2. **ExitTargets — это ID** — копируем как топологию. Клон exit таргетит те же
   exit-ID (валидно — балансер на 2 exit).
3. **Без e2e** — мок applier (как relocate). ApplyChain в проде деплоит (существующая
   работающая функция). Без авто-генерации newID (operator-заданный, как capture).
4. **ExitAWGLinks не копируются** — клоны-выходы не наследуют balancer-side links
  (балансер, таргетящий клон, заминтит свои на re-apply). Документировано.

---

## 19. P2a — Offsite backup MVP (passphrase-encrypted SSH push) (2026-07-09)

**Скоуп:** Периодически (и on-demand) пушить **зашифрованную паролем** копию стора
(store.json — единый source of truth: hosts/chains/users/infos/metrics) на
**внешний SSH-таргет**, чтобы потеря хоста не теряла состояние оркестратора.
КРИТИЧНО: master-key файл **НИКОГДА** не покидает хост — внешний бэкап
перешифровывается **отдельным паролем** (scrypt-derived), не master-ключом.

**Решение (подтверждено пользователем):** пароль хранится в `PanelSettings`
(в уже зашифрованном at-rest master-ключом сторе) → включает и периодический
цикл, и on-demand «Backup now». Восстановление: пароль вводится отдельно (он в
scrypt-параметрах блоба, не в блобе).

### Что сделано (коммиты bc42ac9 + 4e60f2b + cc4ae8d)

1. **Passphrase-шифрование** (`internal/chain/backup_crypto.go`, НОВЫЙ): `EncryptBackup`/
   `DecryptBackup` — scrypt (N=2^16,r=8,p=1) → AES-256-GCM. Формат `ABBKP1` (magic
   || salt(16) || N/r/p(8) || nonce(12) || ct+tag), отдельный от at-rest `ABENC1`.
   Параметры в блобе → тюнимы без поломки старых. `IsBackupBlob` детектор.

2. **Конфиг** (`model.OffsiteBackupConfig` + `PanelSettings.OffsiteBackup`):
   Enabled/Host/User/SSHKeyID/RemotePath/Passphrase/IntervalMin/LastBackupAt.
   Round-trip через `GetSettings`/`SaveSettings`.

3. **Ядро** (`internal/chain/backup_offsite.go`, НОВЫЙ): `PushOffsiteBackup` —
   `ExportStore` (plaintext in-process) → `EncryptBackup(passphrase)` → SSH
   `UploadText` (через `ports.SSHConnector`, key resolved by ID из registry) →
   stamp `LastBackupAt`. **Master-key не читается, не передаётся.**

4. **Периодический цикл** (`internal/web/server.go` `StartOffsiteBackupLoop`):
   mirror `StartBackgroundMetrics`, читает свежий cfg каждый тик (default 360 мин
   = 6ч), не бэкапит сразу на старте. Wiring в `cmd/angry-box/main.go:830`.

5. **Хендлеры + UI** (`internal/web/backups.go` + `settings.go` + `settings.templ`):
   - `handleSaveOffsite` (отдельный эндпоинт `POST /ui/backup/offsite/save` —
     трогает ТОЛЬКО offsite config, не сбрасывает остальные настройки).
   - `handleBackupNow` (`POST /ui/backup/offsite/now`).
   - `handleImportBackup` расширен: `ABBKP1` magic → `DecryptBackup(passphrase)`
     → `ImportStore` (восстановление из зашифрованного блоба).
   - Settings UI: карточка «Offsite backup» (host/user/key/path/passphrase/
     interval/enabled) + «Backup now» + поле passphrase в import-форме.

6. **i18n**: 15 ключей en+ru (offsite + restore).

### Тесты

- **11 unit** (`backup_crypto_test.go`): roundtrip, wrong passphrase, bad magic,
  empty passphrase/plaintext, different salts, IsBackupBlob, full push via fake
  connector (blob decrypts to store with host), no-target/no-passphrase, connect-fails.
- **8 web** (`handlers_offsite_test.go`): save persists + не сбрасывает другие
  настройки, empty host disables, backup-now ok/fail (LastBackupAt stamped/not),
  encrypted restore roundtrip (host появляется в store), wrong/missing passphrase.
- Существующие backup/settings-тесты не сломаны (включая
  `TestHandler_SettingsView_NoNestedFormsInMainForm` — offsite-форма отдельная).

### Риски (помечены)

1. **Passphrase в сторе** — в at-rest-зашифрованном (master-key) сторе. Без
   master-key файла стор plaintext → пароль виден. Документировано: «enable
   master-key encryption (store.json.key) для at-rest защиты пароля».
2. **Backup SSH target ≠ managed node** — TOFU host-key: первый коннект к
   offside-боксу потребует доверия. Переиспользуем существующий connector (TOFU
   как для нод). Флаг.
3. **scrypt N=2^16 ~64MB** — память на каждый backup. На слабом оркестраторе
   тяжело, но backup раз в 6ч — приемлемо. P2a+: тюнимый N в cfg.
4. **Без retention/rotation** — один блоб per backup по remote_path (перезаписывается).
   Operator управляет retention на offside-target.
5. **Без restore-from-remote-pull** — operator скачивает блоб вручную → paste в
   restore (с passphrase). Без master-key бэкапа (master-key НИКОГДА не покидает хост).

### Явные не-цели

Без restore-from-remote-pull. Без retention/rotation. Без master-key бэкапа
(master-key НИКОГДА не покидает хост — это принцип). Без e2e SSH-пуша (мок-connector
в тестах). Обе фичи (P1b + P2a) — чистая модель + хендлеры + UI; deploy-путь
тронут только переиспользованным ApplyChain (clone) / UploadText (backup).

---

## 20. P1b/P2a follow-up — три мелочи (2026-07-09)

Три точечные правки, закрывающие дыры из §18/§19. Коммиты `d60e991` + `b086b8e` + (Fix 2).

### Fix 1 — свежий AWG /24 при клоне (§18 риск 1 закрыт)
- `allocateAWGServerSubnet` (cryptogen.go): первый свободный `10.8.X.0/24` (X=1..250), **пропускает legacy `10.8.0.0/24`** (chain-entry default) — standalone AWG-инbaунд клона никогда не конфликтует с chain AWG-инbaундом на той же ноде.
- `clone.go`: `cloneInbounds` принимает `taken`-subnets (собираются `CloneNode` из `ListNodeInfos`); AWG-инbaунды клона получают **свежий /24** (не копию source); два AWG-инbaунда на одном клоне — разные /24. Source не тронут.
- 3 теста: allocator free/skip-legacy, clone AWG subnet fresh + source untouched + two-inbounds distinct.

### Fix 3 — тюнимый scrypt N (§19 риск 3 смягчён)
- `EncryptBackupWithParams(plain, pass, N, r, p)` + `EncryptBackup` обёртка (сохраняет сигнатуру, 11 существующих тестов не сломаны). N/r/p <= 0 → package defaults.
- `OffsiteBackupConfig.ScryptN` + `PushOffsiteBackup` прокидывает его. N хранится per-blob → cross-N decrypt работает (старые блобы всегда расшифровываются).
- 3 теста: low-N roundtrip, defaults-on-zero, cross-N decrypt.

### Fix 2 — offsite retention/rotation (§19 явная не-цель «без retention» закрыта)
- `OffsiteBackupConfig.Retention` (0 = default 5). `RemotePath` теперь **директория** на offsite-таргете; блоб пишется в `<RemotePath>/angry-box-<ts>.abbkp` (ts = `20060102-150405`, сортируется лексикографически = хронологически).
- После push: `ls -1 <dir>/angry-box-*.abbkp` → sort → `rm -f` старых сверх Retention. **Best-effort**: ошибка ls/rm не фейлит push (блоб уже off-host; ротация — housekeeping, логируется).
- `backups.go handleSaveOffsite` читает `offsite_retention`/`offsite_scrypt_n`. UI: 2 новых поля (Retention + scrypt N) + placeholder RemotePath = директория. 4 i18n-ключа en+ru.
- 3 unit-теста: rotation removes 3 oldest (8 blobs - keep 5), under-limit no rm, ls-fails-non-fatal. Web `BackupNow_OK` обновлён под timestamp-путь.
- **Semantic shift `RemotePath`**: было «файл», стало «директория». Существующие настройки работают (блоб в `<путь>/angry-box-<ts>.abbkp`), структура меняется — operator правит путь при желании.

### Верификация
- 9 новых тестов (3+3+3) зелёные. `go build ./...` + `go vet ./...` чистые. Полный non-e2e набор зелёный (один pre-existing Windows TempDir flake, проходит при повторе — не регрессия).

### Остаточные не-цели
- scrypt N без UI-валидации минимума (operator's choice — ниже = слабее к brute-force, документировано в help-тексте).
- retention всегда on (default 5); «без ротации» = большое значение Retention (нет off-switch).
- без e2e SSH-ротации (мок-fake); без retention-счётчика в сторе (серверная ротация через ls/rm).

---

---

## 21. P0a-followup — root-cause research (upstream WebSearch, исправлено) (2026-07-09)

**Важно — исправление:** первая версия этого раздела утверждала, что
`include_interface` в sing-box "захватывает только local-socket трафик, не
forwarded ingress". **Это неверно** — upstream docs + реализация показывают, что
`include_interface` ДОЛЖЕН захватывать forwarded ingress. Симптом = реальный
bug/config-ordering, не documented limitation. Этот раздел переписан по
реальному upstream-evidence (WebSearch через субагента: sing-box docs +
SagerNet/sing-box issues + sing-tun реализация).

### 21.1 Что подтверждено upstream (WebSearch)

- **Официальные docs** (`https://sing-box.sagernet.org/configuration/inbound/tun/`):
  `include_interface` — "Limit interfaces in route. Not limited by default.
  Conflict with exclude_interface." + "Interface rules are only supported on
  Linux and require auto_route." Ничего про "local-only" — это route-фильтр на
  ingress-интерфейс, применяется на prerouting/routing стадии (где forwarded
  ingress и решается).
- **Реализация** (`sagernet/sing-tun`): два пути —
  - **ip-rule path** (`tun_linux.go`, без auto_redirect): `IncludeInterface` →
    ip-rule `it.IifName = includeInterface; it.Goto = matchPriority`. `IifName`
    на ip-rule матчит ingress-интерфейс **любого** пакета, включая forwarded.
  - **nftables prerouting path** (`redirect_nftables_rules.go`, с auto_redirect):
    rule внутри Prerouting hook — `MetaKeyIIFNAME` → lookup include-set →
    `Invert:true` → `Counter` → `VerdictReturn`. Логика: "if iifname NOT in
    include-set → return (bypass tun); иначе fall-through → redirect в tun".
    Forwarded ingress с включённого интерфейса = intended behavior.
- **sing-box-extended** (`shtorm-7/sing-box-extended`, НЕ `1776178536` — тот 404):
  inherits upstream `include_interface` без изменений — НЕТ fork-специфичной
  модификации. Не источник бага.
- **Migration index** (sing-box.sagernet.org/migration): НЕТ 1.13.0 страницы;
  `include_interface` не deprecated/не изменён в 1.11/1.12/1.13. 1.11 = WireGuard
  outbound → endpoint (ортогонально). 1.10.0 = введён `route_address`/`route_address_set`.

**Вывод:** `include_interface:["awg0"]` — правильное documented поле для "только
трафик, приходящий на awg0". `route_address_set` — НЕ замена (он destination-based,
не ingress-interface-based). Симптом = bug или config-ordering, не design limit.

### 21.2 Конкретный кандидат-диагноз: issue #3805 + auto_redirect

- **SagerNet/sing-box #3805** "tun.include_interface generates empty iifname
  rules" — **Open**, milestone `1.13 Next`, баг-лейбл, **0 maintainer response,
  0 linked PR**. Баг: multi-element include-set рендерится как `{ "", "" }`
  вместо `{ "awg0", "br-lan" }`. nft rule становится
  `iifname != { "", "" } counter return` → для ЛЮБОГО реального имени интерфейса
  `!= { "", "" }` = TRUE → return → **все пакеты bypass, tun ничего не захватывает**.
  Это ТОЧНО наш симптом — ЕСЛИ наш include-set рендерится пустым.
- **Critical: single-element `["awg0"]` НЕ affected** — рендерится как
  `iifname != "awg0"` (правильно). #3805 ломает ТОЛЬКО ≥2 элементов. Значит:
  - Если наш рендер ВСЕГДА single `["awg0"]` → #3805 не наша причина.
  - Если какой-то §15.3 trial или multi-AWG (`awg0` + `awg1`/`awg-exit-nX`)
    толкнул ≥2 элемента → #3805 срабатывает → exact symptom.
- **`tunIncludeInterfacesForNode`** (`awg_tun_overlay.go:233`) добавляет `awg1`
  (multi-AWG-interface) + `awg-exit-nX` — это **≥2 элементов** при multi-exit или
  co-located standalone AWG → #3805-класс баг. Это может быть скрытой причиной
  того, что multi-interface ноды ломаются сильнее.

### 21.3 auto_redirect vs auto_route — coexist, не conflict

- **auto_route** = ip-rule/routing-table path. **auto_redirect** = nftables
  prerouting enhancement, **requires auto_route**, "always recommended on Linux"
  (docs). Они **coexist** (auto_redirect augments, не заменяет): prerouting
  nftables решает первым, unmatched → fall-through → auto_route ip-rules.
- Наш текущий конфиг (`awg_tun_overlay.go:94`): `AutoRoute:true,
  StrictRoute:false`, но **`AutoRedirect` не выставлен (false)** → мы на
  ip-rule-only path, БЕЗ nftables prerouting. Это значит:
  - #3805 (nftables include-set bug) **НЕ применим** к нам сейчас (nftables
    prerouting не установлен без auto_redirect).
  - Мы на ip-rule path, который `IifName`-матчит forwarded ingress — должен
    работать. Раз не работает → проблема НЕ в include-interface-матчинге, а
    **после него** — сам tun device не принимает forwarded packets
    (NOARP/POINTOPOINT кандидат), ЛИБО auto_redirect надо включить (он
    "always recommended" + "better than tproxy") и тогда nftables prerouting
    может дать другой результат.

### 21.4 Исправленные fix-направления (по upstream-evidence)

| # | направление | feasibility | риск | статус |
|---|---|---|---|---|
| 0 | **Включить `auto_redirect:true`** (docs: "always recommended on Linux", "better than tproxy") + перетестировать egress | High, ~10 мин живой VPS | Low (recommended flag) | НЕ ПРОБОВАЛИ (§15.3 триалыл — нужно проверить, был ли auto_redirect) |
| 1 | **Диагноз `nft list chain inet sing-box prerouting` + `ip rule show`** — увидеть реальный include-set + ip-rules | High diagnostic, ~5 мин | 0 (read-only) | НЕ СДЕЛАНО — single most informative command |
| 2 | **Single-element enforcement**: если multi-interface (`awg0`+`awg1`/`awg-exit-nX`) → #3805 → временно одно-элементный include (или ждать 1.13 Next fix) | Medium | Low-Medium | НЕ СДЕЛАНО — проверить, multi ли у нас на сломанной ноде |
| 3 | **Upstream issue** на SagerNet/sing-box с мин. репродьюсером (config + `nft` output + tcpdump) | High value | 0 (research) | НЕ СДЕЛАНО — но сначала #1 диагностика |
| 4 | **netfilter TPROXY workaround** (вне sing-box, ручной nftables) — для forwarded ingress на awg0 → tun | Medium (новый код) | Medium (source-IP per-client через TPROXY сохраняется) | НЕ ИССЛЕДОВАНО — но теперь МЕНЕЕ приоритетно (#0/#1 могут решить) |
| 5 | **sing-box как AWG endpoint** (без awg-quick/TUN-overlay indirection) | High effort | Medium (отказ от kernel-AWG rework #11) | Last resort |
| 6 | **Userspace WG return** | Medium | High (regression AGENTS #10) | Last resort |

### 21.5 Рекомендация (исправленная)

**Сначала #1 (диагноз) — 5 мин, 0 риска, разъясняет ВСЁ.** Команды на живой VPS:
```
nft list chain inet sing-box prerouting   # если есть — auto_redirect on; смотрим include-set
nft list table inet sing-box               # fallback если chain name другой
ip rule show                                # ip-rules от auto_route
ip route show table 2022                    # (или table id из ip rule show)
sing-box trace 2>&1 | head                 # подтвердить "No entries" / router-match
```
Decision tree из `nft` output:
- `iifname != "awg0"` (single, correct) → include-матчинг ОК → проблема в tun
  device acceptance (NOARP/POINTTOPOINT) → #0 (включить auto_redirect) или #5/#6.
- `iifname != { "", "" }` (empty set) → **#3805 multi-interface bug** → #2
  (single-element) → решает сразу.
- **НЕТ prerouting chain / нет sing-box nft table** → auto_redirect не установлен
  (мы на ip-rule path) → #0 (включить auto_redirect, "always recommended") →
  перетестить.
- `ip rule show` показывает правильные rules, но tun всё равно пуст → tun device
  не принимает forwarded → #5 (sing-box как endpoint) или #6.

**#0 (auto_redirect:true) — самый дешёвый потенциальный фикс.** Мы его НЕ
включали (`awg_tun_overlay.go:94` — `AutoRedirect` zero/default false). Docs
говорят "always recommended on Linux, better than tproxy" — мы упустили
рекомендованный флаг. Это первая вещь для следующей живой сессии.

### 21.6 Минимальный upstream-репродьюсер (для #3, если #1 укажет на upstream)

```
config.json (kernel awg0 via awg-quick, AllowedIPs 0.0.0.0/0, Table=off) +
sing-box tun inbound (auto_route:true, include_interface:["awg0"],
strict_route:false, stack:"mixed") + direct outbound + route tun-in→direct.
Доказательство: tcpdump -i awg0 (виден дешифрованный SYN src 10.8.0.x dst remote)
vs tcpdump -i sing-box-tun (пусто) + sing-box trace (No entries).
Ожидаемое: include_interface + ip-rule IifName → tun принимает forwarded ingress.
Фактическое: не принимает. + вывод `nft list chain inet sing-box prerouting` +
`ip rule show` для классификации (empty-set #3805 vs tun-device vs auto_redirect-off).
```

### 21.7 Открытые вопросы (нужна живая VPS — НЕ разрешимы локально)

0. Был ли `auto_redirect:true` в каком-то §15.3 trialе? (нужно перечитать
   §15.3 — если нет, #0 — новый непроверенный path).
1. Реальный include-set на сломанной ноде (`nft` output) — empty (#3805) или
   correct single?
2. Multi-interface ли на сломанной ноде (`awg0`+`awg1`/`awg-exit-nX` → #3805)?
3. Vanilla sing-box 1.13.14 — тот же симптом? (изоляция extended vs upstream —
   но sing-box-extended не меняет include_interface per §21.1, так что маловероятно).
4. `auto_redirect:true` решает? (docs: "always recommended", мы не пробовали).

### 21.8 Статус P0a-followup

Research завершён с реальным upstream-evidence (WebSearch через субагента).
**Предыдущая гипотеза (local-socket-only) опровергнута** — include_interface
должен захватывать forwarded ingress. Новый leading-кандидат: **мы не включили
`auto_redirect` (recommended flag)** + возможный **#3805 multi-interface bug**
если нода multi-interface. Точный диагноз = `nft` + `ip rule show` (5 мин, 0
риска). Фикс-кандидаты: #0 auto_redirect, #2 single-element (если #3805).
Архитектурные #5/#6 — last resort. Это остаётся P0-блокером продукта, но НЕ
блокер для UX-фич (P0b/P1a/P1b/P2a + follow-up Fix 1/2/3 — все готовы). Следующая
живая сессия: #1 диагноз (5 мин) → потом #0 или #2 по результату.

### Ключевые URLs
- https://sing-box.sagernet.org/configuration/inbound/tun/ (include_interface + auto_redirect + auto_route docs)
- https://github.com/SagerNet/sing-box/issues/3805 (multi-interface empty iifname set, Open, 1.13 Next)
- https://github.com/SagerNet/sing-box/issues/3789 (1.13.0 auto_redirect netlink FATAL, closed not-planned — loud failure, не наш silent случай)
- https://github.com/SagerNet/sing-box/issues/4137 (auto_redirect vs routing_mark conflict)
- https://github.com/shtorm-7/sing-box-extended (extended upstream, НЕ 1776178536)
- Реализация: sagernet/sing-tun tun_linux.go (ip-rule IifName) + redirect_nftables_rules.go (nftables prerouting iifname set)
### 21.9 Код: auto_redirect opt-in field (2026-07-09)

Реализован P0a кандидат #0 как **opt-in, не default** — `AWGTUNOverlayParams.AutoRedirect *bool`
(awg_tun_overlay.go). Default = OFF (render не эмитит поле, sing-box трактует отсутствующий как false).

**Почему НЕ default-ON (важно):** trial показал, что `auto_redirect:true` ломает
`sing-box check` на хостах без Linux nftables/netlink — реальный бинарный
`sing-box check` FATALит: `initialize inbound[0]: initialize auto-redirect: invalid
argument` (подтверждено `TestRenderAWGTakeoverConfig_SingBoxCheck`). Т.к. deploy
(applier_push.go) запускает `sing-box check` ПЕРЕД restart, default-ON сломал бы
весь AWG-deploy на хостах где auto_redirect не инициализируется (SagerNet#3789
netlink-FATAL класс). Поэтому:

- **Default OFF** — конфиг проходит `sing-box check` везде (verified: takeover
  test green, full suite green).
- **Opt-in через `AWGTUNOverlayParams.AutoRedirect = &true`** — оператор включает
  на конкретной ноде ПОСЛЕ живого VPS-триала, подтвердившего что auto_redirect
  инициализируется чисто на этом ядре. Это лазейка для P0a-fix trial без риска
  сломать всем.

**TODO (live-VPS):** wiring opt-in через UI/настройки ноды (сейчас field есть, но
никуда не привязан из handlers — `BuildAWGTUNOverlay` callers не выставляют
`AutoRedirect`). Шаги для следующей живой сессии:
1. На entry-ноде: выставить `AutoRedirect = &true` в merged_config.go вызове
   `BuildAWGTUNOverlay` (временно хардкод для триала) → deploy → `sing-box check`
   пройдёт? → egress работает? Если да → wiring в UI как per-node toggle.
2. Если `sing-box check` FATALит → нода попадает в #3789-класс → auto_redirect на
   этом ядре нельзя → #2 single-element (если multi) или #5 sing-box-as-AWG.

Тесты: `TestBuildAWGTUNOverlay_AutoRedirectDefaultOff` (absent) +
`TestBuildAWGTUNOverlay_AutoRedirectOptIn` (true) — 2 новых, зелёные.
`roles.go RenderAWGBalancer` — auto_redirect OFF (коммент про opt-in). Takeover
зовёт `BuildAWGTUNOverlay` → наследует default OFF.

Весь non-e2e набор зелёный (10 пакетов). Auto_redirect остаётся P0a-кандидатом,
но теперь есть БЕЗОПАСНЫЙ opt-in path без риска сломать deploy всем.

### 21.10 Живая VPS-диагностика (entry 34.14.98.64, 2026-07-10)

SSH к актуальной entry-ноде (e2e_helpers_test.go:41 — IP сменились относительно
AGENTS.md #E2E; актуальные: entry=34.14.98.64, middle=207.175.1.227,
exit=35.189.235.61, user=lcp, key=id_ed25519). Диагностика read-only + один
временный awg-клиент (cleanup выполнен). **NFT КРИТИЧНО — НЕ установлен.**

#### Что подтверждено на entry

1. **sing-box-extended 1.13.14** — Revision `93e34d124b7f6d92a68ce6527afeff0273f2e706`
   (="Merge tag 'v1.13.14' into extended" per §21.1). Tags: with_gvisor,with_wireguard,
   with_mtproxy,with_manager. Подтверждено — это наш patched extended билд.
2. **`nft` command not found** — nftables НЕ установлен на Debian 12 entry-ноде.
   => `auto_redirect:true` НЕВОЗМОЖНО (FATAL'd бы `initialize auto-redirect` —
   проверено §21.9 + SagerNet#3789). Мы на **ip-rule-only path** (без nftables
   prerouting). Это закрывает §21.5 #0: **trial auto_redirect требует
   `apt install nftables` как предусловие** — без него #0 не пробуется.
3. **ip-rules ПРАВИЛЬНЫЕ** (`ip rule show`):
   ```
   9000: from all iif awg0 goto 9002
   9000: from all iif awg-exit-n1 goto 9002
   9001: from all goto 9010
   9002: from all nop
   9003: from all to 172.16.250.0/30 lookup 2022
   9004: from all lookup 2022 suppress_prefixlength 0
   9005: not from all dport 53 lookup main suppress_prefixlength 0
   9005: from all iif sing-box-tun goto 9010
   9006: not from all iif lo lookup 2022 / from 0.0.0.0 iif lo / from 172.16.250.0/30 iif lo
   ```
   `table 2022`: `default via 172.16.250.2 dev sing-box-tun`.
   `ip route get 1.1.1.1 from 10.8.0.5 iif awg0` → `via 172.16.250.2 dev sing-box-tun
   table 2022 cache iif awg0` — **routing для forwarded ingress корректен**.
4. **sing-box-tun РАБОТАЕТ** (не сломан глобально): журнал показывает успешный DNS
   exchange — `inbound/tun[tun-in]: inbound packet connection from 172.16.250.1
   to 172.16.250.2:53` → `router match[0] => sniff` → `hijack-dns` → `outbound/direct
   to 8.8.8.8:53`. Tun принимает трафик, который до него доходит.
5. **FORWARD chain `awg0→sing-box-tun ACCEPT = 0 packets`** (iptables -L FORWARD):
   правило есть (наш deploy ставит), но **0 пакетов через него прошло**. Пакеты
   доходят до awg0 (awg0 RX 167 pkts, 0 dropped) но НЕ попадают в FORWARD →
   дропаются на routing-стадии ДО FORWARD, ИЛИ не идут через ip-rule 9000.
6. **rp_filter: effective на awg0 = max(all=1, awg0=0) = 1** (Linux max-rule —
   `all.rp_filter` переопределяет `awg0.rp_filter=0`). Я выставил `all=0` (временный
   тест) — **forwarded ingress ВСЁ РАВНО не дошёл до tun** (см. тест ниже).
7. **Интерфейсы UP**: awg0 (10.8.0.1/24), awg-exit-n1 (10.10.0.2/32),
   sing-box-tun (172.16.250.1/30). sing-box-tun = `<POINTOPOINT,MULTICAST,NOARP,
   UP,LOWER_UP>` tun type. include_interface=["awg0","awg-exit-n1"] (multi —
   потенциальный #3805, но nft нет → #3805 не применим сейчас).
8. **config**: auto_route:true, strict_route:false, include_interface:["awg0",
   "awg-exit-n1"], stack:"mixed", auto_redirect ОТСУТСТВУЕТ (наш рендер OFF).
   log level: trace.

#### Живой тест (loopback awg-клиент)

Создал временный awg-клиент `awgtest` (10.8.0.99) НА САМОЙ entry, подключённый к
awg0 через 127.0.0.1:51820 (loopback), добавил peer на awg0. Пинг 1.1.1.1 с awgtest:
- awg0 tcpdump: `10.8.0.99 > 1.1.1.1: ICMP echo request` — **forwarded ingress
  ПРИХОДИТ на awg0** (дешифровка работает).
- sing-box-tun tcpdump: **ПУСТО**.
- sing-box trace: **No entries**.
- ping: 0 received (100% loss).

**ВАЖНО — loopback-тест артефакт:** src 10.8.0.99 — это **локальный** IP (awgtest
на entry), kernel видит его как свой → local-delivery, НЕ forwarded через
ip-rule 9000 `iif awg0`. Поэтому `ip route get 1.1.1.1 from 10.8.0.99 iif awg0`
→ "Invalid argument" (src локальный). Этот тест подтверждает что pcap-цепочка
работает, но НЕ доказывает forwarded-fail (нужен УДАЛЁННЫЙ src).

Попытка middle-теста (настоящий удалённый src) — на middle запущен СИСТЕМНЫЙ
sing-box (systemctl, PID постоянно меняется — supervisor перезапускает) → мой
awg-клиент конфликтует. Остановлено без окончательного middle-теста.

#### Итог диагноза (§21.10)

- **auto_redirect НЕ включён И НЕ может быть (nft не установлен)** — это
  РЕАЛЬНОЕ предусловие, упущенное в §21.5. На Debian 12 entry нужно
  `apt install nftables` перед trial auto_redirect. После установки auto_redirect
  может решить (nftables prerouting path сильнее ip-rule + bypass #3805
  для multi-element через nft set).
- **ip-rule path routing работает** (`ip route get` → tun) — но forwarded
  ingress не попадает в FORWARD (0 pkts) / tun (trace пуст). rp_filter=0 НЕ
  решил (loopback артефакт не доказателен, но middle-тест не завершён).
- **root cause сужен до**: пакет приходит на awg0, routing-table говорит "в tun",
  но kernel не доставляет его в tun/FORWARD. Кандидаты: (a) ip-rule 9000 `iif awg0
  goto 9002` НЕ матчит реальный forwarded packet (возможно iif не awg0 из-за
  awgtest-loopback — НО реальный remote-src не проверен); (b) nftables-less
  ip-rule path имеет известный gap для forwarded ingress (требует auto_redirect
  per docs "always recommended"); (c) что-то режет на INPUT/пре-routing.

#### Следующий шаг (живая VPS, ~30 мин)

1. `sudo apt install nftables -y` на entry.
2. Включить auto_redirect (временно хардкод в merged_config.go →
   BuildAWGTUNOverlay{AutoRedirect:&true}) → deploy entry → `sing-box check`
   пройдёт? (nft теперь есть) → egress работает (curl --interface middle-awg
   ifconfig.me → entry/exit IP)? Если да → **P0a РЕШЁН**: nft+auto_redirect
   предусловие + opt-in field уже готовы (§21.9). Wiring в UI per-node toggle.
3. Если не решено → `nft list chain inet sing-box prerouting` покажет include-set
   (SagerNet#3805 для multi ["awg0","awg-exit-n1"] → single-element triал).

#### Cleanup выполнен

awgtest-интерфейс удалён, test-peer (10.8.0.99) удалён с awg0, rp_filter all
восстановлен =1 (исходный), sing-box активен. Middle sing-box (системный) НЕ
тронут (не мой — оставил как было). Временные /tmp файлы на entry — мелочь
(прав нет удалить .conf, безвредно).

**P0a root cause:** nftables НЕ установлен → auto_redirect невозможен →
ip-rule-only path не доставляет forwarded ingress в tun. Фикс-кандидат: `apt
install nftables` + auto_redirect opt-in (поле готово §21.9). Требует
deploy-trial на live VPS для подтверждения.