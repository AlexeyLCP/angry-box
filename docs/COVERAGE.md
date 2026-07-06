# Test Coverage — 2026-07-06

Per-package statement coverage (`go test ./internal/... -cover`, non-e2e):

| Package | Coverage |
|---|---|
| internal/backend/factory | 100.0% |
| internal/chain | 83.0% |
| internal/backend/singbox | 78.0% |
| internal/config | 72.6% |
| internal/takeover | 64.4% |
| internal/web | 62.1% |
| internal/i18n | 45.5% |
| internal/ssh | 11.2% |

Notes:
- `internal/ssh` 11.2% — most of the package is real SSH I/O against remote
  hosts; unit tests cover the TOFU CheckHostKey + keypair gen + InstallPublicKey
  shape, but the connect/run/upload paths are exercised via e2e (live VPSes,
  build tag `e2e`) rather than unit tests.
- `internal/i18n` 45.5% — the package is a data map; the new
  TestEnRuKeyParity + TestT_FallbackToKey + TestT_LangDefaultsToEnWhenUnknown
  cover the T() logic; the bulk "uncovered" lines are the map literals
  themselves (not executable statements).
- `internal/chain` 83.0% — core orchestration (applier/store/merged_config/
  frozen/cryptogen/awg_*) is well-covered with rollback + port-conflict +
  migration + sentinel tests.
- e2e tests (build tag `e2e`) are NOT in this count — they run against live
  GCloud VPSes and cover the full SSH deploy + sing-box restart + rollback
  path that unit tests mock.

This snapshot is regenerated with `make test-coverage`. The raw
`coverage.out` is git-ignored (build artifact); this file is the human
summary. CTO-review §9 recommended committing coverage numbers so the
baseline is tracked.
