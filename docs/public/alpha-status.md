# Supported scope — 0.2.0 and historical 0.1.0

## 0.2.0

The bounded serial workflow supports standard-library Go v1 plus single-package JavaScript `node:test`, fixed TypeScript typecheck then `node:test`, and Python `unittest`. New profiles require the bundled VM and explicit immutable local input: frozen pure-JS npm lock-v3 dependencies or exact locked pure wheels. Tests, helpers, manifests, locks, TS config and dependency payload remain protected. No arbitrary package acquisition, native extensions, plugins, monorepos, other frameworks or guest installers are offered. See the [input walkthrough](desktop-guide.md#candidate-020-language-and-dependency-inputs).

Catalog eligibility is not a readiness check, test result or native attestation. Compile/setup/discovery and zero-case infrastructure errors cannot become passing tests or repair evidence. Existing independent approvals, retained runtime identities and no-host-fallback rules remain.

### Scoped Mac evidence

Executable/runtime source `6e60138` passed fresh Go v1 and the complete twelve-case v2 native matrix. Each Node/TS/Python workflow ran four real tests through baseline pass, intentional first-candidate failure and repair pass, using `local_protocol_fixture/not_real_provider` model replies. The additional compiler, setup/discovery, resource, signal, descendant and held-stream checks qualified their exact expected outcomes and cleanup. Real VM evidence is separate from model provenance.

Fresh installation/window/catalog/retained-history and physical Alpha.3 plus stable 0.1.0 eight-step upgrade/recovery/rollback/uninstall passed. UI/history/restart behavioral reuse remains attributed to `72ed8dd` with measured equivalence, alongside fresh current-package checks; it is not a new restart run. Final release receipts distinguish the carrying source commit, accepted component source and any documentation-only repackaging by exact inventories/hashes. A new package timestamp is not a new native/provider pass.

A separate real-gateway TypeScript task used Magpie “CPA Grok 本地”, requested `grok-4.6`; upstream model identity was not independently verified. Researcher, Planner, Worker, Debugger, QA and Auditor each completed one real call (six of a seven-call ceiling). With a typed dependency, four real VM tests passed baseline, detected an explicitly injected first-candidate fault, then passed after Debugger repair. Source, protected inputs and dependency input stayed unchanged; all native operations closed and patch/report were sealed. This proves that route/project/host scenario, not Node/Python real-provider or Windows access. Historical Go and OpenAI subscription receipts keep their original meaning.

### Windows and distribution contract

Windows distribution requires actual native hosted installer/UI/restart/upgrade/uninstall and WHPX checks for the exact release, with fixture-model replies. Consult the [v0.2.0 release receipts](https://github.com/tsumon/foundry/releases/tag/v0.2.0) for actual runs and final package hashes; no future CI success is inferred here. Hosted Windows cannot reach a Mac loopback CPA service. Native fixture workflows do not establish Windows real-provider access, and no tunnel, key export or provider substitution is implied.

The seven release assets are DMG, EXE, clean Foundry source, macOS sources, Windows sources, scoped evidence and SHA256SUMS. Each platform source archive must include its original runtime/installer sources, both exact sandbox source trees and distribution linkage. Mac stays ad-hoc integrity signed without Developer ID/notarization; Windows stays unsigned. Versioned links and checksums do not authenticate a publisher or certify production safety. [Mandatory release gates](release-readiness.md#candidate-020-gates)

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
