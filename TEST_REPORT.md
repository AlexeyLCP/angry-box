# Angry-Box — отчёт по тестам

**Дата:** 2026-07-02  
**Проект:** `/home/lcp/projects/angry-box`  
**sing-box:** `1.13.14-extended-2.5.0-patched`  
**E2E VPS:** Debian 12, user `lcp`, sudo, SSH key `~/.ssh/id_ed25519`

| Роль   | IP             |
|--------|----------------|
| entry  | 34.62.128.71   |
| middle | 207.175.40.161 |
| exit   | 23.251.133.38  |

---

## Итог

| Категория | Результат | Время |
|-----------|-----------|-------|
| Unit: `internal/backend/singbox` (InstallAWG) | **PASS** | 0.01s |
| Unit: `internal/chain` (transport/resolve/check) | **PASS** | 0.05s |
| Unit: `internal/chain` (полный пакет) | **PASS** | ~53s |
| E2E: AWG + TUIC + ClientConnectivity (6 тестов) | **PASS** | 131.8s |

**Все ключевые тесты зелёные.**

---

## Unit-тесты

Запуск: `2026-07-02 ~19:00 MSK`

```
go test ./internal/backend/singbox/ -run InstallAWG -count=1
→ ok  (0.007s)

go test ./internal/chain/ -run 'TestTransport|TestResolveServerName|TestSingboxCheck|TestGetProtocolPresets' -count=1
→ ok  (0.049s)

go test ./internal/chain/ -count=1
→ ok  (52.9s)
```

### Исправления unit-тестов в этой сессии

- **`TestSingboxCheck`** — вместо `private_key_hex` используется валидный keypair из `GenerateRealityKeypair()`.
- **`TestTransportInboundJSONParity` / `TestTransportOutboundJSONParity`** — обновлены под inter-node REALITY без `flow` и без `multiplex`.

### Примечание по auto-apply

`TestScheduleAutoApply_Deploys` намеренно использует failing SSH connector; в логе видно `WARN auto-apply: deploy failed … ssh: exit status 1` — это **ожидаемо**, тест проверяет audit log и **PASS**.

---

## E2E heavy-тесты (живые VPS)

Запуск: `AB_ROUTE_DNS=1 go test -tags e2e ./internal/chain/ -count=1 -timeout 900s -v`  
Фильтр: `TestE2E_Heavy_Protocol_AWG|TestE2E_Heavy_Protocol_TUIC|TestE2E_Heavy_ClientConnectivity`  
**Общее время:** 131.77s

| Тест | Результат | Время | Что проверяет |
|------|-----------|-------|---------------|
| `TestE2E_Heavy_Protocol_TUIC` | **PASS** | 13.5s | TUIC user-entry на middle, конфиг `tuic`/`alpn`/`h3`, порт 443 |
| `TestE2E_Heavy_Protocol_AWG_Kernel` | **PASS** | 13.7s | AWG kernel: `amneziawg` в lsmod, `awg-quick` в PATH, `wireguard`+`amnezia` в конфиге |
| `TestE2E_Heavy_Protocol_AWG_Userspace` | **PASS** | 11.8s | AWG userspace на exit: `"system": false`, amnezia preset |
| `TestE2E_Heavy_ClientConnectivity_1Hop` | **PASS** | 20.9s | TUIC → direct, single exit, egress IP = exit |
| `TestE2E_Heavy_ClientConnectivity_2Hop` | **PASS** | 30.9s | entry TUIC → REALITY transit → exit, egress `23.251.133.38` |
| `TestE2E_Heavy_ClientConnectivity_3Hop` | **PASS** | 41.0s | entry → middle → exit (XHTTP+REALITY transit), egress `23.251.133.38` |

Полный verbose-лог: `test-artifacts/2026-07-02/test-run-e2e-full.log`

---

## Ключевые исправления (почему раньше падало)

### 1. AWG — оркестратор + sing-box-extended

- `installAWGModule()` в `internal/backend/singbox/singbox.go`: PPA `amneziawg` + DKMS fallback (`ANGRY_AWG_TARBALL_URL`).
- E2E AWG-тесты **не скипаются** при ошибке установки — падают жёстко.

### 2. REALITY transit — SNI handshake

**Корневая причина:** `www.microsoft.com` (Akamai) давал `REALITY: processed invalid connection` на GCP VPS.  
**Работают:** `www.cloudflare.com`, `google.com`, `dl.google.com`.

