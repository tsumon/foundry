# Source alpha status

FOUNDRY is an experimental, source-only macOS arm64 (Apple Silicon) alpha. This document separates implemented behavior from release readiness.

## Implemented

- Buildable Go controller and React UI with a loopback-only HTTP server.
- Local SQLite state, exclusive data-directory ownership, immutable source/model/policy snapshots, and controller-owned candidate generations.
- Separate context, exact single-MCP-call, plan-execution, and delivery approvals.
- Serial planning, bounded candidate edits, Docker acceptance commands, QA/Auditor review, and local patch/report export.
- Explicit MCP catalog discovery and a controller-owned synthetic task-time MCP fixture behind `--local-mcp-test`.
- Synthetic acceptance harness with fresh temporary state, fake in-memory credentials, local protocol fixtures, and a random loopback port.
- Read-only model-account acceptance inspection over local account, route, credential-generation, and authorization metadata. It does not read secret values or grant live access.

## Deliberately not implemented

- Automatic patch application, source-repository writes, commits, pushes, releases, deployment, or database migration.
- Multi-worker scheduling or autonomous background execution.
- Generic external MCP `tools/call`, MCP stdio/OAuth, Skill/script execution, hook scripts/webhooks, or Git hooks.
- Automatic provider fallback, paid fallback, retry/replay of unknown remote outcomes, or automatic Docker image pulls.
- An installer, signed/notarized application, package manager formula, or downloadable binary release.

## Support boundary

The reviewed and supported source-alpha target is macOS arm64 (Apple Silicon). Intel macOS (`darwin/x64`) and all other OS/CPU targets are unverified, including their platform-specific dependency-notice coverage, and are not supported by this alpha.

The usable credential backend on the supported target is macOS Keychain with cgo. Other platforms may compile portions of the code but can expose an unsupported secret backend; compilation alone does not establish alpha support.

The expected validation toolchain is Go 1.27.2, Node.js 22.23.2, and npm 10.9.8. The trusted system `/usr/bin/git` must pass `/usr/bin/git --no-lazy-fetch --version`; a newer Git elsewhere on `PATH` does not substitute for it. CI targets the public-repository arm64 `macos-26` image and retains that feature check. The initial public snapshot at commit [`3d30e5d`](https://github.com/tsumon/foundry/commit/3d30e5d) completed both macOS verification and security checks in [public workflow run 39188374585](https://github.com/tsumon/foundry/actions/runs/39188374585). That evidence is commit-scoped; it does not cover later commits or any skipped Docker, Keychain, real-provider, or independent-machine check. Docker-dependent behavior requires a running local daemon and a preloaded trusted image. A skipped Docker smoke test is unverified, not passed.

Legacy accountless model profiles preserve their network capability. New account routes marked `live` are blocked in the production router, but the product as a whole is not globally offline. See [safety](safety.md).

## Not yet verified or claimed

- No real provider account, API key, subscription, entitlement, quota, billing, balance, or reset-time behavior is certified.
- No external MCP implementation is certified for task execution.
- No independent fresh Apple Silicon Mac acceptance run is claimed.
- No security audit, penetration test, dependency legal review, or production-readiness assessment is claimed.
- No public package, installer, tag, GitHub Release, or support SLA is included with this source snapshot.

Synthetic local tests validate protocol and controller behavior only. They are not evidence that a real provider, account, MCP server, Docker environment, or user repository is compatible or safe.

## Publication scope and remaining limits

The approved public source repository is [tsumon/foundry](https://github.com/tsumon/foundry). Public attribution is **tsumon**, and commits use a GitHub noreply address. The first public commit is a clean source snapshot, not a copy of private development history. Internal research, preparation records, runtime data, and private credentials are not part of the public tree.

Security reports go to GitHub private vulnerability reporting and are received by **tsumon**. See [SECURITY.md](../../SECURITY.md) for the reporting link and safe handling guidance. The canonical Apache-2.0 license text, including its standard example appendix, is preserved unchanged.

The source snapshot does not resolve every release-readiness question:

1. Third-party notices are byte-level attribution evidence, not blanket legal/compliance clearance. Optional packages for unsupported platforms remain unverified; see [third-party notices](../../THIRD_PARTY_NOTICES.md).
2. GitHub-hosted evidence is commit-scoped. The [initial successful run](https://github.com/tsumon/foundry/actions/runs/39188374585) covers commit `3d30e5d`; each later commit requires its own successful run. A local pass does not certify a hosted run.
3. Docker, real Keychain, real-provider, and independent-machine acceptance must each be reported as run, failed, or skipped based on evidence.

Publishing this source snapshot does not create a binary release or certify production readiness.
