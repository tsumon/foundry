# Contributing

FOUNDRY is a local delivery workbench with macOS arm64 and Windows x64
desktop targets. Version 0.2.0 adds closed Node/TypeScript/Python inputs beside
Go, with [scoped Mac evidence](docs/public/alpha-status.md) and mandatory native
platform release gates. It is maintained by [tsumon](https://github.com/tsumon). Use
[tsumon/foundry](https://github.com/tsumon/foundry) for non-sensitive issues and
focused pull requests. The project makes no response-time or contribution-acceptance
commitment. Report suspected vulnerabilities privately through
[SECURITY.md](SECURITY.md), never in a public issue or pull request.

## Safety invariants

Contributions must preserve these boundaries unless a separately reviewed design explicitly changes them:

- The deterministic controller, not model or tool output, owns authorization and state transitions.
- Context sharing, each approved MCP call, plan execution, and delivery remain separate approvals.
- The selected source Git repository stays read-only. FOUNDRY exports local patch/report artifacts but does not apply, commit, push, deploy, or migrate.
- Credentials stay in macOS Keychain, Windows Credential Manager or ephemeral test memory. Never place real credentials in tests, SQLite, logs, browser storage, reports, or fixtures.
- No automatic retry of an unknown remote outcome, automatic provider substitution, paid fallback, Docker image pull, external MCP execution, Skill/script execution, or Git write.
- Existing legacy accountless profiles retain their network capability; do not silently reinterpret them as offline.
- Tests that need protocols, credentials, repositories, or accounts use isolated synthetic fixtures.

Read [architecture](docs/public/architecture.md), [safety](docs/public/safety.md), and [alpha status](docs/public/alpha-status.md) before changing behavior.

## Development setup

On Apple Silicon macOS use cgo, a C compiler, Go 1.27.3, Node.js 22.23.2,
npm 10.9.8, and `/usr/bin/git` supporting `--no-lazy-fetch`. A different Git on
PATH is not a substitute. Windows native build/acceptance uses the pinned
`windows-2025` workflow toolchain, cgo and an explicit MinGW-w64 compiler; runtime
Git is resolved from the OS Program Files directory, not PATH. See the desktop
workflow for its exact checks. Unsupported targets must not silently inherit a
pass from cross-compilation. Install frontend dependencies from the lockfile:

```sh
cd web
npm ci
cd ..
```

Use a fresh disposable `FOUNDRY_DATA_DIR`. Do not reuse personal runtime state, real provider profiles, or real credentials. Docker-dependent tests require a running daemon and a preloaded trusted image; never relax `--pull=never` or network isolation merely to make a test pass.

### Pinned v2 inputs

V2 assemblers and their source tests need a separately supplied trusted host Python **3.13 or newer** at an absolute `FOUNDRY_BUILD_PYTHON` path. This build tool is separate from bundled guest Python 3.13.5. Set `FOUNDRY_MULTILANG_INPUTS` to the absolute verified input directory. Do not substitute the host Node/TypeScript/Python for the pinned guest tools: v2 uses Node 24.22.1, TypeScript 5.9.3 and Debian Python 3.13.5 (`3.13.5-2+deb13u5`). The frontend's TypeScript dependency has a separate purpose/version.

`desktop/sandbox/multilang-inputs.lock.json` pins the runtime, compiler, corresponding sources and notices. Build/CI acquisition is an explicit separate operation: `node desktop/sandbox/multilang-inputs.cjs /absolute/input-directory all`. A single architecture (`arm64` or `amd64`) selects that closure; `compiler` prepares only the exact TS compiler fixture for non-native source suites. This acquisition command may download only pinned official inputs and rechecks existing files. The offline assembler and installed app perform no downloads. Supply already verified inputs when working offline; do not run package/project install scripts to bypass a missing fixture.

Before the shared checks, configure the existing local inputs, for example:

```sh
export FOUNDRY_BUILD_PYTHON=/absolute/trusted/python3.13
export FOUNDRY_MULTILANG_INPUTS=/absolute/verified/multilang-inputs
```

Keep normal/race/vet/web/desktop/security checks intact. A compiler-only input is enough only for the source suites selecting that fixture; a native v2 image requires the complete architecture runtime and corresponding-source closure. See [runtime build/sidecar instructions](desktop/runtime-sources.md#v2-offline-runtime-and-source-sidecars).

## Required checks

Run all checks from the repository root unless the command changes directory:

```sh
./scripts/verify.sh
```

The shared script checks exact Go/Node.js/npm versions and system Git capabilities, verifies Go modules, runs exporter tests, normal and race-enabled Go tests, `go vet`, frontend tests, and the production build. It is also the verification entrypoint used by CI.

Linux x86_64 cloud environments run the same shared checks and the desktop
source-contract tests. The source tests model supported native hosts with inert
fixtures; Linux is not a supported desktop packaging or installed-acceptance
target. See the [release readiness gates](docs/public/release-readiness.md) before
promoting a candidate desktop build to a release.

For desktop changes, additionally install the exact desktop lock and run its
tests (`npm ci --prefix desktop`, `npm test --prefix desktop`). The **Desktop
packages** workflow performs native packaging and installed-resource acceptance
on both supported targets. The security workflow audits both npm locks at
`--audit-level=low`, including build dependencies. Do not omit development
dependencies or lower the threshold to obtain a green result.

### Desktop builds

After the locked desktop dependency install, explicitly install its pinned
Electron runtime with `node desktop/node_modules/electron/install.js`. This
Electron version has no automatic `postinstall` hook. The native CI jobs perform
this step before runtime contract tests; source-only checks do not need it.

On the native target, set `FOUNDRY_DESKTOP_OUTPUT` to a **new absolute directory
outside the source tree**. Run `npm run package:mac --prefix desktop` or
`npm run package:windows --prefix desktop`. `FOUNDRY_GO` can select the exact Go
executable. Both builders require `FOUNDRY_SANDBOX_BUNDLE` and `FOUNDRY_SANDBOX_V2_BUNDLE` to select verified native v1/v2 bundles, with matching `FOUNDRY_SANDBOX_SOURCES` and `FOUNDRY_SANDBOX_V2_SOURCES` sidecars. Follow [the offline build sequence](desktop/runtime-sources.md#v2-offline-runtime-and-source-sidecars) before packaging. No signing-account discovery or publishing occurs. Distribute the Electron/runtime source, `sandbox-sources` and `sandbox-v2-sources` beside the installer, outside installed Resources. Building does not prove installation,
Windows ACL behavior, approval flows or clean uninstall; run the matching native
acceptance gates. Keep the v1 installed gate and twelve-case v2 native matrix; preserve Alpha.3 upgrade coverage and supplement it with pinned stable 0.1.0. Hosted Windows model replies are fixtures, not proof of access to a Mac loopback provider. Keep every reused behavioral receipt's tested identity. A docs-only carrying-source rebuild requires measured executable/component equivalence and fresh final package/resource/install checks; changed executable inputs require the affected native gates. Never reuse personal data for package testing.

Document any skipped check and the exact reason. A local pass does not imply GitHub-hosted CI, a clean machine, Docker smoke, or real-provider compatibility has passed.

## Change discipline

Keep changes focused and add tests for behavior changes. Validate failure, restart, and concurrent/repeated-request paths for controller mutations. Unknown or dispatching remote outcomes must remain consumed and must not be automatically replayed. Do not weaken host/origin/session checks, immutable snapshots, revision/epoch fences, scope/protected-path checks, Docker isolation, or bounded input/output handling.

Update public documentation when a user-visible flag, requirement, capability, limitation, approval boundary, or security property changes. Public documents must not link to private planning records, local absolute paths, private runtime data, or generated acceptance artifacts. A committed screenshot must come from a fresh synthetic acceptance run, contain no personal repository, account, credential, token, authorization URL, or host path, and document its source revision, scenario, viewport, and fixture limits in [`docs/media/README.md`](docs/media/README.md). Review both the pixels and embedded metadata before committing it; a screenshot is presentation evidence, not proof of real-provider or Docker behavior.

By intentionally submitting a contribution for inclusion, you agree that it is provided under the repository's [Apache License 2.0](LICENSE), unless explicitly stated otherwise.
