# Desktop release readiness

The first stable release is a bounded, serial Go coding workbench, not an
unattended general-purpose developer. Its primary connection is an explicitly
selected API Key / Responses route. Subscription sign-in stays experimental.
A version number or successful installer build alone does not satisfy the gates
below. Independent approvals, consumed unknown calls and immutable task records
apply to release acceptance as well as development.

## 0.1.0 design and implementation plan

The existing controller, role router, native credential store and isolated VM
remain the architecture. Do not replace them or widen execution capabilities.
The supported outcome is a reviewed local patch and report for committed,
standard-library Go repositories on Apple Silicon Mac or Windows x64.

1. Keep development in one workspace with `source`, `build`, `evidence`,
   `releases` and an archive. Preserve original task data and evidence. Remove
   only identified regenerable caches, not task databases, retained runtimes,
   protected inputs, private history or unknown user files.
2. Update the desktop package and root lock metadata to `0.1.0`. Correct the
   bilingual README, desktop guide, status and changelog to describe the API
   route and actual supported scope. Preserve historical evidence as historical.
3. Run full source/security checks, build the exact clean candidate, and run
   native installation and upgrade checks with independent disposable data.
   Use the previous public Alpha as the supported upgrade source. Verify
   budgets, baselines, retained runtimes, restart and failure recovery.
4. Run one separate, bounded CPA/Grok repair acceptance using the same explicit
   receiver/model and a seven-call maximum. Use a transparent fault-injection
   exercise: the first Worker candidate intentionally violates a protected test;
   a real VM must detect it and the real Debugger must repair it. Record this as
   injected failure, not a spontaneous model mistake. Keep protected tests and
   the original repository unchanged; preserve every consumed call and stop on
   an uncertain result. No automatic retries or provider fallback.
5. Check the candidate on native Mac and Windows. Review the exact diff and
   evidence, fix failures with regression coverage, push and require complete
   successful Verify and Desktop packages runs. Do not substitute an old pass
   for changed runtime bytes.
6. Publish `v0.1.0` only after the applicable gates pass. Include accepted DMG
   and EXE files, source/license sidecars, hashes, a clean source snapshot and
   scoped acceptance evidence. Validate full asset downloads. Signing status
   must be visible in the release and installation instructions.

## Source and dependency gates

Run `./scripts/verify.sh` with the exact checked-in Go, Node.js and npm pins,
cgo and the trusted system Git capability. It must complete exporter tests,
normal and race-enabled Go tests, vet, frontend tests and the production build.
Run `npm ci --prefix desktop` and `npm test --prefix desktop` for desktop source
contracts. Linux CI runs these contracts using explicit inert native-host
fixtures; this does not certify installation or VM execution on Linux.

The security job must complete its redacted source/history scans, workflow
validation, Go vulnerability check and both full npm audits at the existing low
threshold. Record the vulnerability database version or timestamp. If a live
database is inaccessible, identify any official snapshot used and report its
age; a snapshot scan does not establish that a fresh live scan passed.

Installer names come from `desktop/package.json`. Both package builders, native
acceptance and artifact uploads must use that version. Preparing a versioned
candidate does not itself publish it or certify its acceptance.

## Exact-candidate native gates

For the candidate's exact commit, require successful **Verify** and both native
**Desktop packages** jobs, along with retained installer hashes, build inputs,
runtime/source sidecars and installed-acceptance evidence. A source-only Linux
job is additional coverage, not a substitute for either native job.

Require Windows acceptance to cover installed UI, protected persistent data,
restart, uninstall and real WHPX failure/repair/recheck. The macOS CI
job verifies installation and backend contracts but explicitly does not execute
a hardware VM. Complete the corresponding VM failure/repair/recheck and confirmed
shutdown on an Apple Silicon Mac using the exact candidate's installed resources.
Historical VM evidence cannot be carried forward to changed runtime bytes.

Exercise fresh installation and upgrade from the supported previous build,
including old tasks selecting their exact retained runtime, blocked tampered or
missing assets, unchanged consumed budgets, and no automatic replay or provider
effects on restart. Use disposable data and preserve the resulting scoped
evidence. Never substitute a real user's existing task or credentials.

## Real-account workflow gate

The Mac compatible-API path has completed real code delivery and a separately
labeled injected-failure repair/recheck; see
[its scoped acceptance record](compatible-api-acceptance.md). The earlier OpenAI
subscription attempt stopped at media validation and is not a successful route.
New acceptance needs its own explicit account and operation authority. API Key
setup must not perform OAuth or require a subscription catalog receipt. It must
demonstrate explicit route/account/context consent, baseline, bounded role calls,
candidate tests, confirmed-failure repair, independent reviews and explicit
patch/report delivery. Subscription connection and catalog permissions remain
separate whenever that experimental route is used.
Record known failures, unknown outcomes, usage and consumed budgets honestly.
Unknown calls must never be replayed to obtain a passing run. Run separately on
each host for which real-account compatibility will be claimed.

See [response diagnostics](model-response-diagnostics.md),
[session renewal](session-renewal.md) and
[task authorization continuation](task-authorization-continuation.md).

## Distribution and support gates

The current Mac app uses an ad-hoc integrity signature and the Windows installer
is unsigned. Before claiming a production-signed release, configure the owner's
production signing identities, verify the signed Windows installer and verify
Developer ID signing, notarization and stapling on the final Mac package.
Recompute distribution hashes after signing; unsigned or ad-hoc build hashes
cannot attest to different final bytes.

Provide a durable release channel that retains the exact accepted installers,
SHA-256 sums, release notes and complete required corresponding-source and
license sidecars. Expiring CI artifacts alone do not provide this channel.
Describe the supported host versions, known limits, upgrade/data-retention
behavior and security-reporting route. Update the bilingual guides and Alpha
status only to the extent supported by the candidate's evidence.

Production-signed distribution requires the owner's certificates; absent
identities must never be replaced by borrowed identities or a false signing
claim. A clearly labelled unsigned open-source release may be published with
ad-hoc Mac integrity signing and an unsigned Windows installer. This is not
Developer ID notarization, verified publisher status, or a reason to disable
operating-system security protections. Inaccessible release operations and
unperformed applicable native or real-account acceptance still block publication.
See [supported scope](alpha-status.md) for the accepted boundary.
