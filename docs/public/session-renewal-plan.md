# Controlled Session Renewal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Keep a subscription registration signed in through explicitly approved, safely serialized credential rotation without granting or replaying any task operation.

**Architecture:** Extend the existing authorization journal and protected credential backend, with a separate persisted renewal policy/material and desktop-owned coordinator. Reuse the existing strict SIWC token transport and immutable binding validation; desktop settings expose metadata and independent consent, not secrets or provider endpoints supplied by the browser.

**Tech Stack:** Go/SQLite, React/TypeScript, Electron; existing Mac Keychain and Windows Credential Manager backends; local loopback OAuth test fixtures.

**Spec:** [session-renewal.md](session-renewal.md)

## Global Constraints

- Existing accounts start with renewal disabled. Real refresh requires a separate, explicit user approval.
- Tests use isolated temporary stores, in-memory fake credentials and literal-loopback OAuth fixtures only.
- Do not modify original task records, baselines, original evidence, authorization-continuation records or consumed model budgets.
- No model-call retry, catalog query, VM execution, host fallback, additional OAuth scope, account switch or paid-API fallback is authorized by renewal.
- Keep existing UI layout and independent operation approvals. Never publish secrets or private acceptance records.
- Mac and Windows share behavior and preserve their existing native secret backends.

## Task 1: Complete the controlled renewal vertical slice

**Files and responsibilities:**

- Create `internal/domain/session_renewal.go`: secret-free material, policy and status structures; immutable binding and revision fields, state enums and validation.
- Create `internal/store/session_renewal.go` and `_test.go`: additive schema, optimistic-concurrency material approval, disable, durable generation dispatch claim and sanitized receipt; no historic task updates.
- Modify `internal/store/model_accounts_schema.go` or existing store initialization call site to initialize the additive schema without rewriting existing account/task data.
- Create `internal/accounts/session_renewal.go` and `_test.go`: desktop coordinator, scheduling, cancel/join and status; reuse `SIWCService` and lifecycle.
- Modify `internal/accounts/siwc.go`, `siwc_operations.go`, `model_authorizations.go` only as necessary to share strict refresh implementation behind the new standing-consent claim. Keep legacy production `Refresh` denied.
- Create `internal/httpapi/session_renewal.go` and `_test.go`; connect through `server.go` and `model_accounts.go` with existing desktop/capability/session/origin/read-only boundaries.
- Modify `cmd/foundry/desktop_openai.go`, `main.go` and tests for trusted desktop construction, startup/shutdown ownership. No enabling flag accepted from an HTTP DTO and no startup credential reads for disabled accounts.
- Create `web/src/components/SessionRenewal.tsx`, local API/types and tests; embed in existing desktop account section. Do not redesign navigation.
- Update relevant public desktop/subscription guides and both README languages to describe opt-in renewal, independent operation approval and local-simulation-only renewal acceptance.

**Interfaces:**

The coordinator exposes `Status(accountID)`, `Prepare(accountID, expectedAccountRevision)`, `Approve(accountID, materialID, expectedRevision)`, `Disable(accountID, expectedRevision)`, `Tick(ctx)` and `Close()`. Define concrete return DTOs/errors in the domain file before wiring consumers. The HTTP account sub-route is `/api/model-accounts/{id}/session-renewal`, GET metadata-only; POST accepts the exact action `prepare`, `approve` or `disable` and the expected revision/material fields, rejecting all extra authority fields. `Tick` is owned by the desktop lifetime, not an HTTP endpoint. An injectable clock supports deterministic tests; simulation transport can only be supplied by the existing constructor-pinned loopback fixture. An approved production policy admits only the fixed official SIWC profile.

- [ ] **Step 1: Write failing permission and lifecycle tests.** In the new account suite, build from `setupAccounts` and the existing SIWC fixture. Default-disabled test checks zero secret Get/Set/Delete and zero requests after `Tick`; approval tests verify exact account revision, material expiry and binding, then one successful refresh with stable authorization ID and generation +1. Include this behavioral contract:

