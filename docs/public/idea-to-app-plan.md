# Idea-to-app implementation plan

Spec: [idea-to-app.md](idea-to-app.md).

## Global constraints

Preserve existing tasks, evidence, budgets, independent approvals and VM isolation. No host execution of generated programs, no real provider calls during development, no automatic paid-route switch. Keep existing repository entry functional. Batch changes and publication; scratch files stay inside ignored workspace. No desktop temporary folders or speculative framework redesign.

### Task 1: Managed application backend

Own `internal/domain`, `internal/engine`, `internal/workspace`, `internal/httpapi` only. Implement the spec's idea task API, persisted trusted origin, managed repository seed and empty Node dependency admission using installed v2 metadata. Use the existing import, model snapshot, candidate and recovery machinery. The UI sends only title, goal, acceptance string array, profile_id and max_calls. Return normal Mission with optional idea_project `{schema_version:1, template:"browser_app_v1", acceptance:string[]}`. GET availability must not start VM or read credentials.

Provide a useful fixed browser app seed and protected Node tests. App source paths should support HTML/CSS/JS; document the actual contract in source visible to models. Include requirement-related test guidance without treating model-written tests as independent validation. Ensure new app assets are admitted by existing v2 source validation without widening ordinary imports unintentionally.

Add preview JSON and approved full-source ZIP routes as specified; no arbitrary file reads, network calls or program execution. Preview packaging must preserve a valid document, bundle local app assets, reject unsupported/external asset dependencies and inject a strict first CSP before any generated content. Do not rely on user HTML for isolation. ZIP requires current delivered candidate digest and existing delivery checks. Keep old patch/report behavior.

Tests first: no-repo creation; unauthorized calls remain zero; tampered/protected source rejection; current candidate preview; stale/tampered/undelivered export rejected; complete restored archive; restart persistence. Reuse existing controlled fixtures. Run targeted Go packages with local Go toolchain and no provider network. Do not commit or push; report changed files and exact test commands/results. No subagents.

### Task 2: Default idea entry and native integration

Own `web`, `desktop`, and necessary `cmd/foundry` composition. Default new task is a concise idea form; an explicit existing repository tab retains CreateMission. New form uses existing configured profiles and budget controls, warns of unsupported server/platform output and never asks for Git path or import scope. Show creation/pending approval state accurately. Read Task 1 API contract above. Render preview in an opaque-origin sandbox iframe; clear stale previews when mission/candidate changes. Add complete source download in delivery with clear real test coverage labels.

Enable installed coding capability in default persistent workbench without moving its database or invoking VM. Preserve legacy coding data/menu and recovery support. Electron must allow only intended local preview rendering; never attach controller capability to generated content. Extend download policy for approved application ZIP only.

Test form intent and explicit model/budget selections, failure continuation, existing repository navigation, preview sandbox/CSP and stale output, download policy, desktop default data path and profile compatibility. Use local UI fixtures and native independent acceptance data.

### Task 3: Review, acceptance and delivery

Review the complete diff for approval bypass, origin forgery, immutable test loss, source/archive/preview escapes, startup migration and false evidence claims. Fix concrete findings. Run backend suite, frontend suite/typecheck/build and desktop tests once assembled. Run actual local fixture create-to-deliver workflow and native preview. Update bilingual README and changelog to describe idea-first workflow and actual output limitations. Batch commit/push, follow exact commit CI, produce new Mac/Windows packages, publish one release and install Mac preserving data. Delete own obsolete artifacts after evidence summary and final assets are retained. Report limitations honestly.
