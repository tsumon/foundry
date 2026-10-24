# Task authorization continuation implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Let an existing coding task explicitly adopt a replacement same-identity subscription grant without rewriting history or resetting budget.

**Architecture:** Append-only task-local continuation records select an effective model snapshot; the original snapshot stays immutable. Dedicated prepare/approve/apply transactions and current metadata validation fence future dispatch. Historical invocations resolve their own snapshot digest.

**Tech Stack:** Go, SQLite, existing engine/HTTP approval pattern, React/TypeScript, Go tests and Vitest.

**Spec:** `docs/public/task-authorization-continuation.md`

**Acceptance status:** backend and workbench implementation are local-fixture
tested only; no real task has been rebound or real continuation accepted. UI
tests exercise zero requests on render, local-only candidate reads, independent
prepare/approve/apply payloads, invalid/expired/stale/denied states, unchanged
unknown 1/7 usage, and effective selection in future consent. The test-only
loopback fixture uses actual web/dist, temporary SQLite, synthetic catalog
lifecycle receipts and actual continuation endpoints; it includes ready, pending,
stale, applied and old-approval-denied tasks. Browser screenshot acceptance,
final combined regression/scanner evidence and remote synchronization are separate
review gates and must be recorded independently; no provider/native/VM acceptance
is claimed by these local tests.

## Global Constraints

- Only local fixtures for development and acceptance. No real login, catalog, model, Keychain, VM or Docker access.
- Do not read or modify real desktop application stores or acceptance projects during implementation/tests.
- Preserve the original task, original model snapshot/profiles, baseline, evidence, invocation ledger, consumed budget and unknown-usage marker.
- No automatic refresh, retry, API fallback or host execution fallback.
- Continuation is metadata-only and independently prepared, approved and applied. It grants no context or execution permission.
- Keep existing workbench navigation and independent approval boundaries. Public documentation contains no personal local paths, account IDs or internal evidence.
- Go 1.27.3, Node 22.23.2, npm 10.9.8; existing dependencies only. GOPROXY=off and GOTOOLCHAIN=local for offline verification.

### Task 1: Append-only authority continuation and dispatch/recovery integration

**Files:**
- Create focused continuation domain/store/engine/HTTP files and matching tests under `internal/domain`, `internal/store`, `internal/engine`, `internal/httpapi`.
- Modify `internal/domain/types.go`, model selection validation, `internal/store/store.go`, credential/model-call authority and invocation recovery consumers, `internal/engine/controlled_calls.go`, `internal/engine/coding_runtime.go`, HTTP mission projection/router only where required.

**Interfaces:**
- Consumes existing `MissionModelSnapshot`, `catalogSelectionTx`, immutable authorization metadata, revisioned mission updates and invocation ledger.
- Produces `domain.EffectiveModelSnapshot(m Mission) (*MissionModelSnapshot, error)` and digest-addressed historical lookup, append-only `model_continuations` mission metadata, engine prepare/approve/apply methods, sanitized HTTP DTOs.
- HTTP: `POST /api/missions/{id}/model-continuations` prepares with `{revision, authorization_id, catalog_operation_id}`. `POST /api/missions/{id}/model-continuations/{continuationID}/approve` and `/apply` consume `{revision,digest}`. Read with the normal mission response. Return existing mission response envelope; no provider/secret fields in DTOs. If router conventions require a naming adjustment, report it before Task 2.

- [ ] Write a store regression with a real SQLite fixture: freeze an original SIWC snapshot, consume one unknown-outcome invocation of a seven-call budget, replace grant, independently record new catalog receipt, prepare/approve/apply continuation. Assert no original data is rewritten, used calls stays one, and effective selection changes only on apply.

```go
before, _ := json.Marshal(m.ModelSnapshot)
used := m.Budget.UsedCalls
// Exercise dedicated preparation/approval/application against the SQLite store.
after, _ := json.Marshal(applied.ModelSnapshot)
if !bytes.Equal(before, after) || applied.Budget.UsedCalls != used {
    t.Fatal("continuation rewrote historical authority or refunded budget")
}
effective, err := domain.EffectiveModelSnapshot(applied)
if err != nil || effective.Roles[domain.Researcher].Authorization.ID != replacementID {
    t.Fatal("approved continuation did not select exact replacement")
}
```

- [ ] Run focused tests before implementation; preserve expected RED evidence in the private task report.
- [ ] Implement canonical records and validation, dedicated transactional operations, expiry/CAS/cleanup guards, append-only protection and sanitized DTOs. Ordinary update paths must reject fabricated/applied continuations.
- [ ] Integrate effective selection into future authority, actual sealed model request and dispatch gates. Integrate digest-addressed original/applied history into recovery without granting historical records current permission. Reject old approvals after application.
- [ ] Add targeted failure tests for every constraint in the spec, including duplicate application, concurrent prepare/account changes, reopening after a committed apply, malformed persisted records and catalog lineage drift. Prove zero secret/provider/VM effects.
- [ ] Add HTTP tests for missing/incorrect revisions/digests, CSRF/body validation, safe mission projection and the three independent operations.
- [ ] Run `go test -count=1 -timeout=600s ./internal/domain ./internal/store ./internal/accounts ./internal/engine ./internal/httpapi` with offline environment. Record command/output; self-review and commit only the task files after passing.

### Task 2: Workbench controls, local browser fixture and final acceptance

**Files:**
- Create `web/src/components/ModelContinuation.tsx` and focused API/types/tests following Task 1's final DTO contract.
- Modify existing task model/details integration, `web/src/components/ModelSnapshot.tsx`, relevant styles, mission types and local browser fixture only as needed.
- Update `docs/public/mac-subscription-workbench.md`, this plan's acceptance evidence and `scripts/alpha-files.txt` for the two public documents.

**Interfaces:**
- Consume sanitized `model_continuations` and existing account/catalog metadata APIs without fetching any provider catalog.
- Use Task 1's exact prepare/approve/apply routes. Display original snapshot separately from approved effective selection.

- [ ] Write failing component/API tests for render causing zero mutations; preparation showing pending material; separate approval and apply clicks; expired/malformed material keeping actions disabled; unchanged 1/7 budget display and no auto context/model call.

```tsx
render(<ModelContinuation mission={fixture} onChanged={changed} />);
expect(screen.getByText(/不调用模型/)).toBeInTheDocument();
expect(fetchMock).not.toHaveBeenCalled();
// Drive explicit prepare, approve and apply with userEvent and assert each
// outgoing method, route and exact revision/digest independently.
```

- [ ] Implement a compact panel within existing task details; surface missing current catalog as a blocker, never a refresh/query shortcut. Explain per-sign-in local IDs and preserve original snapshot details.
- [ ] Add a loopback-only fixture covering pending, stale, applied, and old-approval-denied states with fake identities and no real app storage.
- [ ] Run focused RED/GREEN, all frontend tests and build. Run full Go tests, race and vet offline, export/scanner checks already available locally, and fixture-only browser screenshots with a fresh data directory.
- [ ] Record exact evidence and unresolved limits; do not claim real continuation acceptance. Keep current installed app and real task binding untouched. Commit verified source/docs only; remote synchronization follows existing user authorization and clean-source rules, never publishes private reports or creates a Release.
