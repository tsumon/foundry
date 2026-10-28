# API Key coding implementation plan

**Goal:** Make API Key the usable primary desktop coding setup without changing
task approvals or silently migrating subscription tasks.

The requested provider is an existing CPA/Grok entry configured in Magpie.
Add an explicit compatible Responses route for it, preserving the official
OpenAI endpoint fence. Do not hardcode personal endpoints, keys or provider IDs
in public source. Verify the same repair/delivery tests with both adapters before
using the selected private configuration for a separate bounded acceptance task.

**Architecture:** Reuse versioned model accounts, immutable routes, native secret
storage, profiles and the existing model-driven coding engine. Add focused setup
to the current settings page, with read-only reconciliation after uncertain local
writes. No new provider gateway, database migration or credential representation.

**Tech stack:** Go, SQLite, React, Electron. **Spec:** [API Key coding](api-key-coding.md).

## Constraints

- Keep existing tasks, baselines, evidence and spent budgets unchanged.
- Do not read real keys or call real providers during development.
- Keep independent approvals, protected tests, strict response validation and
  native sandbox isolation; no fallback, implicit retries or host execution.
- Only reviewed source and public documentation go to GitHub.

## Implementation and checks

- [x] In `web/src/components/OpenAIAPISetup.test.tsx`, cover API setup with no
  OAuth/catalog/inference requests, explicit billing acknowledgement and unchanged
  role defaults. Cover wrong account types, missing credentials, duplicate clicks,
  uncertain route/profile saves and read-only reconciliation. Run targeted Vitest
  before implementing and confirm the missing entry is the failure.
- [x] Add `OpenAIAPISetup.tsx` using `modelRouteApi.list/create`, `api.profiles`
  and `api.saveProfile`. Reuse matching official Responses routes, expose saved
  configuration immediately, lock uncertain outcomes and never retain a key.
- [x] Integrate in `ModelSettings.tsx`; put API accounts before advanced route
  configuration, default new accounts to OpenAI/API Key, select a newly created
  account for credential entry. Make subscription setup explicitly experimental.
  Correct credential/runtime text without weakening backend gates.
- [x] In `internal/model/router_test.go`, reject duplicate Content-Type on API
  responses and verify no retry. Keep the existing successful JSON contract and
  failure/recovery engine tests. Run offline model, engine and HTTP regressions.
- [x] Update bilingual README and desktop guide with the actual API setup,
  protected repository requirements, budget semantics and real-call status.
- [ ] Run complete Go, web, desktop, security and source checks. Inspect the
  rendered settings with isolated fixture data; keep screenshots private.
- [ ] Push reviewed source, check exact-commit Verify and Desktop packages CI,
  build/update the Mac package without opening original data. Report the Windows
  artifact and distinguish installation checks from real provider verification.
- [ ] Use the explicitly authorized existing Magpie CPA/Grok configuration for
  a separate task with a seven-call ceiling. Pin the provider endpoint and model;
  do not spend or modify the historical subscription task's remaining allowance.
  Real acceptance remains pending until performed.
