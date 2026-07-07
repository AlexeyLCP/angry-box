# Test Coverage — 2026-07-07

Per-package statement coverage (`go test ./internal/... -cover`, non-e2e):

| Package | Coverage |
|---|---|
| internal/backend/factory | 100.0% |
| internal/chain | 82.9% |
| internal/backend/singbox | 77.1% |
| internal/config | 72.6% |
| internal/web | 61.9% |
| internal/takeover | 60.0% |
| internal/i18n | 45.5% |
| internal/ssh | 42.7% |

Notes:
- `internal/ssh` 42.7% — the v0.3.1 SSH connection pool (`pool.go`) added
  Ping + pool tests, raising coverage from 11.2% to 42.7%. The remaining
  uncovered paths are real SSH I/O (connect/run/upload) exercised via e2e
  (live VPSes, build tag `e2e`).
- `internal/i18n` 45.5% — the package is a data map; TestEnRuKeyParity +
  TestT_Fallback + TestT_LangDefaults cover the T() logic; the bulk
  "uncovered" lines are the map literals themselves (not executable).
- `internal/chain` 82.9% — core orchestration (applier/store/merged_config/
  frozen/cryptogen/awg_*) is well-covered with rollback + port-conflict +
  migration + sentinel + multi-AWG-interface + takeover-materialization tests.
- `internal/takeover` 60.0% — the v0.4.0 re-render path (PushConfigWithAWG +
  RenderTakeoverAWGConf + AwgServerConfigToAmnezia) is unit-tested; the full
  cutover flow is e2e (live VPSes).
- e2e tests (build tag `e2e`) are NOT in this count — they run against live
  GCloud VPSes and cover the full SSH deploy + sing-box restart + rollback
  path that unit tests mock.

CI coverage: `.github/workflows/ci.yml` `build-test` job includes a "Coverage
summary" step that prints `go tool cover -func=coverage.out` per-package so
coverage regressions are visible in the CI log (the raw `coverage.out` is
git-ignored — a build artifact, not a committed baseline).

This snapshot is regenerated with `make test-coverage`. CTO-review §9
recommended committing coverage numbers so the baseline is tracked.