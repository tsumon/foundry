# Changelog

All notable public-facing changes will be recorded here.

## Unreleased

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

The public source destination is [tsumon/foundry](https://github.com/tsumon/foundry), with attribution to tsumon and private security reports handled through GitHub. This is a clean source snapshot, not a public copy of private development history. The initial snapshot at commit [`3d30e5d`](https://github.com/tsumon/foundry/commit/3d30e5d) passed [public GitHub-hosted macOS verification and security checks](https://github.com/tsumon/foundry/actions/runs/39188374585); that evidence is commit-scoped. Current desktop artifacts require successful native **Desktop packages** and **Verify** runs for their precise commit. Distribution uses expiring CI artifacts, not a GitHub Release or tag. Mac packages are ad-hoc signed, not notarized; Windows installers are unsigned. No real-provider, independent end-user-machine, production-readiness or blanket legal/compliance certification is claimed. See [alpha status](docs/public/alpha-status.md).
