# Supported scope — candidate 0.2.0 and released 0.1.0

## Candidate 0.2.0 (acceptance pending)

The current source adds fixed single-package JavaScript `node:test`, TypeScript typecheck then `node:test`, and Python `unittest` profiles beside standard-library Go v1. New profiles use the bundled VM only, with explicit immutable local input: frozen pure-JS npm lock-v3 dependencies or exact locked pure Python wheels. Tests, helpers, manifests, locks, TS config and dependency payload are protected. No arbitrary packages, native extensions, plugins, monorepos, other frameworks or in-guest installers are supported. See the [input walkthrough](desktop-guide.md#candidate-020-language-and-dependency-inputs).

Catalog eligibility is not a readiness check, test result or native attestation. Compile, setup/discovery and zero-case infrastructure failures cannot become passing tests or repair evidence. Existing independent approvals and retained runtime identities remain in force.

Implementation/source fixtures and static package validation exist; actual candidate Mac VM, installed UI/upgrade, new-language provider, hosted Windows WHPX/installer and exact-commit CI gates remain pending. No candidate 0.2.0 download or successful run is asserted here. The native matrix uses explicitly synthetic model replies; it cannot extend historical Mac Go real-provider proof. Hosted Windows cannot reach a Mac loopback CPA service, so its fixture workflow cannot establish Windows real-provider access.

## Released 0.1.0

Foundry's first stable scope is a serial, approval-bound coding workflow for
committed standard-library Go repositories. This path remains available under the
historical `alpha-status.md` URL so older links keep working.

## Supported

- Apple Silicon Mac and Windows x64 desktop installers, bundled Linux VM and Go
  verifier; local Git is required. Windows requires already-enabled WHPX.
- Explicit API Key setup for OpenAI or compatible Responses endpoints, native
  credential storage and immutable per-role account/model snapshots.
- Read-only repository import, bounded outgoing context, separately approved
  plans, isolated candidate edits, protected tests and real guest verification.
- At most two repairs of confirmed test failures, separate QA/Auditor review,
  explicit local patch/report delivery and retained invocation evidence.
- Persistent project, target, environment and resource records; separately
  labeled disposable demonstrations.
- Upgrade retention of exact sandbox identities, baselines, original evidence and
  consumed budgets; uncertain operations stop without replay or host fallback.

## Evidence and limits

[Mac CPA/Grok acceptance](compatible-api-acceptance.md) records a real API-driven
code delivery with five role calls and passing baseline/candidate VM tests. That
first candidate passed. A separate six-call real-provider run then exercised a
deliberately injected failure, Debugger repair and passing nine-test VM recheck.
Both runs are documented with their scope; Release receipts bind the final assets. Windows native installation and WHPX tests use local
model protocol fixtures; do not present them as Windows real-provider acceptance.
The [release](https://github.com/tsumon/foundry/releases/tag/v0.1.0) records exact
commits, package hashes and scoped checks. Tests on a prior build are historical,
not automatic acceptance of a later binary.

The Go guest has no network or host shares. It cannot install dependencies or
validate native Mac/Windows applications, websites or arbitrary language stacks.
Only supported regular committed files are imported; source/protected files are
not changed. Windows repositories must share the drive used by Foundry data.
Call ceilings and timeouts are not financial caps. Set provider spending limits.

## Experimental or unavailable

- ChatGPT subscription sign-in, catalogs and separately approved renewal remain
  experimental. Historical subscription media-validation failures are unresolved
  by the success of a different API endpoint. No automatic provider/billing switch.
- Docker is an explicit Mac-only option using an existing fixed local image; it
  is not a fallback. Windows Docker remains unavailable.
- MCP/Skills/hooks management and discovery do not authorize arbitrary desktop
  execution. Script hooks, external webhooks, deployment, migration and Git writes
  are not offered by this desktop release.
- No parallel autonomous workers, automatic patch application or unattended
  general-purpose software development guarantee.
- No Intel Mac, Windows ARM or Linux desktop package.

## Distribution

Versioned releases include installers, SHA-256 sums, clean Foundry source and
third-party corresponding-source/license archives. Mac has an ad-hoc integrity
signature, not Developer ID/notarization. Windows is unsigned. Stable scope does
not imply verified publisher status, commercial support or platform certification.
Do not disable operating-system security to run the app.

See the [desktop guide](desktop-guide.md), [safety model](safety.md),
[release gates](release-readiness.md) and [changelog](../../CHANGELOG.md).
