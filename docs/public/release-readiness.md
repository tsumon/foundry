# Desktop release readiness

FOUNDRY remains Alpha until the release candidate has evidence for the gates
below. A version bump, source test pass or successful installer build alone does
not satisfy these gates. Existing approval boundaries, consumed unknown calls
and immutable task records apply to release acceptance as well as development.

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
acceptance and artifact uploads must use that version. Keep the current Alpha
version until the remaining release gates are met.

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

The documented live attempt stopped at response media validation; real-model
end-to-end coding acceptance remains incomplete. New acceptance needs its own
explicit account and operation authority. It must demonstrate separate connection,
catalog and task approvals, baseline, bounded role calls, candidate tests,
confirmed-failure repair, independent reviews and explicit patch/report delivery.
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

Missing signing credentials, inaccessible release operations and unperformed
native or real-account acceptance remain release blockers. See
[Alpha status](alpha-status.md) for the current accepted scope.
