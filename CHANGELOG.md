# Changelog

All notable public-facing changes will be recorded here.

## Unreleased

### Added

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

The public source destination is [tsumon/foundry](https://github.com/tsumon/foundry), with attribution to tsumon and private security reports handled through GitHub. This is a clean source snapshot, not a public copy of the private development history. The initial snapshot at commit [`3d30e5d`](https://github.com/tsumon/foundry/commit/3d30e5d) passed [public GitHub-hosted macOS verification and security checks](https://github.com/tsumon/foundry/actions/runs/39188374585); that evidence is commit-scoped, and each later commit requires its own successful run. No GitHub Release, tag, package, installer, independent-machine acceptance, or real-provider certification is claimed. The reviewed alpha target is macOS arm64; Intel macOS and other OS/CPU targets remain unverified and unsupported. No blanket third-party legal/compliance clearance is claimed. See [alpha status](docs/public/alpha-status.md).