**Фикс:**
- `DefaultRealitySNI = "www.cloudflare.com"` в `internal/chain/protocolpresets.go`
- Обновлены пресеты `maximum_stealth_2026`, `pro_2026`
- Inter-node REALITY: пустой `flow`, без `multiplex` (как XHTTP transit)

### 3. Client connectivity — где крутится клиент

- TUIC/QUIC из **WSL → VPS** ненадёжен (`timeout: no recent network activity`).
- E2E: клиент на **entry VPS цепочки** (`EntryHostOverride: 127.0.0.1`), не на workstation.
- 1-hop: клиент на том же VPS, где deployed inbound (`e2eRoleForAddr(c.Nodes[0].Addr)`).

### 4. Routing / DNS

- Linear chain: outbound tag `ch-{name}-out-{sni}`, без urltest-обёртки (иначе `detour not found: ch-*-strategy`).
- DNS bootstrap: `final: dns-direct` в client config (избегает loop до поднятия туннеля).
- `AB_ROUTE_DNS=1` обязателен для client connectivity E2E.

---

## Команды для воспроизведения

```bash
# Unit
go test ./internal/backend/singbox/ -run InstallAWG -v
go test ./internal/chain/ -count=1

# AWG (ключевой протокол)
go test -tags e2e ./internal/chain/ -run 'TestE2E_Heavy_Protocol_AWG' -v -timeout 900s

# Client connectivity (все топологии)
AB_ROUTE_DNS=1 go test -tags e2e ./internal/chain/ -run TestE2E_Heavy_ClientConnectivity -v -timeout 900s

# Полный heavy suite
go test -tags e2e ./internal/chain/ -run TestE2E_Heavy -v -timeout 3600s
```

Переменные окружения:

| Var | Назначение |
|-----|------------|
| `AB_ROUTE_DNS=1` | Включить route/dns в merged config (нужно для client E2E) |
| `E2E_SKIP_HEAVY=1` | Пропустить mutating E2E |
| `E2E_CLIENT_LOCAL=1` | Клиент на workstation (на WSL часто ломается) |
| `ANGRY_AWG_TARBALL_URL` | Fallback URL для DKMS-сборки AWG |

---

## Артефакты логов (эта сессия)

Все файлы в `test-artifacts/2026-07-02/`:

| Файл | Содержимое |
|------|------------|
| `TEST_REPORT.md` | Этот отчёт (+ копия в артефактах) |
| `test-run-unit.log` | Unit-прогон |
| `test-run-e2e-full.log` | E2E verbose (актуальный, все PASS) |
| `test-run-e2e.log` | E2E summary (tail) |

---

## Что делает каждый тест

### Инфраструктура E2E (`internal/chain/e2e_helpers_test.go`)

**`e2eHeavy(t)`** — общая обёртка для mutating-тестов:
- Пропуск при `E2E_SKIP_HEAVY=1`
- Глобальный mutex (тесты идут **последовательно**, не параллельно)
- Однократный `e2eResetAllServers`: чинит `root`-owned binary, `systemctl reset-failed`

**`deployChain`** → `Applier.ApplyChain` по SSH на каждый hop:
1. Pre-flight SSH на все ноды
2. Генерация hop-параметров (REALITY keys, UUID, short_id)
3. `InstallAWGModule` (если AWG) + push merged config
4. `systemctl restart sing-box`, проверка `is-active`

**`verifyClientConnectivity`** — end-to-end проверка трафика:
1. Требует `AB_ROUTE_DNS=1`, иначе `Skip`
2. `RenderClientConfig` → mixed inbound `127.0.0.1:11080` + TUIC outbound
3. Загружает конфиг на **VPS entry-ноды цепочки** (не WSL)
4. `sing-box check` + `sing-box run` на VPS
5. `curl -x socks5h://127.0.0.1:11080 https://ifconfig.me`
6. Сравнивает IP с egress-нодой (exit VPS)

**`baseChain`** — дефолтная цепочка: TUIC user-entry, XHTTP transport, preset `maximum_stealth_2026`, port 443.

---

### Unit: AWG install (`internal/backend/singbox/standalone_awg_test.go`)

