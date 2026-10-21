# OpenAI Single-Inference Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Add an independently approved, default-closed OpenAI subscription inference acceptance path, tested without real credentials or provider calls.

**Architecture:** Reuse immutable acceptance claims/dispatches and current workbench cards. Add a narrow inference controller and store validation, leaving the general router closed. Tests replace network/secret boundaries only, preserving actual ledger, request builder, parsing and UI behavior.

**Tech Stack:** Go 1.27.3/SQLite and React/TypeScript; Node 22.23.2, npm 10.9.8; no new dependency.

**Pre-publication security update:** The 2026-10-21 vulnerability scan reports `GO-2026-6809` in the existing Go 1.27.2 standard library. Task 3 must advance the pinned verification toolchain to Go 1.27.3 and repeat the full scan/verification; baseline results below still belong to Go 1.27.2. This is a patch-level security update, not a platform expansion. See the [Go release history](https://go.dev/doc/devel/release).

**Spec:** [openai-inference-gate.md](openai-inference-gate.md)

## Global Constraints

- Fixed model `gpt-6.1-sol`; fixed prompt `Reply exactly: FOUNDRY_INFERENCE_OK`; expected text `FOUNDRY_INFERENCE_OK`.
- Only POST `https://api.openai.com/v1/responses`, `store:false`, `stream:true`; no extra instructions/history/tools or `max_output_tokens`.
- Independent creation, approval and explicit execution; `max_attempts:1`, deadline 30 seconds, total response limit 1 MiB, material TTL min(10 minutes, credential expiry).
- Default closed; no real login, Keychain access, catalog query or inference in implementation/verification; no refresh, retry, fallback, role/task changes, scripts or Git execution inside Foundry.
- Keep the current workbench UI and all existing approval boundaries. Local fixtures never represent real acceptance.
- No private operational records, identity metadata, tokens or internal analysis in public commits. Only reviewed clean source/documents; no Release.

## Task 1: Backend authority, streaming and recovery

**Files:** create `internal/domain/model_acceptance_inference.go`, `internal/store/model_acceptance_inference.go`, `internal/acceptance/production_inference.go`, `internal/model/subscription_inference.go`, `cmd/foundry/openai_inference.go` and focused tests; modify the existing acceptance domain/store/service, HTTP history and startup wiring only as required. Update `scripts/alpha-files.txt` for all new public files.

**Interfaces:** consumes `ModelAcceptanceMaterial`, `ModelAcceptanceOperation`, existing ledger and `model.CredentialResolver`. Produces `NewProductionInference(*store.Store, ProductionInferenceOptions) *Service` with route/resolver-only options; `ProductionInferenceStatus(account string)` with Enabled/Blocker/Admission; JSON capability `production_inference_enabled`, `production_inference_blocker`, admission profile `openai_siwc_inference_v1`, source `production_inference`, access `inference_once`. Material adds `inference_prompt` and `response_byte_limit`; receipt adds optional `terminal_event`, `output_matched`, `output_digest`, `request_id`, `resolved_model`. OutputMatched must distinguish absent from false. Exact frozen request body is server-derived, not accepted from clients. Match failure is a completed transport with output_matched=false, not automatically a second request.

- [ ] Add failing tests using actual synthetic connection/catalog ledger fixtures. Begin with a runtime interface assertion (avoids compile-only failure), then prove pending and approved operations perform zero secret reads and zero HTTP hits; concurrent execute yields one dispatch only.

```go
type inferenceLedger interface {
  ProposeProductionInference(string, domain.ModelAcceptanceProposal, domain.ProductionAdmission, time.Time) (domain.ModelAcceptanceOperation, error)
}
// In a test using productionStore(t):
// _, ok := any(s).(inferenceLedger); if !ok { t.Fatal("independent inference ledger is missing") }
```

- [ ] Run focused failing tests: `go test ./internal/store ./internal/acceptance -run ProductionInference -count=1`; record the expected missing-gate failure before implementation.
- [ ] Implement fixed material and lineage guards. Require real successful same-credential catalog containing the fixed model and its still-current connection. Reuse existing snapshot/claim machinery with small named extensions rather than duplicating whole ledger functions. Add dispatch reservation/revalidation and finish validation with current process admission.

```go
const OpenAIInferenceModel = "gpt-6.1-sol"
const OpenAIInferencePrompt = "Reply exactly: FOUNDRY_INFERENCE_OK"
const OpenAIInferenceExpected = "FOUNDRY_INFERENCE_OK"
const OpenAIInferenceURL = "https://api.openai.com/v1/responses"
```

- [ ] Implement the dedicated network adapter with exact body, fresh no-proxy/no-redirect connection, bounded SSE, terminal completion/model/text validation, credential-echo defense and finite errors. Reuse safe parsing/usage classification without enabling the general live router or persisting arbitrary output. Add metadata-only startup activation `--openai-inference-route` mutually exclusive with connection/catalog/probe options; expose independent capability in the existing HTTP history.
- [ ] Add table-driven tests for each spec failure class, permissions and recovery: no scope expansion, no refresh/secret writes, 401/403/429/503, quota after deltas, incomplete/broken/oversized/mismatched-model stream, unknown usage, content mismatch, lease change/failure, expiry at each boundary, persistence/restart and forged receipts. Check actual HTTP request method/body/destination and hit counts; do not use real native secrets or production traffic.
- [ ] Run `go test ./internal/domain ./internal/model ./internal/store ./internal/acceptance ./internal/httpapi ./cmd/foundry -count=1`, then scoped race tests, inspect diff and commit backend/tests/manifest only. Supply TDD failure and passing evidence for review.

## Task 2: Workbench integration and synthetic browser fixture

**Files:** `web/src/types.ts`, `web/src/components/ModelAcceptanceOperations.tsx` (extract narrowly focused validation/render helpers if needed), corresponding tests, `cmd/foundry-acceptance` fixture/tests, public allowlist. No new navigation or visual redesign.

**Interfaces:** consume Task 1 JSON capability and frozen/receipt fields. Produce existing propose/decide/execute/read API interactions with exact fixed model, prompt and same-account successful catalog prerequisites. Strictly parse `production_inference` provenance; historical connection/catalog rows remain read-only.

- [ ] Write failing component tests: independent pending→approve→execute, no network mutation on mount/status read, fixed model/body summary, no-token-cap warning, false/absent/mixed capability denial, expiry, malformed or stale response denial, no replay after unknown mutation, usage absent versus zero, output match versus mismatch and expired credential. Use rendered UI events and actual API spies, not source text assertions.
- [ ] Run `npm test -- --run src/components/ModelAcceptanceOperations.test.tsx` from `web` and record RED before implementation.
- [ ] Extend the existing settings card with fixed inference review and separate approval/execution controls; prior phases are read-only in inference mode. Show honest limited single-use authority and unknown quota/remote billing. Preserve legacy simulation tests and production connection/catalog behavior.

```ts
// Existing API calls remain separate; a decision never calls execute.
await modelAcceptanceApi.decide(accountID, operation, "approve");
// Only a later explicit user event may call:
await modelAcceptanceApi.execute(accountID, approvedOperation);
```

- [ ] Add a process-owned local browser fixture for these cards with synthetic metadata and in-memory secrets, using the real inference controller and loopback-only/test-owned transport interception. No production transport override via normal startup flags or HTTP. A fixture's visible label must say local simulation; synthetic success must not satisfy real production lineage outside its disposable database. If a _test.go subprocess fixture is used, keep all interception out of production builds.
- [ ] Run frontend tests/build and fixture-focused Go tests, commit UI/fixture/allowlist updates, and report exact fixture launch command and browser entry URL mechanism without exposing real credentials.

## Task 3: End-to-end verification and publication

**Files:** public feature status/changelog/readmes for accurate scope, `.go-version`, `go.mod`, `scripts/verify.sh`, `CONTRIBUTING.md` and `scripts/alpha-files.txt`; private evidence outside export/repository. Reuse `scripts/verify.sh`, `scripts/export-alpha.mjs` and pinned scanning tools.

- [ ] Upgrade the pinned/minimum Go toolchain and its verification/documentation references to 1.27.3 for `GO-2026-6809`; retain all security scan rules and supported-platform boundaries.
- [ ] Run full `./scripts/verify.sh`; record Go/race/vet, frontend and build results.
- [ ] Browser-check the disposable local fixture on normal desktop and narrow layout: pending/approved/success, quota failure, interrupted stream, expired credential and fresh default-closed startup. Capture screenshots with no real account data; do not interact with the user's live account instance.
- [ ] Run gitleaks source/history, govulncheck, npm audit and actionlint. Export a new clean allowlisted source snapshot, run verification there and confirm internal/private files are absent.
- [ ] Obtain independent spec/security review, resolve blocking findings with regression tests, and repeat affected verification. Public commits must contain no private report/fixture runtime state.
- [ ] Verify origin is `tsumon/foundry`, clean public ancestry and fast-forward destination. Push approved source to the existing public main branch without force or Release. Check both GitHub CI jobs against the exact pushed SHA until completed; fix failures within scope and reverify before pushing again.
- [ ] Hand off exact commit, CI result, screenshots and real-inference-still-closed status. Do not create real inference material or enable the real inference route without a later user decision.
