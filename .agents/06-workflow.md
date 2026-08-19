# 06 — Workflow

Extracted from AGENTS.md. This file is project law.

---

## Workflow: How an Agent Executes a Task

```
1. READ    → Read .agents/00-index.md, then 01-purpose.md + 05-rules.md.
             docs/PROGRESS.md: last 1–2 entries only. `git log --oneline -15`.
2. AUDIT   → Read all relevant files, trace data flow end-to-end (e.g., UI → Store → Applier → SSH).
3. PLAN    → Write a short plan: which files to change, what logic to update. Ask for permission if architectural changes are needed.
4. CODE    → Implement changes cleanly following Go, HTMX, and Templ best practices.
5. TEMPL   → Run `templ generate` if any `.templ` files were modified.
6. BUILD   → Run `go build ./...` to ensure no compile-time errors.
7. TEST    → Run tests if applicable (`go test ./...`).
8. COMMIT  → `git add` specific files (never `git add -A` blindly), prefix per Commit Convention.
9. STATUS  → After each commit output `git status` + `git log --oneline -5`.
9.5 PUSH-GUARD → BEFORE `git push` ALWAYS check open PRs and issues:
             `gh pr list --repo AlexeyLCP/angry-box --state open`
             `gh issue list --repo AlexeyLCP/angry-box --state open`
             If there are unhandled PRs (not yours) or issues — do NOT push silently.
             Tell the user what is open and by whom, and propose an order:
             (a) review/merge the PR first, (b) fix the issue first, (c) push after.
             Never push on top of someone's unreviewed work.
10. DOCS   → Every feature/fix commit gets an entry in docs/PROGRESS.md (what was
             done, which files, which tests). Architecture / rules / env / debug
             changes → update the matching .agents/ file. Do not grow AGENTS.md.
```

## Commit Convention

- `fix:` — bug fixes
- `feat:` — new features
- `test:` — test additions
- `docs:` — documentation
- `refactor:` — code restructuring
- Сообщения коммитов — на русском (если не запрошено иное).
- Commits end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

---
