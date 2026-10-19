# Changelog

All notable public-facing changes will be recorded here.

## Unreleased

### Added

- Source-only macOS arm64 (Apple Silicon) alpha documentation.
- Apache License 2.0 text.
- Public architecture, safety, contribution, security, and alpha-status documents.
- Chinese and English README entry points with an approval workflow and isolated quickstart.

### Security and attribution

- Upgrade the indirect `golang.org/x/text` dependency to `v0.41.0` for `GO-2026-5970` and `GO-2026-6629`.
- Refresh the dependency's checksum and unmodified license evidence.
- Correct the notice totals: 110 npm references plus 8 Go references, mapping to 86 distinct legal texts.

### Current product capabilities

- Local-first serial plan/edit/verify/export workflow over a committed Git snapshot.
- Immutable model and role-policy snapshots for newly created tasks.
- Separate context, single-MCP-call, plan-execution, and delivery approvals.
- Controller-owned synthetic MCP test service behind `--local-mcp-test`.
- Isolated Docker verification using an existing local image with networking disabled.

### Release status

The public source destination is [tsumon/foundry](https://github.com/tsumon/foundry), with attribution to tsumon and private security reports handled through GitHub. This is a clean source snapshot, not a public copy of the private development history. No GitHub Release, tag, package, installer, hosted-CI pass, or real-provider certification is claimed by this changelog. The reviewed alpha target is macOS arm64; Intel macOS and other OS/CPU targets remain unverified and unsupported. No blanket third-party legal/compliance clearance is claimed. See [alpha status](docs/public/alpha-status.md).
