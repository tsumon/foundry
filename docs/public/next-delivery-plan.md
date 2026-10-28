# Delivery expansion implementation plan

**Goal:** Eliminate duplicate CI, complete native Windows acceptance where the available environment permits, and extend the approved coding workflow to JavaScript/TypeScript, Python and frozen dependencies. Report real-provider acceptance separately for each platform.

**Architecture:** Keep the current desktop, controller, approvals, immutable task journal and VM execution boundary. Add explicitly versioned verification profiles; preserve existing Go tasks and their retained runtimes. Dependency preparation and model credentials remain outside untrusted project execution.

**Tech stack:** Existing Go controller, Electron desktop, Node test tooling, Apple Virtualization and Windows WHPX.

**Spec:** This document records the approved sequence and the implementation decisions below. The existing `embedded-sandbox.md`, `live-coding.md` and `release-readiness.md` supply the preserved boundaries.

## Global constraints

- Work in the fixed Foundry checkout. No additional Desktop staging directories.
- Preserve all existing tasks, baselines, evidence and consumed model budgets. Use independent acceptance data.
- Retain separate readiness, baseline, context, execution and delivery approvals. No host execution fallback or automatic provider retries/switching.
- Use an explicitly selected OpenAI-compatible API for real-provider acceptance; do not copy long-lived credentials into public CI, repository content, artifacts or logs.
- Do not claim Windows real-provider acceptance from protocol fixtures or Mac evidence.
- Do not mutate released v0.1.0 assets or historic runtime policies. New profiles apply to new tasks only.
- Consolidate local checks and independent review before one source push. Tag publication must not trigger another automatic build of an already checked commit.
- Remove verified regenerable intermediates and redundant copies after successful replacement; retain source, receipts, task data, required rollback artifacts and published assets.

### Task 1: CI event and concurrency policy

**Files:** `.github/workflows/verify.yml`, `.github/workflows/desktop.yml`, `desktop/test/ci-policy.test.cjs`, and the desktop package/lock metadata only if needed to declare the already locked YAML parser.

**Interfaces:** Consumes GitHub push, pull-request and manual dispatch events. Produces one automatic check per main-branch push or PR update, no automatic tag/feature-branch push checks, and independent per-workflow concurrency groups.

- [x] Parse the actual YAML using the existing locked `js-yaml` 4.3.2 package. Add focused regression coverage for `main` pushes, feature-branch pushes, release tags, pull requests and the existing manual Desktop dispatch. Mutation cases must catch restored unrestricted pushes, tag triggers, removed PR checks and shared/cross-workflow concurrency groups.
- [x] Observe the new policy test failing against the unrestricted workflows before editing them.
- [x] Limit automatic `push` to `branches: [main]`; retain all existing pull-request checks and Desktop manual inputs. Set workflow-level concurrency to `${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}` with cancellation of superseded runs. Do not remove jobs, steps, audits, native gates, permissions restrictions, pinned actions, failure conditions or artifact validation.
- [x] Re-run focused tests, the full desktop suite and actionlint. Independently compare all job definitions before/after to ensure event policy is the only workflow change.
- [x] Review the complete diff and commit locally. Do not push during this task.

## Language delivery contract

New tasks may select JavaScript (`node:test`), TypeScript (pinned compiler with real type checking, then `node:test`) or Python (`unittest`) once the corresponding installed runtime and native acceptance are available. Existing Go policies, wire bytes, approval hashes and runtime retention stay unchanged. New profiles and protocol use v2; there is no automatic migration or execution fallback.

Support single-package repositories and frozen pure-language dependencies, validated before baseline. Dependencies are immutable separate inputs, omitted from model source context. Initial dependency import accepts an already resolved package-lock v3 node_modules tree or exact pure Python wheel closure; it never runs package install/build scripts. Unsupported native extensions, loaders, plugin frameworks, monorepo resolution and online dependency installation are rejected explicitly. No claim of arbitrary ecosystem support.

Framework reports authenticate the supervisor's observations, not mathematical correctness of arbitrary project code. Python code sharing its interpreter can modify framework behavior. Protected test bytes, fixed commands, isolated execution and complete structured evidence remain required. Ordinary stdout cannot become test metadata.

