# Desktop release readiness

Released 0.1.0 is a bounded, serial Go coding workbench, not an
unattended general-purpose developer. Its primary connection is an explicitly
selected API Key / Responses route. Subscription sign-in stays experimental.
A version number or successful installer build alone does not satisfy the gates
below. Independent approvals, consumed unknown calls and immutable task records
apply to release acceptance as well as development.

<a id="candidate-020-gates"></a>

## 0.2.0 release gates

Version 0.2.0 adds only the [closed language/input contracts](desktop-guide.md#candidate-020-language-and-dependency-inputs). Mac component source `6e60138` passed Go v1/full twelve-case v2 native acceptance, fresh installation/current UI checks and both pinned physical upgrade paths. A separate six-call real TypeScript gateway delivery completed with an intentionally injected first-candidate fault and repair. [Evidence scope](alpha-status.md) preserves original attribution, including measured UI/restart reuse. Windows distribution still requires actual native hosted CI; this source text does not assert its future run result. Released 0.1.0 files and receipts remain fixed.

1. Finish whole-branch review and one consolidated fix wave before source publication. Keep exact package/web/lock/controller/User-Agent version metadata consistent; do not alter protocol/schema/profile identities. Run covering normal/race/vet/web/typecheck/build, desktop/source/export/license/secret/workflow and existing full dependency/security gates using pinned tools and explicit offline inputs. Preserve all original task records, evidence, approvals and consumed budgets.
2. Build final carrying-source packages after version and documentation changes. When the final diff is documentation-only, accepted component runtimes and complete source kits may retain their exact `6e60138` bytes/attribution only with measured executable/output/source equivalence and a release bridge; record build-timestamp/integrity metadata differences explicitly. This is not a new VM/provider pass. Fresh final package/resource/physical-install checks remain required. Keep one Go v1 `sandbox/` plus one shared three-profile `sandbox-v2/`, both complete source sidecars and notices outside Resources. Verify binary/resource inventories, canonical descriptor/image/supervisor/manifest digests, source linkage, `distribution-manifest.json` and final installer hashes. An amd64 rootfs alone cannot satisfy Windows native packaging.
3. Run the unchanged installed v1 acceptance and the closed v2 matrix below on supported native hardware, with independent repositories/dependencies/data and separate approvals. Every attempt owns a fresh data directory and receipt; unexpected failure stops without retry. Preserve its actual evidence before any source fix/rebuild. Catalog eligibility and protocol fixtures cannot substitute for actual guest boot/isolation/EOF/termination.
4. Test the exact candidate DMG/app on Apple Silicon: language/dependency/inspection UI, independent readiness/baseline/context/execution/delivery controls, actual failure evidence/report/patch, two launches and clean quits, old-runtime retention and data recovery. Use real installed-workbench screenshots with disposable data; screenshots alone prove neither guest nor provider execution. Compare original task/project/workbench inventories before/after.
5. Keep Alpha.3 (`5237fc49bafe4c5d33d3d6fa117ac59c467ace16`) upgrade coverage and supplement it with stable 0.1.0 (`ab89b5a88f73755e91aaefa5b73b1dfd5c73fdb0`). Both use closed source/installer identities and independent synthetic seeds. Stable's supplement tests upgrade/recovery/rollback/uninstall; the full candidate native/UI matrix runs once. Preserve strict old rows/SQL: Alpha.3 allows its six existing additive objects plus dependency_admissions; stable allows only dependency_admissions. Never ignore arbitrary schema differences or point an old program at current data.
6. After the consolidated source push, require actual exact-commit Verify, security and native Windows Desktop packages results. Windows must exercise installer/UI/restart/upgrade/uninstall, WHPX, all v2 profile/resource/stream cases and secure-I/O source regressions. Mac tests, cross-compilation and process-platform emulation cannot satisfy Windows execution. Record exact workflow/run/artifact/commit/hash evidence; no failed or unverified package may be published.
7. Keep provider proof separate. New language/context-dispatch real-provider acceptance needs its own explicitly selected receiver/model, independent sample task and bounded call authorization. Historical Mac Go CPA/Grok receipts remain scoped. Hosted Windows cannot reach the user's Mac loopback provider: its native tests use `local_protocol_fixture/not_real_provider`, and the Windows real-provider gap remains explicit. Do not tunnel the service, export credentials, substitute a paid API or claim fixture replies are real.
8. Publish only after applicable gates pass, with reviewed clean source, final installers, complete corresponding sources/notices, checksums and scoped receipts. Verify full asset downloads with normal TLS and SHA-256. Choose download links from actual published assets; do not invent run IDs or turn this plan into a release receipt. Signing stays ad-hoc Mac/non-notarized and unsigned Windows unless real new signing evidence exists.

### Versioned release assets

Publish exactly seven fixed assets under v0.2.0: `Foundry-0.2.0-arm64.dmg`, `Foundry-0.2.0-x64.exe`, `Foundry-0.2.0-source.tar.gz`, `Foundry-0.2.0-macos-sources.tar.gz`, `Foundry-0.2.0-windows-sources.tar.gz`, `Foundry-0.2.0-evidence.tar.gz` and `SHA256SUMS`. Each platform source archive must actually contain its original runtime/installer sources, both exact sandbox source trees and distribution linkage. Validate complete inventories before publishing. Exact final checksums, CI runs, package/component/carrying-source identities and acceptance/equivalence receipts belong in release evidence; planned names alone prove no result. Preserve all v0.1.0 assets.

### Installed native matrix and receipts

The legacy installed command is unchanged. V2 adds a closed profile/case pair, for example:

```text
<Resources>/bin/foundry-vm-acceptance --native-vm-acceptance --bundle <Resources>/sandbox-v2 --data-dir <fresh-private-directory> --profile c4_node_model_v2 --case workflow
```

The desktop wrapper verifies sealed installed resources. Use existing `FOUNDRY_INSTALLED_RESOURCES`, `FOUNDRY_VM_ACCEPTANCE_DATA` and `FOUNDRY_VM_ACCEPTANCE_REPORT`, then invoke `node desktop/native-vm-smoke.cjs --v2-matrix` once. Its default still runs v1; `--profile <closed-profile> --case <closed-case>` selects one explicit attempt. These commands are acceptance-only and never run during ordinary startup. They execute real VM tests and require the corresponding authority, not permission inferred from reading this page.

| Profile | Case | Required native outcome |
| --- | --- | --- |
| Node, TS, Python individually | `workflow` | Baseline pass/4 → assertion failure/4 → repaired pass/4 → sealed delivery; six fixture model calls per profile |
| TS | `typecheck` | Compiler-only infrastructure failure/0, owned adapter exit 2; no model/Debugger call |
| Python | `setup`, `discovery` individually | Infrastructure failure/0, adapter exit 1; no model/Debugger call |
| Python | `memory` | Infrastructure failure/0 with actual signaled phase/resource observation |
| Python | `pids` | Infrastructure failure/0, protected fork EAGAIN assertion and observed resource failure |
| Python | `timeout` | Timeout/0 with started/signaled phase and actual deadline observation |
| Python | `signal` | Infrastructure failure/0 with signal and clean resource counters |
| Python | `descendant` | Pass/1 with exact phase-group descendant cleanup |
| Python | `held_stream` | Infrastructure failure/0 despite direct parent exit 0; incomplete framework report and held descendant streams require bounded cleanup/EOF |

Each workflow uses admitted nonempty dependencies, including an actual TS declaration and Python pure-wheel import, with independent protected source/config/test hashes. It must establish actual read-only aliases, UID/groups/no-new-privileges, private report/control FD boundaries, no NIC/share, cgroups/page/inode and TS compiled-output freeze. Resource/stream boundaries use the representative Python profile instead of repeating every shared failure across all languages. Fixed production limits are not weakened for tests. Malformed/late serial frame tests remain explicitly offline unless native execution actually occurs; positive native handshakes and held-stream cleanup must exercise the real driver.

Every strict schema-2 receipt is at most 64 KiB and retains `v2` profile/case/provenance, runtime inventory/admission digests, complete input contract, dependency file hashes and expected/protected hashes. Operation `native_v2` facts bind the owned session/request/target and authenticated checks/phases/class/completed counts/exit/truncation. Unknown fields, mismatched counts/owner/target, missing cleanup or unobserved resource qualifications fail. Only twelve successful validations with one manifest identity produce the `foundry-native-vm-v2-matrix` aggregate. Model provenance remains `local_protocol_fixture/not_real_provider`. No receipt reconstructs live authority.

Windows artifact validation/upload includes the unchanged v1 receipt, all twelve v2 receipts and aggregate. Failure-only diagnostics are limited to the thirteen bounded attempt receipt filenames; no installer/source kit/data/environment/raw transcript is uploaded as a diagnostic workaround. Existing successful-distribution gates remain mandatory.

## Historical 0.1.0 design and implementation plan

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
a hardware VM. Complete the corresponding VM failure/repair/recheck and confirmed shutdown on Apple Silicon using the accepted component identities. A docs-only carrying-source package may reference those exact receipts only with measured runtime/executable/output equivalence and fresh package/install verification; it does not create a new VM pass. Changed runtime or executable bytes require affected native validation.

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