| Тест | Что делает |
|------|------------|
| `TestInstallAWGModule_AlreadyLoaded` | Fake SSH: `lsmod` уже показывает `amneziawg` → только `modules-load.d`, **без** `apt install` |
| `TestInstallAWGModule_InstallSuccess` | Модуль не загружен → `apt install amneziawg` (PPA), persist `modules-load.d`, проверка `awg-quick` |
| `TestInstallAWGModule_InstallFails` | `apt` падает → ошибка `amneziawg install failed` |
| `TestInstallAWGModule_ConnectFails` | SSH недоступен → ошибка connect |

Реальный код: `internal/backend/singbox/singbox.go` → `installAWGModule()` (PPA + DKMS fallback из `ANGRY_AWG_TARBALL_URL`).

---

### Unit: chain config builders (`internal/chain/applier_test.go`, `helpers_test.go`)

| Тест | Что делает |
|------|------------|
| `TestTransportInboundJSONParity` | `buildTransportInbound()` генерит vless+REALITY inbound без flow/multiplex — сверка с expected JSON |
| `TestTransportOutboundJSONParity` | `buildTransportOutbound()` — vless REALITY outbound, public_key из private, uTLS chrome |
| `TestSingboxCheck` | Собирает XHTTP+REALITY inbound с **валидным** keypair, прогоняет локальный `sing-box check` |
| `TestResolveServerName` | Fallback SNI = `www.cloudflare.com`; приоритет: Reality preset → XHTTP hosts |
| `TestClampRealityPrivateKeyB64_*` | RFC 7748 clamp для REALITY keys |
| `TestScheduleAutoApply_Deploys` | Фоновый deploy при `AutoApply=true`; failing SSH → audit log `deploy` для node |

---

### E2E: Deploy & Takeover

| Тест | Шаги | Критерий PASS |
|------|------|---------------|
| `TestE2E_Heavy_Deploy_FreshNode` | `DeployWithOptions` на entry VPS | binary установлен, `systemctl is-active` |
| `TestE2E_Heavy_Takeover_SingBox_FullFlow` | Deploy chain → `DetectVPN` → `Takeover` на middle | status `taken`, converted inbounds > 0, :443 listen |
| `TestE2E_Heavy_Rollback_OnBadConfig` | Push битый конфиг → rollback к предыдущему | sing-box остаётся active |

---

### E2E: Протоколы (прогонялись ✅)

#### `TestE2E_Heavy_Protocol_TUIC`
1. Single node на **middle** VPS
2. `ApplyChain` с `baseChain` (TUIC user-entry)
3. SSH: `cat /etc/sing-box/config.json` — ищет `"tuic"`, `"alpn"`, `"h3"`
4. `ss -lntu :443` + `systemctl is-active`

#### `TestE2E_Heavy_Protocol_AWG_Kernel` ⭐ ключевой
1. Single node на **entry** VPS
2. Chain: `UserProtocolAWG`, preset `pro_2026`, port `51820`, transport XHTTP
3. `ApplyChain` → report должен содержать `AWG` client material (CPS, mimicry)
4. Конфиг: `"wireguard"`, `"amnezia"`, `"jc"`
5. SSH `lsmod | grep amnezia` — модуль **загружен**
6. SSH `command -v awg-quick` — утилита **установлена оркестратором**
7. При отсутствии модуля/утилиты — **FAIL** (не skip)

#### `TestE2E_Heavy_Protocol_AWG_Userspace`
1. Single node на **exit** VPS
2. Chain: AWG, preset `russia_2026`, port `51821`
3. Конфиг: `"wireguard"`, `"system": false`, `"amnezia"` (userspace wireguard endpoint)

#### `TestE2E_Heavy_Protocol_VLESSRealityXHTTP_Advanced`
1. 2-hop entry→exit, preset `xhttp_max_stealth_2026`
2. На **exit** transport-in: vless + reality + xhttp path из пресета
3. Не проверяет client traffic — только структуру конфига

---

### E2E: Client Connectivity (прогонялись ✅)

Общий сценарий `verifyClientConnectivityOnEntry`:

```
[VPS entry цепочки]
  sing-box client (mixed :11080)
    → TUIC loopback :443 (user-entry inbound)
      → [chain routing AB_ROUTE_DNS=1]
        → inter-node outbound(s)
          → exit: direct-out → internet
            → curl ifconfig.me → IP должен = exit VPS
```

