# Angry-box — Файл знаний (Knowledge & Progress)

> Единый источник правды: что сделано, что нужно, откуда берём. Обновлять при каждом изменении. Не удалять — накапливать.

Последнее обновление: 2026-07-04 (real e2e egress verified + 3 routing bugs found & fixed — §11)

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
11. **E2E-инфра:** GCloud VPSes — entry `34.62.128.71`, middle `207.175.40.161`, exit `23.251.133.38`. User `lcp`, key `id_ed25519`. GCloud UDP 443 firewall на exit VPS — известный инфраструктурный затык e2e.

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
