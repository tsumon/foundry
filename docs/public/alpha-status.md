# Alpha status

FOUNDRY is an experimental local-workflow Alpha. It provides source and native
desktop packaging for macOS arm64 (Apple Silicon) and Windows x64. A successful
**Desktop packages** platform job for the exact source commit and its native
acceptance evidence are required before treating its artifact as accepted;
build configuration alone is not a pass. See the
[desktop guide](desktop-guide.md) for installation and actual capabilities.

2026-10-24 acceptance snapshot: Mac `46e1247` passed local physical-Mac install,
native UI, restart and real bundled-VM failure/repair/recheck. Windows `0c87188`
passed the complete native package job: Unicode-path install, cross-drive data,
real WHPX failure/repair/recheck, native UI automation, restart and uninstall with
persistent evidence retained. Download the exact artifacts and inspect workflow
links in the [verified-build table](../../README.en.md#verified-builds--2026-10-24).
Mac GUI evidence is local; Windows GUI evidence is native automation and captured
painted windows, not a claim of manual interactive testing. Roles use local
model-protocol fixtures on both platforms. Newer revisions require their own
gates; real-account end-to-end acceptance remains unauthorized.

## Implemented

- [Mac subscription account workbench](mac-subscription-workbench.md): the trusted
  Mac coding desktop can explicitly prepare native credential readiness, then
  separately propose/approve/execute connection and catalog operations. A manual
  browser handoff is checked against its current owned waiting session. Locally
  recorded catalog evidence binds an explicit profile choice to account, route,
  authorization generation and expiry for future tasks; no catalog receipt grants
  inference. Normal desktop and Windows remain closed. Only synthetic local
  integration/UI acceptance is claimed; real Keychain, sign-in, catalog/model use
  and account-driven task acceptance remain separately unauthorized.

- [Model-driven coding workbench](live-coding.md): a native opt-in composes OpenAI
  account-backed role calls, committed Go imports, immutable candidates, controlled
  sandbox verification, at most two confirmed-failure repairs and final review/
  delivery. Each model send needs an exact durable claimed invocation under the
  current independent approval. It uses no descriptor-supplied solution. Tests
  exercise the production protocol with owned loopback services; synthetic tests
  and native-VM tests have separate evidence. Real-account end-to-end acceptance
  remains pending authorization. Windows Docker remains unavailable.

- [Built-in sandbox](embedded-sandbox.md): the default for new controlled coding
  tasks is a bundled Linux VM (Apple Virtualization on Mac arm64; headless
  QEMU/WHPX on Windows x64), with separate readiness/baseline/execute approvals.
  Standard-library Go tests run without a guest NIC or host filesystem share.
  Docker is explicitly optional on the supported Mac path. Missing hardware,
  changed assets and uncertain shutdown block; no host, TCG or automatic Docker
  fallback. Native installed acceptance is required, not inferred from unit tests.

- Native desktop packaging of the unchanged workbench and Go controller: a Mac
  DMG and Windows per-user EXE. Runtime Go/Node/compiler installation is not
  required. The native menu distinguishes persistent empty data from disposable,
  retained **local simulation** runs. Patch/report exports use bounded native
  save dialogs; installation never grants pending approvals or real effects.
  A separate coding menu enables configuration, not account/sandbox execution.
  The Mac artifact has an ad-hoc
  integrity signature only; the Windows installer is unsigned. Neither is a
  production-signed, notarized or auto-updating release.

- [C4 approval-bound Go verification](coding-verification-c4.md): immutable profile, independent baseline approval/execution, append-only lifecycle, exact-owned cleanup, failure/repair and bound reviews. Production remains default-closed with strict controlled activation and no HTTP/environment bypass or legacy fallback. A separately approved, bounded Double-project C4-C Docker acceptance completed baseline pass → candidate failure → repaired pass with confirmed cleanup. Roles remained simulated; retained historical evidence is not future authority or a general isolation guarantee.
- [Repository-task orchestration](repository-task-orchestration.md): isolated accountless, descriptor-driven local proposals and exact-content checks for arbitrary bounded goals/nested paths, including declared new source files and dependent multifile edits. Existing controller approvals, immutable generations, repair limits, failure replanning, reviews and patch export are reused. This is not autonomous reasoning, compilation or real repository Docker execution. Existing-path import consumes committed HEAD only and preserves uncommitted work; its descriptor must match the commit.

- C3 local repository admission in the ordinary recoverable-task form: explicit inspection consent, exact receipt binding, input/race invalidation, registered project entry, durable import history (including attempts without a task), and frozen source provenance.
- Manual read-only recovery diagnosis in the existing candidate/local-operation view. It reports independently checked dimensions and stale revisions without repairing, retrying, polling, or changing the separate context/execution/delivery approvals.

- Buildable Go controller and React UI with a loopback-only HTTP server.
- Local SQLite state, exclusive data-directory ownership, immutable source/model/policy snapshots, and controller-owned candidate generations.
- Separate context, exact single-MCP-call, plan-execution, and delivery approvals.
- Serial planning, bounded candidate edits, Docker acceptance commands, QA/Auditor review, and local patch/report export.
- Explicit MCP catalog discovery and a controller-owned synthetic task-time MCP fixture behind `--local-mcp-test`.
- Synthetic acceptance harness with fresh temporary state, fake in-memory credentials, local protocol fixtures, and a random loopback port.
- A [content-driven C1 coding-loop fixture](coding-loop-c1.md) exercises an intentionally failing Worker edit, Debugger repair, deterministic simulated verification, independent reviews, and explicit patch export through the production controller. It remains a local simulation and does not execute a shell, Docker, or a cloud model.
- [C2 traceable failure and explicit replanning](coding-loop-c2.md) adds version-one controller traces, sealed failure-to-plan adjustments, distinct evidence-backed QA/Auditor checklists and integrity-bound reports. The `coding_loop_replan` draft scenario exercises separate approvals through local content-check simulation only; it does not certify real project commands, accounts, Keychain, providers or Docker.
- Read-only model-account acceptance inspection over local account, route, credential-generation, and authorization metadata, plus three separately proposed, approved, and explicitly executed connection, catalog, and inference gates against process-owned local fixtures. They do not read real secret values or grant live access; real login, catalog discovery, and inference remain blocked.
- An optional OpenAI identity-only local simulation route requests the fixed `openid profile email` identity scope set through the same separate proposal, approval, and explicit execution gate. It is regression coverage, not a prerequisite for subscription onboarding. It records identity proof only: it does not establish model access, account/workspace selection, entitlement, quota, billing, catalog discovery, inference, or live-login readiness. See [identity-only simulation](openai-identity-connection.md) and [subscription connection direction](openai-stage0.md#current-subscription-connection-direction).
- A dedicated OpenAI subscription connection-only local fixture freezes all six requested scopes and the protocol resource in its approval digest. It requires fresh matching authorization evidence and retains single-attempt/unknown-outcome recovery. Catalog, inference and refresh are unavailable for that account, including through legacy shortcuts; the official resource is an audience, not an external transport destination. See [subscription connection preparation](openai-subscription-connection.md).
- A separate synthetic browser-connection fixture connects the same approval ledger to a process-owned SIWC session: ten-minute human-login lifetime, thirty-second network bound, explicit status reads/cancellation, joined shutdown and consumed/unknown restart recovery. Temporary session handles are released after completion; durable receipts remain. This prepares the [connection handoff](openai-connection-handoff.md), not live admission or a native Keychain acceptance claim.
- OpenAI subscription accounts receive an optional stage-0 metadata projection: account-base address policy, Keychain `not_checked`, remote billing `unverified`, quota `unknown`, and connection/catalog/inference `blocked`. A canonical account base does not prove that every route or endpoint ownership check passed.
- A separate local `foundry-keychain-check` command can exercise generated diagnostic data in an isolated Keychain service with a private recovery journal. It is noninteractive, has no plaintext fallback, does not accept a credential value or enumerate account credentials, and does not update the browser report.

The [OpenAI operator entry](openai-operator-entry.md) is implemented and default-closed, with a paired startup opt-in, fresh journaled native readiness prerequisite and independent workbench connection approval. This is locally simulated implementation evidence, not real-provider acceptance. Installing or starting the default build never grants real-login permission.

The [independent OpenAI catalog gate](openai-catalog-gate.md) adds a separate default-closed operator mode for one approved `GET /v1/models`. It requires a current successful real connection receipt and unexpired matching authorization; connection permission never implies catalog permission. Preparation is metadata-only. Inference, automatic refresh/retry and paid fallback remain closed. Local synthetic tests are implementation evidence only; actual catalog/entitlement/quota acceptance remains unverified.

The [independent single-inference gate](openai-inference-gate.md) adds a mutually exclusive, default-closed operator mode for one separately approved fixed-text `gpt-6.1-sol` request. It requires current connection and same-credential catalog evidence. Preparation/approval are metadata-only; execution reserves one dispatch before leasing its credential. Bounded SSE completion, expected-output match and actual supplied usage are recorded separately, without retaining arbitrary output. Unknown outcomes are consumed, never replayed. Expiry stops without refresh, retry or paid fallback; the timeout/receive bound is not a token or monetary cap. Validation is synthetic only, and does not certify real inference or grant task/role access.

## Deliberately not implemented

- Automatic patch application, source-repository writes, commits, pushes, releases, deployment, or database migration.
- Multi-worker scheduling or autonomous background execution.
- Generic external MCP `tools/call`, MCP stdio/OAuth, Skill/script execution, hook scripts/webhooks, or Git hooks.
- Automatic provider fallback, paid fallback, retry/replay of unknown remote outcomes, or automatic Docker image pulls.
- A browser-triggered native Keychain test or ordinary-startup real-login, live catalog or live-inference action. Only separately confirmed operator activation can prepare the corresponding independent approval workflow; the connection mode's native check is never triggered by a web request. Coding task inference has a separate claimed-invocation capability; it does not open login, catalog or single-inference operator gates.
- A production code-signing/notarization service, automatic updater, package
  manager formula, or GitHub Release. Installers are versioned CI artifacts with
  finite retention, not a permanent release channel.

## Support boundary

The desktop targets are macOS arm64 and Windows x64, subject to each exact
commit's native CI acceptance. Intel macOS, Windows ARM and Linux desktop
installers are not supported by this milestone. Windows repository admission is
deliberately narrower: committed regular `100644` files only, no executable-bit
entries, symlinks, junctions, aliases or alternate data streams. Windows storage
uses protected per-user DACLs and native held locks, not POSIX chmod emulation.

Native credential backends are macOS Keychain with cgo and Windows Credential
Manager. Normal desktop/simulation modes keep credential access disabled; coding
mode uses native storage only for explicit operations. Windows rejects blobs
over its native 2,560-byte limit rather than silently truncating or using plaintext;
larger subscription bundles are therefore unsupported. Windows real Docker stays
closed. Compilation alone does not establish native or real-provider acceptance.

The expected validation toolchain is Go 1.27.3, Node.js 22.23.2, and npm 10.9.8. The trusted system `/usr/bin/git` must pass `/usr/bin/git --no-lazy-fetch --version`; a newer Git elsewhere on `PATH` does not substitute for it. CI targets the public-repository arm64 `macos-26` image and retains that feature check. The initial public snapshot at commit [`3d30e5d`](https://github.com/tsumon/foundry/commit/3d30e5d) completed both macOS verification and security checks in [public workflow run 39188374585](https://github.com/tsumon/foundry/actions/runs/39188374585). That evidence is commit-scoped; it does not cover later commits or any skipped Docker, Keychain, real-provider, or independent-machine check. Docker-dependent behavior requires a running local daemon and a preloaded trusted image. A skipped Docker smoke test is unverified, not passed.

Legacy accountless model profiles preserve their network capability. Live account
routes stay blocked outside the exact controlled-coding invocation capability;
the product is not globally offline. See [safety](safety.md).

## Not yet verified or claimed

- C3 acceptance uses disposable local Git/SQLite/HTTP with synthetic model responses and content checks. It does not execute compiler commands or Docker. C4 real-project / trusted-image readiness remains `not_checked`; a synthetic pass is not real-project readiness.

- No real provider account, API key, subscription, entitlement, quota, billing, balance, or reset-time behavior is certified. The presence of the Keychain diagnostic is not a claim that it passed on this or any other machine.
- No external MCP implementation is certified for task execution.
- No independent fresh Apple Silicon Mac acceptance run is claimed.
- No security audit, penetration test, dependency legal review, or production-readiness assessment is claimed.
- No tag, GitHub Release or support SLA is included. Installer availability and
  verification are tied to successful, retained CI artifacts for a precise commit.

Synthetic local tests validate protocol and controller behavior only. They are not evidence that a real provider, account, MCP server, Docker environment, or user repository is compatible or safe.

## Publication scope and remaining limits

The approved public source repository is [tsumon/foundry](https://github.com/tsumon/foundry). Public attribution is **tsumon**, and commits use a GitHub noreply address. The first public commit is a clean source snapshot, not a copy of private development history. Internal research, preparation records, runtime data, and private credentials are not part of the public tree.

Security reports go to GitHub private vulnerability reporting and are received by **tsumon**. See [SECURITY.md](../../SECURITY.md) for the reporting link and safe handling guidance. The canonical Apache-2.0 license text, including its standard example appendix, is preserved unchanged.

The source snapshot does not resolve every release-readiness question:

1. Third-party notices are byte-level attribution evidence, not blanket legal/compliance clearance. Optional packages for unsupported platforms remain unverified; see [third-party notices](../../THIRD_PARTY_NOTICES.md).
2. GitHub-hosted evidence is commit-scoped. The [initial successful run](https://github.com/tsumon/foundry/actions/runs/39188374585) covers commit `3d30e5d`; each later commit requires its own successful run. A local pass does not certify a hosted run.
3. Docker, real Keychain, real-provider, and independent-machine acceptance must each be reported as run, failed, or skipped based on evidence.

Publishing source or a CI installer does not create a GitHub Release or certify
production readiness. Real models/subscriptions remain paused.