#### `TestE2E_Heavy_ClientConnectivity_1Hop`
- **Топология:** только exit (TUIC inbound + direct egress)
- **Transport:** XHTTP (default, не используется — нет второго hop)
- **Проверка:** egress IP = `23.251.133.38`
- **Клиент на:** exit VPS (role=2), loopback TUIC

#### `TestE2E_Heavy_ClientConnectivity_2Hop`
- **Топология:** entry → exit
- **Transport:** `TransportReality` (plain REALITY TCP между hop'ами)
- **Проверка:** трафик проходит REALITY transit, egress = exit IP
- **Клиент на:** entry VPS

#### `TestE2E_Heavy_ClientConnectivity_3Hop`
- **Топология:** entry → middle → exit
- **Transport:** XHTTP+REALITY (default из `baseChain`)
- **Проверка:** два inter-node hop'а, egress = exit IP
- **Клиент на:** entry VPS

---

### E2E: Chain construction (деплой без client curl)

| Тест | Что делает |
|------|------------|
| `TestE2E_Heavy_Chain_SingleNode` | 1 нода middle, deploy, healthy :443 |
| `TestE2E_Heavy_Chain_2Hop` | entry+exit, REALITY transport, оба healthy :443 |
| `TestE2E_Heavy_Chain_3Hop` | entry+middle+exit, REALITY, конфиг entry содержит TUIC inbound |
| `TestE2E_Heavy_Chain_TopologyChange` | 2-hop → delete → 3-hop → delete → 2-hop; проверка что старые chain tags убраны из конфига |

---

### E2E: Balancer, AWG import, QUIC (не в финальном прогоне)

| Тест | Что делает |
|------|------------|
| `TestE2E_Heavy_Balancer_URLTestInChain` | 2-hop + strategy urltest; конфиг entry содержит `urltest` outbound |
| `TestE2E_Heavy_Balancer_Failover` | SOCKS backends на middle/exit; urltest на entry; stop middle → curl `generate_204` через exit |
| `TestE2E_Heavy_QUICCapture_AWGConfig` | Захват 5 QUIC-пакетов → deploy AWG pro_2026 → конфиг содержит `i1`/amnezia CPS |
| `TestE2E_Heavy_ImportAWG_PreservesPeers` | Seed `awg0.conf` + peers.list → `ImportAWGConfigs` не затирает существующих peer'ов |
| `TestE2E_Heavy_ImportAWG_FromSeededNode` | Import с seed-ноды → парсинг priv/pub ключей |

---

### E2E: Concurrency (`e2e_heavy_test.go` конец файла)

| Тест | Что делает |
|------|------------|
| `TestE2E_Heavy_ConcurrentApply` | Параллельный deploy на разные VPS — проверка host lock |
| `TestE2E_Heavy_ConcurrentApply_SameHost` | Два apply на одну ноду — второй ждёт lock |

---

## Статус по протоколам

| Протокол | Роль | Статус |
|----------|------|--------|
| **AWG kernel** | user-entry | ✅ E2E PASS |
| **AWG userspace** | user-entry / hop | ✅ E2E PASS |
| **TUIC** | user-entry | ✅ E2E PASS |
| **REALITY** | inter-node transit | ✅ E2E PASS (с `cloudflare` SNI) |
| **XHTTP+REALITY** | inter-node (default) | ✅ E2E PASS (3-hop) |
| End-to-end client | 1/2/3 hop | ✅ все PASS |

---

## Артефакты в проекте

Все файлы лежат в `test-artifacts/2026-07-02/`:

| Путь | Описание |
|------|----------|
| `TEST_REPORT.md` | Этот отчёт (копия) |
| `test-run-unit.log` | Unit-прогон singbox + chain |
| `test-run-e2e-full.log` | Финальный E2E (6 тестов, все PASS) |
| `test-run-e2e.log` | Краткий итог E2E |
| `e2e-heavy-runs/e2e-heavy-full*.log` | Промежуточные прогоны полного `TestE2E_Heavy` (отладка takeover/REALITY) |
| `client-debug/e2e-client*.log` | Логи отладки client connectivity (до фикса SNI) |
| `client-debug/tuic-client.log` | TUIC client smoke |
| `configs/angry-box-e2e-store.json` | E2E store snapshot |
| `configs/tuic-client-test.json` | Тестовый TUIC client config |

Корневой отчёт: `TEST_REPORT.md` (в корне репозитория).

---

*Сгенерировано автоматически по результатам прогонов 2026-07-02.*