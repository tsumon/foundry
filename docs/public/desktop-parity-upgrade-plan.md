# Desktop parity and upgrade implementation plan

> Execute continuously using test-driven development and task-scoped review; no further incremental product approval is required.

**Spec:** `docs/public/desktop-parity-upgrade.md`

**Goal:** Finish subscription preparation on both desktops and safely resume old task metadata against exactly retained runtimes, then verify and ship updated installers.

## Global Constraints

- Preserve existing UI and independent approvals. No automatic retries, refresh, paid-API switching, or host execution fallback.
- Preserve original task snapshots, baseline, evidence and the used 1/7 budget byte-for-byte. Acceptance uses disposable data and synthetic credentials first.
- Native secret checks touch only generated Foundry diagnostic items. Do not log or publish secrets or private records.
- Fail closed on incomplete credentials, invalid paths, damaged archives, expired credentials/evidence, and mismatched runtime identities.
- No Release or unrelated feature expansion. Push only reviewed source and public documentation after verification.

### Task 1: Windows native subscription parity

Own `internal/model/secrets_credential_manager*`, `internal/model/secrets_windows*`, `internal/keychaincheck/`, `cmd/foundry/desktop_openai*`, `desktop/openai-handoff.cjs` and its tests, and platform wording/tests needed for this feature. Coordinate before editing other shared files.

1. Add failing tests for a subscription-sized payload above 2560 bytes, bounded overflow, interrupted writes, missing/tampered chunks, deletion failures, exact-target cleanup, and legacy reads. Exercise the real wrapper with an in-memory native boundary; never call a real account.
2. Implement bounded native segmentation with an explicit commit envelope and exact-target cleanup. Keep every native blob <=2560 bytes; cap total payload at 256 KiB. The unique per-version references and durable write intent in `accounts.CommitModelAuthorization` remain authoritative. A failed/pending write must never yield a readable credential; deletion must also remove orphan segments when the root record is absent.
3. Extend the native diagnostic test to a large synthetic value, asserting absence after cleanup. Do not enumerate Credential Manager or increase its native limit.
4. Permit trusted Windows desktop coding composition and browser handoff; retain ordinary-mode, readonly, unsupported-platform, expiry, duplicate-launch and independent-approval denial tests. Make the preparation probe native on both platforms. Use platform-neutral credential-store wording where necessary without redesigning UI.
5. Run focused RED/GREEN tests, relevant package tests, desktop handoff tests, frontend tests if changed, and Windows compile checks. Commit only owned changes. Report exact tests, limitations and commits. Native Windows execution belongs to final CI, not emulation on Mac.

### Task 2: Exact frozen runtime archive and resolution

Own `internal/vmruntime/`, `internal/engine/` runtime selection and recovery paths, `cmd/foundry/coding_runtime.go`, and trusted desktop runtime startup wiring. Do not modify Task 1 files.

1. Add failing inert-bundle tests for archive publication and reload, old/new coexistence, absent/tampered manifests or assets, unsafe paths, incomplete staging, repeated idempotent retention, and unchanged source files.
2. Add a small private content-addressed archive API in vmruntime. Retain the verified installed runtime before startup, never executing a VM during archival. Preserve native private-directory/DACL and non-symlink rules. Load only validated archives and do not delete retained bundles.
3. Extend C4 capability to resolve complete frozen identities rather than comparing only the current default. Route readiness, execution and cleanup through the selected runtime. Current runtime remains the profile for newly created tasks.
4. Add recovery regression using disposable DB: old frozen mission + old retained runtime + different default passes structural recovery, but missing/tampered archive remains blocked. Assert raw original snapshots, budget, baselines and journal bytes unchanged and zero provider/VM activity.
5. Provide a bounded local legacy-import path requiring an explicit source and expected identity. Avoid arbitrary renderer execution. Exercise its filesystem effects using inert bundles.
6. Run focused tests, relevant engine/vmruntime/cmd/desktop tests and cross-platform compilation. Commit owned changes and record evidence.

### Task 3: Integration, installed acceptance and public delivery

1. Review both implementation diffs against the spec; fix security/correctness findings with regression coverage.
2. Update installer/installed-smoke and UI assertions for parity, safe startup and archive retention. Update bilingual README and public export allowlist; distinguish fixture tests, native credential checks, VM guest tests and live provider acceptance accurately.
3. Run full offline Go/race/vet, frontend, desktop and public export/secret scanning. Run local fixture UI acceptance and installed Mac checks with disposable data. Preserve private before/after metadata for the user's original task.
4. Commit reviewed changes, push the exact source to the existing public repository, and require full Verify and Desktop workflows. Do not lower checks or treat skipped/failed jobs as passed.
5. Verify/download exact-commit DMG and EXE plus digests. Back up before Mac replacement; retain the exact old verified sandbox for the frozen task. Launch updated Mac and verify original task/baseline/evidence/budget invariants with no model invocation.
6. Deliver installer locations, commit and CI links, truthful native acceptance results and any remaining operational prerequisite. Do not call local simulation a successful live model run.
