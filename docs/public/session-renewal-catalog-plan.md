# Renewal/catalog prerequisite implementation plan

Goal: admit only proven same-registration renewal successors when preparing a new catalog request, without renewing old approvals or evidence.

Design: [Renewal and catalog prerequisite lineage](session-renewal-catalog.md).

### Task 1: Implement evidence-backed connection lineage and offline regressions

1. Inspect `internal/store/model_acceptance_catalog.go`, `model_acceptance_operations.go`, `session_renewal.go`, `model_authorizations.go`, their tests, and the local session-renewal browser fixture. Keep the change narrow; no transport or live-data changes.
2. Add a failing regression using the real synthetic store lifecycle: complete connection, enable renewal, commit and finish renewal with retired secret cleanup, then prepare a new independently approved catalog operation. Record RED before production edits.
3. Add an append-only nonsecret connection-origin authorization snapshot side table. Capture on successful connection in the same transaction. Capture legacy exact-match origin at renewal claim before changing credentials where proof exists; never rewrite a receipt or fabricate an old expiry. Validate snapshot hashes against original connection result before use.
4. Extend only the connection prerequisite mismatch path to verify a complete contiguous committed renewal chain, original result digest, exact immutable binding/account/route, latest connection claim, and no pending/unknown cleanup. Keep all exact current-generation checks at catalog proposal, approval, claim, dispatch, completion, catalog selection, and task continuation.
5. TDD negative cases: different binding or registration; changed route/account; missing, unknown, failed, skipped, uncommitted edges; pending secret cleanup; corrupt/missing origin; credential expiry; rotation after catalog approval; old catalog receipt stays invalid. Include multi-generation success and database reopen where practical. Check original connection/old task/evidence/budget records are unchanged.
6. Extend a local synthetic HTTP/browser fixture to expose a renewed account, usable catalog preparation, pending independent approval, and unchanged original budget. No external transport, real Keychain, VM, Docker, account, login, or model call. UI layout remains unchanged. Add tests proving readiness/preparation has no dispatch side effects.
7. Update design details to match actual implementation, especially legacy limitations. Run focused tests during iteration, broader affected suites before completion, and record exact commands/results. Main controller performs independent review, full offline acceptance, and browser acceptance.

### Release and preservation gates

- Check original task/baseline/evidence and consumed budget before/after using the existing read-only preservation verifier. Do not install over or stop the active app; continuous renewal remains enabled.
- Run offline Go tests/race/vet and web/desktop tests/build with cached dependencies. Exercise only local synthetic UI fixtures.
- Review final diff, test evidence, documentation and absence of sensitive material. Do not modify safety gates or protected project tests.
- Push and build both packages only within execution authorization. Existing Windows package CI requires real VM execution; if that conflicts with the current no-VM instruction, report the permission blocker rather than triggering it or skipping a mandatory check.
- Report actual tested results and exact artifact/CI status; never describe older packages as containing this fix.
