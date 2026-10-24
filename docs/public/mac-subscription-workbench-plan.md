# Mac subscription workbench implementation plan

> For agentic workers: use test-driven implementation and independent code review. Execute the approved scope without additional feature expansion.

**Goal:** safely bridge Mac subscription connection, independently approved catalog discovery and new-task account selection.
**Architecture:** trusted Mac coding composition root + process-local coordinator, reusing SIWC, acceptance and frozen account selections.
**Tech stack:** Go, SQLite, Electron, React/TypeScript, local protocol fixtures.
**Spec:** [mac-subscription-workbench.md](mac-subscription-workbench.md).

## Global Constraints

- Only local simulation for this acceptance; no real login, Keychain access, model catalog/inference or VM/Docker execution.
- Preserve current UI/navigation and independent approvals. No automatic refresh, retry, paid API fallback or host execution fallback.
- Default closed, Mac coding workbench only. Native tests inject secrets and protocol boundaries; production has no HTTP/environment fixture override.
- Keep original private evidence and databases intact. No private records, credentials or history in the public repository; no Release.
- Fresh permission/recovery/UI evidence before push. Check CI without skipping security checks, then deliver an exact-revision DMG with truthful acceptance limits.

## Task 1: Integrated account flow and local regression

Files: `cmd/foundry/main.go`, `cmd/foundry/coding_runtime.go`, new `cmd/foundry/desktop_openai.go` and tests; `internal/acceptance` coordinator and tests; `internal/httpapi` desktop account handlers and tests; `internal/store`/`internal/domain`/`internal/engine` account-catalog provenance integration as needed; `desktop/main.cjs` and narrowly scoped handoff module/tests; `web/src` account, role and task model components/APIs/tests. Register every new public source file in `scripts/alpha-files.txt`.

- [ ] Read the existing connection/catalog constructors, durable claim checks and coding model snapshot path before editing.
- [ ] Add failing tests for default-closed activation, disallowed platform/runtime, same-origin/capability rejection, immutable official route, failed/expired probe, active-operation phase change, concurrent preparation and restart without effects. Observe the expected failures before implementation.
- [ ] Add the process-local coordinator and Mac-only composition. All metadata GETs have zero secret/provider effects. Connection preparation alone does not start OAuth; catalog preparation alone does not query models. Preserve single-use operation claims and independent phase policies.
- [ ] Add failing tests for stale approval, duplicate execution, cleanup uncertainty, expired auth, catalog 429/5xx/malformed/unknown, and no refresh/retry/API fallback. Exercise the actual coordinator/services with local issuer/catalog and memory secret boundaries.
- [ ] Wire account preparation/status into existing settings cards. Keep normal desktop and Windows closed. Add a trusted current-session-only external browser handoff, with rejection tests for arbitrary loopback URLs, stale sessions and repeated launch.
- [ ] Wire locally recorded approved catalog evidence to explicit account/model role selection for future tasks. Test wrong account/route/generation/model, expiry and account switch; verify existing task snapshots remain unchanged. Do not turn catalog selection into model-use approval.
- [ ] Test UI pending/failed/expired/unknown states and the user-controlled browser step. Do not auto-approve or start real operations.
- [ ] Run focused RED/GREEN tests, then full Go, desktop and web tests/build, including race-sensitive paths. Record exact commands/results in private evidence, update these checkboxes only for verified work.
- [ ] Update public guide/status and bilingual README with implemented behavior and remaining real-acceptance limits; do not claim a live connection has been tested.

## Task 2: Review, desktop acceptance, CI and DMG

- [ ] Independent review of Task 1 against the spec; fix material findings with focused regressions.
- [ ] Exercise the local-fixture UI using the actual components/controller; capture only synthetic data. Verify no credential-store/provider effects and no task/sandbox execution from account setup.
- [ ] Run clean public export, secret scans, dependency/security checks and complete local verification. Do not lower standards when infrastructure is unavailable.
- [ ] Commit/push only reviewed public source/docs to `tsumon/foundry`; inspect exact-commit Verify and Desktop workflows, fix failures and rerun only as appropriate. No Release.
- [ ] Obtain/build the exact-revision Mac DMG, verify its checksum/resource manifest and installation contracts; run safe installed mock/configuration UI checks where allowed. Clearly distinguish these from real account or VM execution.
- [ ] Return the DMG location, source revision, CI links and actual local-fixture results. Keep real login and real model use pending the user's distinct authorization.
