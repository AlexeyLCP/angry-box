# 03 — Environments

Extracted from AGENTS.md. This file is project law.

---

## Test Servers & E2E Infrastructure

### Личные тестовые VPS (МОЖНО трогать; SSH `root@<ip>`, ключ `~/.ssh/id_ed25519`)

| Alias | IP | OS / kernel | Состояние / правила |
|---|---|---|---|
| `n1` | 144.31.224.212 | Debian 13, 6.12.95 | **ЕДИНСТВЕННЫЙ тестовый сервер, полностью наш** (2026-07-19: lucx-ui снят — x-ui disabled/removed, xray убит, awg1 down; бэкап /root/cleanup-backup-20260719/). amneziawg-tools v1.0.20260618-2 + DKMS module 1.0.20260611 + iptables/nftables/openresolv + tcpdump. Живёт v0.8 trial-деплой (awg-quick@awg0 :51840 + sing-box TUN overlay). Same-host клиент НЕ проверяет egress (IP клиента локален на kernel) — использовать **netns-изоляцию** (veth pair, endpoint на host-veth IP, PROGRESS §28). |
| ~~`n2`~~ | 144.31.157.106 | — | **БОЛЬШЕ НЕ НАШ** (2026-07-19: отдан под тестирование другого продукта). НЕ ТРОГАТЬ. |

### GCloud тестовые (project `project-d4c6c72c-4f10-4288-902`) — могут быть ОСТАНОВЛЕНЫ, проверять доступность до использования:
  - `vps-de-test-1` — 34.40.120.7 (Debian 12, key: `google_compute_engine`)
  - `vps-de-test-2` — 35.198.166.183 (Ubuntu 24.04, key: `id_ed25519`)
  - `vps-de-test-3` — 35.198.100.1 (Ubuntu 24.04, key: `id_ed25519`)

### ПРОДОВЫЕ GCloud (entry 34.14.98.64, middle 207.175.1.227, exit 35.189.235.61) — НЕ ТРОГАТЬ. Никаких деплоев, проб, рестартов, дебаг-команд.

- Run E2E: `go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 300s`
- Auth: `gcloud auth login lucipoher@gmail.com`
