# Model-driven coding workbench implementation plan

> For agentic workers: use subagent-driven-development for implementation and
> independent review. This is one end-to-end delivery, not separate product phases.

**Goal:** Connect real model transport, repository candidates, controlled Docker
tests and bounded repairs in the installed workbench without implicit authority.

**Architecture:** A separately activated coding runtime composes existing model
snapshots/invocation claims with C4 verification. Existing synthetic runtimes and
historical seals remain compatible. Desktop configuration never grants execution.

**Tech stack:** Go, SQLite, React/TypeScript, Electron, existing platform filesystem
and native secret-store adapters.

**Spec:** [Model-driven coding workbench](live-coding.md).

## Global constraints

- Keep the current workbench layout and independent approvals.
- No real credentials, login, provider calls or Keychain reads during development.
- No automatic retry, refresh, account/model substitution or paid API fallback.
- Keep protected tests, original repositories and historical evidence unchanged.
- Use owned local model services and simulated Docker boundaries for automated tests.
- No real Docker acceptance is inferred from mocks or from packaging.
- No Release, private history, private evidence or runtime data in the public push.

## Implementation work packages

### Task 1: Controlled backend composition

Files: `internal/model/router*.go`, `internal/engine/c4*.go`,
`internal/engine/controlled_calls.go`, `internal/engine/model_selection.go`,
`internal/engine/repository_import_create.go`, `internal/domain/c4*.go`,
`internal/httpapi/coding_tasks.go`, `cmd/foundry/coding_runtime.go`, and their tests.

Produces a separately gated coding runtime and `/api/coding-tasks` capability and
creation API. GET is metadata-only. POST consumes the existing `CreateRequest`
shape with an inspected repository and fixed C4 declaration; it neither approves
nor calls a model or Docker. The implementation records the exact response
contract for the UI before integration.

- [x] Add failing tests for model-backed C4 creation and approval-bound invocation;
  keep the old fixture gate rejecting real role selections.
- [x] Add a default-closed production router admission bound to a claimed model
  invocation and exact sealed request. Deny live catalog/probe traffic through it.
- [x] Compose account-backed model calls with controlled readiness/baseline,
  generation editing, actual verifier dispatch, repair and delivery paths.
- [x] Exercise the whole engine with an owned HTTP model server and synthetic
  Docker boundary; assert per-role receipts, failed/passed generations, protected
  files, unchanged source and truthful provenance.
- [x] Cover denial, expiry, duplicate send, changed approvals, cancellation,
  malformed output, quota, unknown results, failed cleanup and restart.

Core red/green command:
`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -count=1 ./internal/model ./internal/engine ./internal/httpapi ./cmd/foundry`.

### Task 2: Native desktop activation and platform storage

Files: `desktop/main.cjs`, `desktop/runtime.cjs`, `desktop/test/`,
`cmd/foundry/desktop.go`, `internal/model/secrets_windows.go`,
`internal/model/secrets_other.go`, and associated platform tests.

Consumes the backend's explicit `--desktop --coding-workbench` launch contract.
Normal launch remains unchanged. Produces a native-menu opt-in, retained isolated
coding data location, exact argument/environment allowlist and Foundry-scoped
native credential adapter. No startup credential operations are permitted.

- [x] Add tests that normal launch stays closed and only the exact native coding
  mode enables configuration; arbitrary CLI/environment injection remains denied.
- [x] Add the coding menu and runtime launch, retaining renderer/download isolation.
- [x] Add Windows native credential storage with exact namespace/probe isolation,
  size/error bounds and fake-native tests; do not read real credentials.
- [ ] Test Windows/Mac startup and shutdown, and preserve retained evidence on
  switch, crash, upgrade and uninstall.

Commands: `node --test desktop/test/*.test.cjs`; platform Go tests in the existing
Windows/macOS CI jobs. Native private test credentials may only be exercised in
the CI test namespace, never through a real account.

### Task 3: Existing-workbench setup and evidence UI

Files: `web/src/components/CreateMission.tsx`, a focused coding setup component,
`web/src/components/DecisionPanel.tsx`, `web/src/components/C4Verification.tsx`,
`web/src/types.ts`, `web/src/api.ts`, tests, and truthful public documentation.

Consumes `/api/coding-tasks` and existing account/settings/import/C4/mission APIs.
Shows runtime availability, selected account/model/spend class, image and fixed
policy, call-budget limitations and pending independent decisions. Never stores
credentials in browser storage or starts provider/Docker effects on render.

- [x] Add failing UI tests for default-closed setup, stale/missing metadata and
  distinct model/testing provenance.
- [x] Add model-backed task creation alongside the existing task form without
  changing navigation or removing simulation paths.
- [x] Update context/execution decisions so real routes are not described as local
  synthetic endpoints; show limits and preserve separate approval actions.
- [x] Verify errors and loading states never imply authorization or success.
- [x] Update bilingual README and desktop guide with actual implemented behavior
  and explicit pending real-account acceptance, supported projects/platforms.

Commands: `npm test -- --run` and `npm run build` in `web/`.

### Task 4: Integrated review, installers and CI

- [x] Independent security/correctness review of the assembled diff, especially
  task admission, legacy bypasses, recovery and credential dispatch boundaries.
- [x] Full Go tests, race tests, vet, frontend and desktop tests; secret/dependency
  scans and workflow checks without lowering their standards.
- [ ] Build Mac installer; test the installed app in a fresh private data location
  with local inputs. Keep screenshots and detailed evidence outside the repository.
- [ ] Commit only reviewed source/public documents, push to `tsumon/foundry`, and
  inspect both Verify and Desktop packages workflows for that exact commit.
- [ ] Obtain Windows package and native CI installation/render checks; report any
  unsupported real-execution capability as unavailable, not as a passed feature.
- [ ] Deliver updated installers, checksums and a concise result that distinguishes
  implemented transport from still-unauthorized real-account acceptance.