### Task 2: pinned runtime inputs

- [x] Pin dual-architecture Node, Python and TypeScript inputs with authenticated official source metadata, exact bytes and hashes, complete ELF loader/library closure, licenses and corresponding source kits.
- [x] Implement a separate v2 offline assembler. Preserve the original static Go assembler; reject archive escapes, collisions, unsafe links, unresolved libraries and unlisted input. Native Windows execution remains an independent acceptance requirement.
- [x] Validate shipped assets in the installed bundle; a missing runtime yields unavailable metadata. Task catalog wiring and native acceptance remain separate gates.

### Task 3: framework evidence and guest contract

- [x] Add a bounded structured report validator for new runners, separate from the unchanged Go event parser.
- [x] Require suite start, sequential unique case lifecycle, one final summary, complete EOF, positive completed pass/fail count and matching process exit. Missing/duplicate/unknown fields, partial output, discovery errors, all-skipped runs, forged stdout and count mismatches cannot pass or authorize repair.
- [x] Add image-owned Node reporter and Python unittest adapter, then a fixed TypeScript compile phase. Compilation/discovery failures are non-passing infrastructure outcomes, not fabricated test counts.
- [x] Add closed versioned profile/policy identities with pre-change v1 byte/hash regression fixtures. These declarations alone do not enable execution.
- [x] Add separate wire identities and bounded dependency transport, including transcript authentication and late-transfer failure tests. Runtime admission remains startup-owned and commands are never supplied by clients.
- [x] Add bounded immutable dependency bundle storage and verified transfer snapshots. This storage layer does not itself approve package graphs or execution.
- [x] Implement npm/wheel admission and versioned protected test/config contracts with offline regression tests. These APIs alone do not enable project execution.
- [x] Integrate those admissions and contracts with profile-aware repository import. Retain all separate approvals and cleanup gates. Backend synthetic workflow, renderer and recovery regressions pass; native execution remains pending.

### Task 4: task, UI and packaging integration

- [x] Wire installed profile selection through task creation, context, plan, patch, failure repair and re-verification. Include language and dependency summary in model context, never dependency archive contents or credentials. Backend and renderer fixture regressions pass; native execution remains pending.
- [x] Preserve the workbench layout; display actual returned runtime eligibility, exact test contract and evidence source. Unsupported retained runtime remains unavailable/read-only. Wide/narrow synthetic UI acceptance passes; installed native acceptance remains pending.
- [ ] Package Mac and Windows with both retained v1 and available v2 runtimes; verify restart, upgrade and recovery with isolated test data.

### Task 5: acceptance and consolidated publication

- [ ] Independently review the complete diff; run security checks and regression suites without omissions.
- [x] Extend the existing native acceptance driver with a closed three-profile matrix, strict authenticated receipts, and both Alpha.3 and stable upgrade inputs. Offline tests and task review pass; actual native and installation execution remain pending.
- [x] Configure the trusted Python build prerequisite and explicitly include the new assembler tests in native Windows selection; missing tools or unrun tests must not be reported as passes. Configuration is locally checked; hosted native execution is pending consolidated CI.
- [ ] Native Mac and Windows VM evidence: baseline pass, deliberate assertion failure, repair pass, protected tests/dependencies unchanged, exact VM shutdown, separate delivery approval. Include no-network/write/isolation and incomplete-result cases.
- [ ] Windows real-provider acceptance requires a reachable authorized provider route. No separate Windows test computer is available for this acceptance. Hosted CI cannot reach a developer machine’s loopback API. Do not export persistent credentials or silently introduce public tunnels; report any missing native-provider evidence separately from fixture/native-runtime acceptance.
- [ ] One consolidated source push after local checks; observe hosted CI and publish installation artifacts only for verified source. Do not trigger duplicate tag builds or rewrite v0.1.0 artifacts.
- [ ] Update bilingual README with only tested support and explicit framework/dependency limits. Delete redundant build intermediates after their replacements and receipts are verified.
