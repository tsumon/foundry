# Contributing

FOUNDRY is an experimental source-only macOS arm64 (Apple Silicon) alpha maintained by [tsumon](https://github.com/tsumon). Use [tsumon/foundry](https://github.com/tsumon/foundry) for non-sensitive issues and focused pull requests. This alpha makes no response-time or contribution-acceptance commitment. Report suspected vulnerabilities privately through [SECURITY.md](SECURITY.md), never in a public issue or pull request.

## Safety invariants

Contributions must preserve these boundaries unless a separately reviewed design explicitly changes them:

- The deterministic controller, not model or tool output, owns authorization and state transitions.
- Context sharing, each approved MCP call, plan execution, and delivery remain separate approvals.
- The selected source Git repository stays read-only. FOUNDRY exports local patch/report artifacts but does not apply, commit, push, deploy, or migrate.
- Credentials stay in macOS Keychain or ephemeral test memory. Never place real credentials in tests, SQLite, logs, browser storage, reports, or fixtures.
- No automatic retry of an unknown remote outcome, automatic provider substitution, paid fallback, Docker image pull, external MCP execution, Skill/script execution, or Git write.
- Existing legacy accountless profiles retain their network capability; do not silently reinterpret them as offline.
- Tests that need protocols, credentials, repositories, or accounts use isolated synthetic fixtures.

Read [architecture](docs/public/architecture.md), [safety](docs/public/safety.md), and [alpha status](docs/public/alpha-status.md) before changing behavior.

## Development setup

Use an Apple Silicon Mac (`darwin/arm64`) with cgo, a C compiler, Go 1.27.3, Node.js 22.23.2, npm 10.9.8, and a system `/usr/bin/git` for which `/usr/bin/git --no-lazy-fetch --version` succeeds. A newer Git elsewhere on `PATH` does not replace the controller's trusted system path. Intel macOS and other OS/CPU targets are unverified and unsupported for this alpha. Install frontend dependencies exactly from the lockfile:

```sh
cd web
npm ci
cd ..
```

Use a fresh disposable `FOUNDRY_DATA_DIR`. Do not reuse personal runtime state, real provider profiles, or real credentials. Docker-dependent tests require a running daemon and a preloaded trusted image; never relax `--pull=never` or network isolation merely to make a test pass.

## Required checks

Run all checks from the repository root unless the command changes directory:

```sh
./scripts/verify.sh
```

The shared script checks exact Go/Node.js/npm versions and system Git capabilities, verifies Go modules, runs exporter tests, normal and race-enabled Go tests, `go vet`, frontend tests, and the production build. It is also the verification entrypoint used by CI.

Document any skipped check and the exact reason. A local pass does not imply GitHub-hosted CI, a clean machine, Docker smoke, or real-provider compatibility has passed.

## Change discipline

Keep changes focused and add tests for behavior changes. Validate failure, restart, and concurrent/repeated-request paths for controller mutations. Unknown or dispatching remote outcomes must remain consumed and must not be automatically replayed. Do not weaken host/origin/session checks, immutable snapshots, revision/epoch fences, scope/protected-path checks, Docker isolation, or bounded input/output handling.

Update public documentation when a user-visible flag, requirement, capability, limitation, approval boundary, or security property changes. Public documents must not link to private planning records, local absolute paths, private runtime data, or generated acceptance artifacts. A committed screenshot must come from a fresh synthetic acceptance run, contain no personal repository, account, credential, token, authorization URL, or host path, and document its source revision, scenario, viewport, and fixture limits in [`docs/media/README.md`](docs/media/README.md). Review both the pixels and embedded metadata before committing it; a screenshot is presentation evidence, not proof of real-provider or Docker behavior.

By intentionally submitting a contribution for inclusion, you agree that it is provided under the repository's [Apache License 2.0](LICENSE), unless explicitly stated otherwise.
