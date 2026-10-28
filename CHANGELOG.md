# Changelog

All notable public-facing changes will be recorded here.

## 0.1.0 — 2026-10-28

- Make API Key / Responses configuration the primary desktop model path, with
  explicit endpoint/model/billing confirmation and native credential storage.
  Compatible CPA/Grok gateways no longer depend on ChatGPT subscription login.
- Support bounded serial standard-library Go tasks: research, plan, isolated
  edits, guest tests, confirmed-failure repair, QA/Auditor review and local export.
- Bundle the default isolated Linux VM on Apple Silicon Mac and Windows x64;
  unavailable virtualization stops without host execution or provider fallback.
- Preserve task snapshots, baseline/evidence and consumed calls across upgrades;
  existing tasks use their exact retained runtime. Keep all approvals independent.
- Publish versioned DMG/EXE, hashes, corresponding source and scoped acceptance
  receipts. Mac remains ad-hoc signed (not notarized); Windows remains unsigned.
- Correct bilingual documentation to distinguish the stable Go/API scope,
  experimental subscription features, real provider evidence and local fixtures.

The release receipts identify the exact accepted source and packages. This
version does not add arbitrary-language support, automatic apply, Git writes,
deployment or production signing.

## Earlier Alpha development

- Make development watcher cleanup target the actual Vite process and remove a
  launcher-test scheduling race. Verify cleanup and preserve the controller's
  exit status after both successful and failed runs.
- Record owned connection cancellation before stopping its lifetime context,
  preventing explicit cancellation from being misreported as session expiry.
- Derive native installer acceptance and artifact names from the desktop package
  version, so future prerelease and stable version changes use the same outputs
  as the package builders.
- Add Linux shared verification and desktop source-contract checks to CI;
  native desktop and VM acceptance still require their supported hosts.
- Use literal, non-recursive archive member selection with GNU tar and bsdtar,
  preserving regular-file, size and digest checks for runtime assets and nested
  license notices.

- Bring Windows subscription preparation, single-use connection/browser handoff,
  separately approved catalog and task-account continuation to parity with Mac.
  Native Credential Manager supports bounded complete token bundles using exact
  Foundry-owned segments; incomplete writes and cleanup failures remain blockers.
- Retain verified sandbox bundles across desktop upgrades and select the exact
  frozen identity for existing tasks. Original snapshots, baselines, evidence and
  consumed budgets remain unchanged. No readiness, model call or retry is implied.

- Add bounded, redacted task-response diagnostics that distinguish media/SSE
  parsing, stream termination, provider terminal events and completed-output
  validation without relaxing success checks or retrying calls. Historical
  receipts remain unchanged; unknown usage and consumed budgets stay preserved.

### Desktop Alpha 0.1.0-alpha.2

- Add an opt-in controlled coding workbench that connects account-backed OpenAI
  role calls, committed Go repository imports, isolated candidate edits, controlled
  Docker testing, bounded failure repair, independent review and patch delivery.
- Require exact durable invocation/approval bindings before native credential
  access and each one-shot provider dispatch. Normal startup, catalog and login
  gates remain closed; no automatic refresh, retry or paid fallback.
- Preserve old simulations and protected tests. Add full-loop local HTTP/driver
  regression tests and distinct model/test provenance in the existing UI.
- Add Foundry-scoped Windows Credential Manager storage and isolated persistent
  coding data. Windows real Docker remains unavailable; oversized native secret
  blobs fail closed. This is not a real-account acceptance claim.
- Update Mac/Windows installers, native-mode tests and bilingual usage guides;
  exact-commit CI acceptance is required before distributing artifacts.

### Added

- Native desktop Alpha packaging: Apple Silicon DMG and Windows x64 per-user
  EXE, bundled controller/SPA, private local data, a persistent empty workbench
  and explicit accountless repository simulations. Independent approvals and
  patch/report delivery remain unchanged; no live accounts or Docker are enabled.
- Native Windows path, lock, private-DACL and publication boundaries, installed
  package CI, runtime source/notices, and truthful bilingual installation guides
  with an actual installed-workbench screenshot.
- Source-only macOS arm64 (Apple Silicon) alpha documentation.
- Apache License 2.0 text.
- Public architecture, safety, contribution, security, and alpha-status documents.
- Screenshot-led Chinese and English README entry points with an approval workflow, isolated quickstart, synthetic workbench media, and explicit fixture limits.
- Public model-acceptance preparation documentation covering local inspection, future approval stages, billing evidence, and failure handling.
- A default-closed OpenAI subscription single-inference gate with separate approval and execution, fixed `gpt-6.1-sol` test text, single-use dispatch, bounded streaming receipts and consumed/unknown recovery. Local simulation validates implementation behavior, not real-provider compatibility, quota or billing.

### Security and attribution

- Advance the pinned and minimum Go toolchain from 1.27.2 to 1.27.3 for the standard-library vulnerability `GO-2026-6809`.
- Upgrade the indirect `golang.org/x/text` dependency to `v0.41.0` for `GO-2026-5970` and `GO-2026-6629`.
- Refresh the dependency's checksum and unmodified license evidence.
- Correct the notice totals: 110 npm references plus 8 Go references, mapping to 86 distinct legal texts.

### Current product capabilities

- Local-first serial plan/edit/verify/export workflow over a committed Git snapshot.
- Immutable model and role-policy snapshots for newly created tasks.
- Separate context, single-MCP-call, plan-execution, and delivery approvals.
- Controller-owned synthetic MCP test service behind `--local-mcp-test`.
- Isolated Docker verification using an existing local image with networking disabled.
- Read-only model-account acceptance inspection over local metadata. Separate operator connection, catalog and fixed single-inference gates remain closed at ordinary startup and never imply task acceptance or general live-router access.

### Release status

The public source destination is [tsumon/foundry](https://github.com/tsumon/foundry), with attribution to tsumon and private security reports handled through GitHub. This is a clean source snapshot, not a public copy of private development history. The initial snapshot at commit [`3d30e5d`](https://github.com/tsumon/foundry/commit/3d30e5d) passed [public GitHub-hosted macOS verification and security checks](https://github.com/tsumon/foundry/actions/runs/39188374585); that evidence is commit-scoped. Current desktop artifacts require successful native **Desktop packages** and **Verify** runs for their precise commit. Historical Alpha and current stable releases provide fixed versioned downloads;
CI artifacts remain supplementary and can expire. Mac packages are ad-hoc signed, not notarized; Windows installers are unsigned. The real Mac API acceptance is separately scoped; no independent end-user-machine
or blanket legal/compliance certification is claimed. See [alpha status](docs/public/alpha-status.md).