```go
before, _ := ledger.GetModelAuthorization(binding.ID)
// Prepare and approve through the coordinator using a synthetic account.
// Tick twice at the same due time; only the first tick may send this generation.
after, _ := ledger.GetModelAuthorization(binding.ID)
if after.Binding.ID != before.Binding.ID || after.Generation != before.Generation+1 {
    t.Fatal("rotation changed the grant or failed to advance exactly once")
}
if refreshRequests.Load() != 1 { t.Fatal("duplicate refresh dispatched") }
```

Run the new focused suite first and record the expected RED result before implementation: `go test -count=1 -run SessionRenewal ./internal/accounts ./internal/store ./internal/httpapi`.

- [ ] **Step 2: Implement persisted authority and coordinator.** Initialize a separate renewal table/journal with prepared (ten-minute) material and disabled-by-default policy. Atomic approval validates current binding; atomic dispatch checks current consent and generation before claim. Calculate due time as max(expiry minus five minutes, provider earliest time). Use a 30-second desktop ticker; failed/unknown dispatch is terminal until explicit user recovery, while successful generations remain eligible at their next due time. Preserve disabled state through cancellation/commit races. Return typed, sanitized status without tokens or raw provider errors.

- [ ] **Step 3: Extend strict refresh and failure tests.** Reuse token validation and store journal; never loosen success checks. Add table-driven cases for changed subject/client/scopes/resource, revoked account, early refresh, duplicate coordinator/process claims, missing token, terminal provider refusal, truncated response, HTTP 5xx/transport uncertainty, cancellation, secret Get/Set/Delete faults and recovery after durable intent. Verify old rotating tokens are never replayed and unrelated task tables remain byte-identical. Run account/store suites and `go test -race -count=1 ./internal/accounts ./internal/store`.

- [ ] **Step 4: Wire desktop HTTP and UI with tests first.** Test unauthenticated/cross-origin/no-capability/read-only requests, unknown JSON fields, stale consent and account switching. Read metadata and mount the UI with no POST, secret-store operation or network call. Render separate prepare, approve-enable and disable actions; show approval scope and renewal-disabled state clearly. React tests must assert no auto retry after rejected/unknown POST, stale response ignored on account change, and no mutation to other approval components. Use `npm test -- --run` and `npm run build` under `web`, plus the desktop Node test suite.

- [ ] **Step 5: Documentation and review.** Update the relevant guides and READMEs; real renewal remains unperformed, existing live account consent remains absent. Self-review and commit only public source/docs. Provide RED/GREEN command evidence and a diff for independent specification/security review. Resolve findings before release validation.

## Task 2: Full acceptance and dual-platform delivery

**Files:** no historical data changes; use private temporary/evidence directories for logs and screenshots. Modify implementation files only for verified regressions with new tests and review.

**Interfaces:** consumes Task 1's committed source and its local simulation reports; produces exact-commit passing CI and provenance-checked DMG/EXE artifacts.

- [ ] **Step 1: Full local acceptance.** Run `go test -count=1 -timeout=600s ./...`, `go test -race -count=1 -timeout=1200s ./...`, `go vet ./...`, the web test/build suite and `node --test desktop/test/*.test.cjs`. Run the existing secret/security checks without reduced thresholds. Inspect the renewal UI using only an isolated synthetic fixture. Record real UI result separately from unit tests.
- [ ] **Step 2: Preserve and publish.** Re-run the existing private task-preservation verifier and confirm original task revision, invocation count, source/evidence hashes and 2/7 consumption are unchanged. Check remote main for concurrent changes; push scoped clean public commits without Release or private history/materials. Inspect Verify and Desktop packages runs for the exact new commit; investigate failures rather than bypassing them.
- [ ] **Step 3: Artifacts and installed Mac check.** Download only passing exact-commit packaging artifacts, check published checksums and build provenance, retain old installed app recoverably, install/start updated Mac without opting the live account into renewal. Repeat preservation verification. Report DMG/EXE paths, CI URLs, actual local/Windows installer results and explicitly state real refresh was not performed.
