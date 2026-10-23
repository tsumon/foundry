# Built-in sandbox implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Deliver default built-in, hardware-virtualized Go testing on Mac and Windows, with explicit optional Docker, truthful evidence, native installers and documentation.

**Architecture:** Reuse existing controlled coding/approval/storage mechanisms. Introduce a versioned VM identity and a trusted `internal/vmruntime` process/guest protocol, then adapt that runtime to the existing durable C4 lifecycle without converting it to synthetic or Docker authority.

**Tech Stack:** Go 1.27.3, Swift/Virtualization.framework, QEMU/WHPX, Linux guest, Electron/Node 22.23.2, existing React workbench.

**Spec:** `docs/public/embedded-sandbox.md`.

## Reconciled status — 2026-10-23

Implementation through `aebfd99` is reviewed: VM identity/lifecycle, native helpers, guest controls, desktop/UI integration, native acceptance harness and explicit public-source export are present. Review fixes include exact ownership, current-profile evidence binding, fresh guest cgroups, exact 16,384-inode scratch limits, backend-neutral new recovery evidence, and pinned Windows source/legal validation. A checked implementation step does not certify native Windows execution.

Local records include full Go/unit/race/vet verification, 791 frontend tests, 145 desktop tests, dependency/secret/workflow checks, and offline compilation of the clean no-`.git` export. Physical Mac evidence includes the installed bundle's independently approved baseline (two passes), Worker failure (one pass/one failure), repair (two passes), and exact cleanup, using local model-protocol fixtures only. Separate installed-bundle adverse checks observed read-only protection, both 16,384-inode limits, bounded oversized output, timeout and cancellation. These observations bind to sandbox manifest `b2dc784d22098d66b4a5772f354b6899d89df0895608165c060ee75fa22ad8d5`; earlier source-bundle results retain their original identities.

The first DMG's local upgrade was also observed: the previous alpha.2 app was retained, existing empty persistent task data remained empty (zero tasks before and after), and normal alpha.3 startup made no real model calls. This is not a populated-data migration test. Still pending: native Windows source build and measured DLL closure, installed EXE/WHPX acceptance, exact final-commit remote Verify/Desktop gates and artifact upload, and final bilingual README/artifact/acceptance mapping. A subsequent copy-only correction requires its own final-package identity and acceptance record; earlier evidence is not relabeled. No Release or real-provider acceptance is claimed. Private logs/receipts remain private and unchanged. Unchecked historical red-test steps below indicate missing literal preimplementation records, not missing implemented behavior; later green tests do not reconstruct those records.

## Global Constraints

### Windows portable build bootstrap correction — 2026-10-23

The first native Windows package job reached the pinned MSYS2 bootstrap but stopped before package installation: a portable extraction has no initialized pacman trust database. The fix verifies the bootstrap's exact `pacman-key`, GnuPG tools, packaged MSYS2 keyring/trust/revocation files and unchanged `pacman.conf`, then initializes and populates only that dedicated build root's keyring. It uses a non-login shell, no network key refresh, no signature-policy relaxation and no automatic retry. Local package installation follows only successful initialization. Regression checks cover command ordering, tampered/missing/nonregular trust inputs and fail-closed command errors. Native Windows build and installed WHPX acceptance must still pass in CI; local command-boundary tests are not native acceptance.

### Windows source-link extraction correction — 2026-10-23

The next native job passed the offline keyring/package setup and reached QEMU extraction. Its MSYS2 `bsdtar` uses MSYS2's default deep-copy symlink behavior, which fails when the archive lists a link before its target (and cannot represent dangling links). The pinned QEMU source contains both forward references and dangling links. Use the already-required Windows System32 native tar for this extraction, preserving links rather than copying their targets. Keep exact source digest and member-path validation, require a fresh destination, and propagate any listing/extraction error without fallback or retry. Add a real tiny archive regression with forward file/directory links and a dangling link, integrity/traversal/failure checks, and execute it in the native Windows gate before rebuilding. Do not ignore failed tar exits, extract twice, change Windows features, or claim that a Mac fixture proves Windows execution. Full native package/installed WHPX gates remain mandatory.

Windows native links additionally encode file/directory type, absent in POSIX tar. After extraction, reconcile directory-link types only against the already-validated directory member list, without following links or inspecting external targets. This acts only on links in the fresh private source tree, retains their target text, and stops on any filesystem error. No source file content or runtime sandbox permission is changed.

### Complete offline Python build tooling — 2026-10-23

Native Windows tests and full QEMU extraction now pass. Configure correctly remains offline and fails because its required Python tooling group lacks setuptools, wheel and pip. Add their exact official MSYS2 package bytes/digests plus pip's distlib dependency to the existing local build transaction; retain the already-pinned Python/packaging inputs. Verify official package metadata and dependency closure, add a transaction regression, then rerun native configure/build/install gates. These are build-only tools, not installed-app runtime dependencies. Do not enable PyPI access, disable QEMU tooling checks, alter signature verification, or relax the shipped DLL allowlist.

### Preserve native build failure diagnostics — 2026-10-23

The next native run passes Python tooling and reaches Meson setup, but its subprocess standard output is lost on failure. Preserve bounded stdout/stderr for configure and compilation, emit only a bounded control-character-stripped stdout tail to the CI log, and propagate the original failure without retry. Add success/failure/size-bound regressions before inspecting the next native result. Do not speculate about or weaken feature checks without concrete failure evidence.

### Complete QAPI configuration prerequisites — 2026-10-23

The retained native Meson output identifies a missing `diff` program in QEMU's QAPI schema test configuration. Add the exact official MSYS2 diffutils archive to the pinned local build transaction. Verify its digest/package metadata and dependency closure against the already-pinned bootstrap (libiconv, libintl and the bash-provided sh). Keep QAPI test configuration, all feature audits and the runtime DLL allowlist unchanged; diffutils is build-only and is not shipped in the application. Regression-test transaction inclusion, then repeat the native build/install gates.

- Built-in `embedded_vm` is the default for new tasks; Docker remains explicitly optional.
- No host execution, TCG, automatic backend/accelerator fallback, real credentials/provider calls, automatic retries or paid API switching.
- Existing UI/navigation and independent readiness/baseline/context/execute/delivery approvals remain.
- Legacy C4 JSON/digests and original Docker evidence remain compatible and unmodified.
- Tests are ordinary standard-library Go repositories; protected tests cannot be changed by Worker/Debugger.
- VM policy: one vCPU, 2 GiB guest RAM, 1 GiB/128 PID test cgroup, 180 s total, 120 s verifier, 64 KiB output, no NIC or host shares.
- Source/binary digests and third-party licenses are required; native installed guest acceptance is not replaced by mocked evidence.
- Preserve unrelated work and private evidence. Only reviewed public source is committed/pushed; no Release.

## Shared contracts

```go
// internal/domain, optional on C4Profile; old profiles marshal unchanged.
type C4VMIdentity struct {
    SchemaVersion int `json:"schema_version"`
    ManifestDigest string `json:"manifest_digest"`
    HostPlatform string `json:"host_platform"`
    GuestPlatform string `json:"guest_platform"`
    RuntimeDigest string `json:"runtime_digest"`
    KernelDigest string `json:"kernel_digest"`
    RootFSDigest string `json:"rootfs_digest"`
    VerifierDigest string `json:"verifier_digest"`
    Protocol string `json:"protocol"`
    PolicyVersion string `json:"policy_version"`
}
// internal/vmruntime: sealed trust from verified installed files, no VM effects.
func Load(bundleRoot, instanceRoot string) (*Runtime, error)
func (r *Runtime) Identity() domain.C4VMIdentity
func (r *Runtime) Probe(context.Context) error
func (r *Runtime) Create(context.Context, Request) (*Session, error)
func (s *Session) ID() string
func (s *Session) Start(context.Context) error
func (s *Session) Wait(context.Context) error
func (s *Session) Result() (c4verify.Result, bool)
func (s *Session) Running() bool
func (s *Session) Close(context.Context) error
type Request struct { OperationID, MissionID, Owner, Name, CandidatePath, CandidateDigest string }
// internal/sandbox: VM entry points, never synthetic wrappers.
func CheckC4VMReadiness(context.Context, domain.C4Profile, *vmruntime.Runtime) (C4ReadinessResult, error)
func ExecuteC4VM(context.Context, domain.C4Profile, domain.C4Operation, *vmruntime.Runtime, string, C4Persist) (domain.C4Operation, error)
func CleanupC4VM(context.Context, domain.C4Profile, domain.C4Operation, *vmruntime.Runtime, string, string, C4Persist) (domain.C4Operation, error)
// internal/c4verify: legacy Run remains arm64-compatible.
func RunForPlatform(context.Context, Process, string) Result
```

Runtime `Load` fails for unsupported host, malformed assets or mismatched files, but never checks hardware or starts a process. `Probe` requires the engine's already durable independent approval. Runtime construction is trusted composition, never JSON input. Runtime methods returning errors must not expose raw secrets or unbounded process output. Agent working on these interfaces reports necessary signature changes before editing dependent ownership areas.

### Task 1: Identity and controlled workflow integration

**Files:** `internal/domain/c4*.go`, `internal/engine/c4*.go`, `internal/engine/coding_runtime.go`, `internal/sandbox/c4*.go`, `cmd/foundry/coding_runtime.go`, `internal/httpapi/coding_tasks.go`; matching tests. Do not edit VM runtime or guest implementation.

**Interfaces:** Consume shared `vmruntime` contracts. Produce strict `C4VMIdentity`, VM profile/policy/source support, exact backend dispatch, trusted `NewCodingVMCapability`, and effect-free backend metadata. Add `FOUNDRY_VM_BUNDLE` trusted startup input and explicit Docker mode selection without changing existing fixture entry points.

- [x] Add behavioral regressions preserving a serialized legacy profile digest; reject mixed Docker/VM fields, wrong platform pair, changed manifest, approval reuse and unknown source. Test expected identity equality, not merely `err != nil`.
- [ ] Run `go test ./internal/domain ./internal/engine ./internal/sandbox ./internal/httpapi ./cmd/foundry`, recording the expected failing assertions.
- [x] Implement optional profile identity and a separate VM policy digest branch. Replace binary Docker/synthetic conditions with exhaustive backend/source checks. Freeze trusted startup VM identity into new tasks; reject client substitution.
- [x] Adapt durable lifecycle to a backend-neutral internal interface, keeping Docker exported contracts and tests intact. VM dispatch requires verified runtime/profile equality, exact candidate admission and current approval; `Probe` and `Create/Start` cannot happen while metadata is read. Cleanup only uses an existing exact owned session; recovery without proof stays unconfirmed.
- [ ] Add positive protocol-injected runtime tests of readiness → baseline → approved candidate → test failure → repair → successful re-test and retained original evidence; assert unknown/zero/partial results never qualify.
- [x] Run focused tests, then full Go/race suites once integrations compile; stage only this ownership area and report reviewable changes.

Reconciliation: `4d3d7b5` and `9de294a` implement the integration and metadata corrections; `420a145` adds the real native engine acceptance path. Focused behavioral reds are recorded, but not the literal broad preimplementation command above. The planned all-in-one injected runtime chain was deliberately replaced by private adapter/admission tests plus the actual native approval/failure/repair chain, avoiding a production mock-provenance constructor; the literal injected-chain checkbox remains open rather than claiming that test exists. Incomplete/unknown/zero/truncated evidence rejection is covered separately.

Example regression shape:
```go
before := legacy.ProfileDigest()
legacy.EmbeddedVM = nil
if got := legacy.ProfileDigest(); got != before { t.Fatal("legacy digest changed") }
p := validVMProfile()
p.DockerPath = "/usr/bin/docker"
if p.Validate() == nil { t.Fatal("mixed authority accepted") }
```

### Task 2: Trusted runtime, native helpers and durable session ownership

**Files:** create `internal/vmruntime/{manifest,protocol,runtime,process_darwin,process_windows,process_other}.go` and tests, `desktop/sandbox/macos/main.swift`, `desktop/sandbox/macos/entitlements.plist`. Own only these files; coordinate protocol definitions with Task 3.

**Interfaces:** Produce all shared Runtime/Session methods above. Manifest protocol contains request nonce/secret, action (`probe` or `verify`), guest platform, immutable file snapshot and digest; response contains nonce, policy/self-check facts, result and HMAC-SHA256. Header is `FOUNDRY_VM_V1`, bounded JSON encoding. Trusted guest signals `FOUNDRY_VM_READY_V1` before host transmits input. JSON frame decoding rejects extra/duplicate frames, oversize/missing/trailing data, wrong nonce/HMAC and unsupported schema/action. Never parse child logs as authority.

- [x] Add table tests for traversal, symlink, missing/unlisted assets, bad platform/digest, inherited PATH/env injection, forged HMAC, mismatched nonce, duplicate/partial/oversized frames and bounded cancellation.
- [ ] Run `go test ./internal/vmruntime` before implementation and record failures.
- [x] Implement strict regular-file manifest validation and private bounded snapshot serialization. Fixed helper commands only; no environment executable override. VM instance ID is random and receipts are private. Keep exact native process handles; no PID/name cleanup.
- [x] Implement Mac helper with VZLinuxBootLoader + bundled initramfs, 1 CPU/2 GiB, serial-only transport, no network/disks/shares beyond immutable boot files. Use virtualization entitlement and parent EOF/deadline stop. Record validated startup/terminal facts rather than interpreting console messages as successful start.
- [x] Implement Windows fixed QEMU `-accel whpx` (never TCG), no NIC/default devices/monitor, fixed kernel/initramfs and serial transport, no inherited handles. Resource-limited kill-on-close Job Object must be attached before guest starts. Stop for unsupported WHPX; never enable OS features.
- [x] Test lifecycle with injected native-process/guest protocol seam, exact cleanup, interrupted/persistence recovery, strict argv and no fallback. Production provenance cannot be constructed from that mock seam.
- [x] Run package tests and cross-compile Windows; report native helper compile requirements and complete source diff.

Reconciliation: `ef05e2d`, `b188fd6` and `ef9fc50` provide runtime, reviewed ownership repairs and bounded diagnostics. Initial missing-declaration failures and later behavioral red/green fixes are recorded, but the literal preimplementation package command is not fully documented. Windows test cross-compilation is complete; native Windows process/Job/WHPX behavior is not thereby verified.

Example protocol negative test:
```go
frame := validFrameFor(request)
frame.Nonce = "different-operation"
if _, err := decodeResponse(frameBytes(frame), request); err == nil { t.Fatal("replayed result accepted") }
```

### Task 3: Guest supervisor and reproducible guest image

**Files:** create `internal/vmguest/*`, `cmd/foundry-vm-guest/*`, `desktop/sandbox/build-guest.cjs` and matching tests; modify `internal/c4verify/verify.go` and platform tests. Do not edit domain, runtime, engine or desktop top-level packaging.

**Interfaces:** Consume Task 2 protocol definitions and produce source-built static `/init`, deterministic initramfs from pinned Linux/Go inputs. `c4verify.RunForPlatform(ctx,p,"linux/amd64")` checks exact Go version; legacy `Run` retains arm64 behavior. Build script takes already verified input paths; it never fetches or runs Docker.

- [ ] Add failing platform-version, guest request/path/duplicate/size validation, failure counts, child environment/UID, absent cgroup and result authentication tests.
- [x] Record red tests with `go test ./internal/c4verify ./internal/vmguest ./cmd/foundry-vm-guest` and `node --test desktop/test/*guest*.test.cjs`.
- [x] Implement PID1 setup: proc/device permissions, private control console, tmpfs mounts, cgroup v2 limits, immutable root/workspace/toolchain, unprivileged child and no-new-privileges. Unsupported controls fail before tests. Only privileged supervisor retains frame secret; child descriptors/environment contain none. Native Linux syscalls are compile-tagged; no guest runner can be invoked as a host substitute.
- [x] Run exact approved Go verifier in `/workspace`, collect complete Go JSON event evidence, HMAC the bounded result, and power down. Probe checks mandatory security controls without executing repository code. Parse all inputs strictly and reject files outside allowlisted ordinary relative paths.
- [x] Build deterministic newc initramfs using input files with stable ownership, permissions and timestamps; fail on unexpected source archive members. Include signed/pinned kernel modules only when needed, never a mutable network boot URL. Test archive determinism, traversal and invalid input rejection.
- [x] Run guest package tests and cross-compile Linux arm64/amd64 with `CGO_ENABLED=0`; report exact kernel/runtime input requirements for native acceptance.

Reconciliation: `1eb5dd2`, `acf5eac` and `f13ca6f` implement the guest, fresh per-phase cgroups and exact inode enforcement/observation. Both named initial red commands are recorded (missing declarations/files), with later behavioral red/green records; the first checkbox remains open because a pre-fix behavioral failure for every listed category is not established. Current regression coverage and native Mac observations do not retroactively supply those historical reds. Linux-only unit tests were cross-compiled, not executed on the Mac host.

Example verifier regression:
```go
r := RunForPlatform(context.Background(), fixtureProcess("go version go1.27.3 linux/amd64\n", passingEvents), "linux/amd64")
if r.Class != "passed" || r.Tests != 1 { t.Fatalf("unexpected verified result: %+v", r) }
```

### Task 4: Pinned runtime assets, desktop/UI and native acceptance

**Files:** `desktop/sandbox/assets*.cjs`, `desktop/sandbox/inputs.lock.json`, desktop build/runtime files, `.github/workflows/desktop.yml`, frontend coding-task/approval/report components and tests, `README.md`, `README.en.md`, public safety/desktop/live-coding docs, third-party notices. Private native evidence is outside the repo.

**Interfaces:** Consume strict VM bundle manifest and guest recipe. Bundle resources under `sandbox/` covered by desktop manifest and source inventory. Only display server-frozen identity; default selector is embedded VM, explicit Docker remains. Never fabricate readiness from installed metadata.

- [x] Test missing/tampered sandbox resources, entitlement preservation, Windows binary dependency closure, selected-backend payload and truthful evidence labels. Record failing tests first.
- [ ] Pin official binary/source archives with recorded hashes, version and license provenance. Assess published vulnerabilities against included components/devices; block known applicable risks. Download/build only explicit required inputs, no Docker, account access or runtime model call.
- [ ] Package Mac signed helper and Windows QEMU/dependency closure plus kernel/initramfs. Verify all installed runtime files, signed helper entitlement and absence of unexpected executables/shared folders/network arguments. Include corresponding source archives required by licenses.
- [x] Keep current workbench layout; render built-in runtime identity, prerequisites and approval boundaries. Baseline/candidate evidence names the true sandbox. Missing runtime is actionable blocked state, not synthetic pass.
- [ ] Run full Go/race/vet, frontend/desktop tests, npm audits, secret/vulnerability/workflow scans. Review the complete diff independently and resolve findings before packaging/push.
- [x] On physical Mac install new DMG without losing existing data, boot exact installed guest and execute a disposable protected-test failure/repair/re-test flow using local model responses. Assert no protected changes, network/host access denied, timeout/cleanup bounded. Save private exact manifests and evidence.
- [ ] Push reviewed clean source and run native Windows EXE install/boot/test/cleanup acceptance using WHPX on a capable Windows runner. Do not skip, downgrade to TCG or call synthetic tests native evidence when unavailable. Inspect complete Verify and Desktop workflows for the exact commit.
- [ ] Update bilingual README to evidence-backed capabilities, unsigned-package status and explicit prerequisites. Publish no Release. Deliver local DMG/EXE artifacts and actual acceptance results, or a specific blocked native requirement with no false completion claim.

Reconciliation: UI/desktop implementation and red/green coverage are recorded in `dc922fe`, `9de294a`, `66de7e9`, `31417de` and `e143f32`. Source/legal revalidation (`5cc9ee8`), fixed-source zlib (`92b2cb1`) and complete explicit export (`aebfd99`) passed scoped review. Pins and bounded vulnerability assessment are implemented; native Windows compilation/tests must still establish the patched runtime and actual dependency closure before distribution, so the combined pin/security and two-platform packaging steps remain open. Local suites/scans and independent reviews are recorded, but final-change verification and exact-commit remote gates are still outstanding. The Mac checkbox records the first installed package and the explicitly bounded upgrade observation above; it does not certify a later rebuilt artifact. README prerequisites are already updated; final results, artifact hashes and platform-specific blockers still need reconciliation.

Example installed resource test:
```js
fs.writeFileSync(path.join(bundle, "sandbox", "kernel"), "tampered");
assert.throws(() => verifyResources(bundle, expectedManifestHash), /integrity/);
```

## Completion gate

Run 39945298962 passed the native capability query, Windows boundary tests,
source build and installer build. Installed readiness failed after Job admission
with exit 1, confirmed termination and a rejected diagnostic. The current
classifier omits upstream kernel/initrd/ROM and event-loop startup messages;
it also conflates unrecognized output, pipe read failure and incomplete capture.
Before changing runtime behavior, add fixed startup categories and distinct
capture failure enums. Use pinned upstream message fixtures, ensure private
suffixes never escape, and cover real native child capture in the Windows gate.
No raw stderr, paths, relaxed evidence checks, extra handles, changed limits,
software fallback or real provider calls. Repeat full local checks and native
installation acceptance; the diagnostic change is not a runtime success claim.

Run 39943888763 failed before the WHPX query: the bounded marker remained
`started` while PowerShell Add-Type cold compilation exceeded 60 seconds. Do
not infer missing virtualization or keep increasing the timeout. Replace that
runtime compiler with a tiny CI-built native C probe, using the already-required
fixed GCC. Compile as a separate fail-closed step, then query once with a
15-second execution budget. Load WinHvPlatform.dll using System32-only lookup;
keep the exact HypervisorPresent query, numeric tuple and BOOL/written-size
checks. No partition creation, feature mutation, reboot or runtime fallback.
Offline compiled-C API-stub tests verify loader flags, query arguments, one-call
behavior and release; the native CI then queries the real system. Preserve all
QEMU, installed-guest, UI and artifact gates. References: Microsoft
[WHvGetCapability](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/funcs/whvgetcapability)
and [LoadLibraryExW](https://learn.microsoft.com/en-us/windows/win32/api/libloaderapi/nf-libloaderapi-loadlibraryexw).

Run 39939245820 confirms WHPX availability on its runner, but the installed
QEMU process still exits1 during readiness after native Job admission; termination
is confirmed. Windows currently discards stderr, so no runtime cause is inferred.
Capture that independent channel in the existing bounded diagnostic buffer,
drain it without blocking the guest serial channel, and discard raw bytes after
projecting only fixed diagnostic categories. Do not log text, paths or arguments.
Tie the categories to a confirmed nonzero process exit; they cannot establish
readiness, results or cleanup. Keep suspended creation, exact handle ownership,
Job limits, no host/TCG fallback and artifact gates unchanged. Regression covers
overflow, private text, malformed metadata, pipe closure and native Windows
child failure; run full local tests/review before the next native CI attempt.

Run 39938536363 stopped in the capability probe after its 15-second cold-start
budget, without a capability result. This is not evidence of absent WHPX.
Retain the fail-closed gate; add bounded ordered startup/compiled/queried markers
and a typed timeout/nonzero/launch failure, never raw stderr. Give this read-only
cold PowerShell/Add-Type diagnostic a 60-second total budget with no retry.
Offline tests must reject mixed/private/out-of-order progress and demonstrate
that partial success output cannot authorize execution. Native CI then resolves
the actual stage and capability; no OS feature changes are authorized.

Run 39934807578 now identifies a readiness failure after native Job admission:
QEMU exited 1 and termination was confirmed. Before changing runtime behavior,
add a one-shot read-only Windows Hypervisor Platform capability preflight using
the system DLL's WHvGetCapability(HypervisorPresent). Report only bounded
status/numeric HRESULT facts; no feature installation, reboot, partition creation,
account access or fallback. Run it before the expensive Windows build. Missing
capability fails the job, never skips/adopts the native guest acceptance gate.
Offline tests cover absent API/hypervisor, failed/truncated/malformed replies,
timeout and no retries; native CI establishes this host's actual capability.
Reference: [Microsoft WHvGetCapability](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/funcs/whvgetcapability).

Native Windows run 39930444811 built and installed the EXE, but failed the
mandatory VM gate. The acceptance wrapper discarded the validated failure
stage from CI output; private receipts were not uploaded by the success-only
artifact gate. Add a failure-only, bounded diagnostic projection: acceptance
stage/code plus exact-run instance terminal facts (fixed enums, booleans and
numeric exit/admission codes). Read only regular, bounded files under the fresh
acceptance root, reject links and mismatched instance/manifest identities, and
never emit raw subprocess output, paths, ownership strings, secrets or tokens.
First reproduce the missing CI diagnostic and hostile-file rejection offline.
Keep nonzero exits and every native/installation/artifact gate unchanged. A
diagnostic is not readiness or cleanup authority. Re-run native CI to locate
the actual failing layer before changing execution behavior.

Native Windows run 39927465919 passed QEMU compilation and its actual minimal
DLL/source checks, then stopped while extracting the pinned Linux recipe
inventory. Its native tar text listings use CRLF; splitting only on LF retains
a carriage return in member names and in the subtree directory entry. Accept
LF or CRLF record delimiters for tar listings only. Preserve member bytes,
the fixed subtree, required files, count/duplicate/name and regular-file checks.
First reproduce the same failure using a real archive and CRLF listing output,
then verify exact extracted bytes and malformed/duplicate/missing-file rejection.
No archive retry, path relaxation, dropped source materials or runtime fallback
is allowed. Native Windows packaging and installed WHPX gates must run again.

Native Windows run 39925686067 completed configure, then exposed an incorrect
generated-header selection: QEMU emits CONFIG_WHPX in the architecture-specific
`x86_64-softmmu-config-target.h`, not `config-host.h` (pinned meson.build lines
3320–3389). Record both exact headers in the build proof, require WHPX in the
x86_64 target header, and reject TCG in either header. Revalidate that same proof
during bundle assembly; do not substitute configure flags for generated facts.
Add source-derived positive/negative regressions and an exact-file loading test
before implementation. Native compile and installed WHPX acceptance remain gates.

Windows generated-feature audit follow-up: QEMU 11.1.2 implements
`--without-default-features` using Meson's `auto_features=disabled`, while
Meson 1.12.1 introspection retains raw feature defaults (for example `attr=auto`).
Add a regression for this source-derived case, then require the unique core
`auto_features` option to be explicitly disabled before resolving root-project
user feature `auto` as effectively disabled. Explicitly forbidden features
must still be explicitly disabled; explicit enabled, unknown, and subproject
auto features remain blocked. Keep compiled WHPX/TCG, device and DLL-closure
checks unchanged. Re-run local regressions, independent review and native CI;
this correction alone is not Windows installation acceptance.
References: [Meson feature semantics](https://mesonbuild.com/Build-options.html#features)
and [Meson 1.12.1 introspection](https://github.com/mesonbuild/meson/blob/1.12.1/mesonbuild/mintro.py).

Windows run 39949492239 completed the EXE build but the installed readiness gate
still failed with complete, unrecognized stderr and exit 1. The exit is ambiguous:
the controller terminates its owned Job with code 1 after rejecting the serial
readiness marker. Do not infer an autonomous QEMU crash or change runtime flags.
Record the controller's exact bounded readiness outcome (matched, EOF, short
read, read failure, deadline, or full marker mismatch), a fixed known-prefix
classification only for mismatches, and whether native termination was requested.
Never record marker bytes, raw stderr, paths, tokens, or arbitrary messages.
Keep the exact readiness marker and all rejection/cleanup rules unchanged.
First reproduce EOF, partial/foreign marker, CRLF and owner-stop cases offline;
verify Go and Node projections reject unknown fields/values and cannot authorize
success. Re-run full verification, independent review and native CI. Local
inspection of the pinned source disproved the HPET/VMPORT omission hypothesis:
their runtime defaults are compile-guarded; no speculative device flag fix.
Mac 86d557b installed contracts, physical VM repair loop and native GUI/restart
passed separately; that does not certify Windows or real model accounts.

Completion requires reviewed source, passing safety checks and CI, two installed packages, authentic installed native guest failure/repair/pass/cleanup evidence for both host platforms, and truthful bilingual README. A missing host capability or real-account authorization is stated precisely; neither is hidden by mocked success. Real account acceptance remains outside this authorization and does not prevent accountless sandbox verification.

### Windows early-start dependency diagnosis — 2026-10-23

Run 39954907497 confirms zero-byte readiness EOF, complete unrecognized stderr,
and exact process termination. This disproves the clock-warning/marker-prefix
hypothesis for that run; do not patch QEMU serial behavior or relax readiness.
Pinned QEMU initializes its crypto provider before parsing options. Its Windows
path uses CryptAcquireContext with null container/provider and only
CRYPT_VERIFYCONTEXT | CRYPT_SILENT; the current explicit child environment omits
SYSTEMROOT, which Go's ordinary exec launcher treats as critical. This is a
testable hypothesis, not a confirmed startup cause.

First extend the failure-only classifier with a fixed crypto-initialization
category and private-text regressions. In native Windows boundary tests, launch
a fixed test helper through the actual suspended/Job/handle-owning launcher,
once with the current environment and once with only the authoritative system
Windows directory added as SYSTEMROOT. The helper calls that same ephemeral
provider API, releases its handle, and reports only success and numeric error
facts. It never opens a named key container, accesses accounts, creates keys,
changes host environment/features, boots a VM, or changes production launch
behavior. Require complete bounded results and confirmed exact child termination.
Publish only these fixed diagnostic facts, not environment values or raw errors.
Use the native comparison and the real installed readiness result to establish
whether a production correction is justified. Keep all existing acceptance gates.

### Preserve binary requests in the Windows stdio transport — 2026-10-23

Independent source review found a second, post-readiness defect: QEMU 11.1.2's
non-console `win_stdio_thread` discards every carriage-return byte. Foundry's
four-byte big-endian frame length is binary; a valid 525-byte verify request has
length `00 00 02 0d` and is corrupted. This does not explain the earlier EOF.
Keep protocol v1 and all checks unchanged. Reproduce byte loss by compiling the
actual pinned upstream thread function with controlled ReadFile/event boundaries,
then remove its terminal-emulator CR filter for pipe input. The same source also
acknowledges a byte when the UART cannot receive; retain a BQL-protected pending
byte and deliver/acknowledge it only when the frontend readiness callback permits.
The pipe reader never accesses this flag. No polling, retrying execution or extra buffering.
Test all 256 byte values, repeated CR and blocked-then-ready/exact-once delivery;
reject changed source and repeat application, and pin both before/after
file hashes. Run the same regression with native Windows GCC before building QEMU.
Apply the patch only in the fresh verified source tree before compilation, record
its identity in the build proof, and require the identity again during bundle
assembly. Ship the local patch recipe alongside the already-pinned upstream source
archive and licenses. Never change the console handler, frame parser, byte bounds,
network policy, Job containment, independent approvals or success-only EXE gate.

### Confirmed Windows crypto environment correction — 2026-10-23

Run 39960225908 established the startup cause: the actual owned Windows helper
failed ephemeral CryptAcquireContext without SYSTEMROOT (2148073501), but acquired
and released the provider with only that variable added. Installed QEMU reported
the fixed crypto_initialization category and readiness EOF. Add only SYSTEMROOT
from GetSystemWindowsDirectory, not the parent environment, before CreateProcess.
Reject missing/invalid OS directory, NUL injection, case-insensitive duplicate
names and caller attempts to override SYSTEMROOT. Encode a sorted, double-NUL
terminated UTF-16 environment without inheriting PATH, user or account variables.
Turn the native diagnostic into a regression requiring acquisition/release and
the OS directory despite poisoned parent environment. Test malformed inputs and
Unicode encoding offline. Keep suspended admission, Job/handle ownership and all
execution restrictions unchanged. Then re-run installed Windows acceptance; the
crypto test alone cannot certify WHPX boot, repair, UI, restart or uninstall.

### Unicode installation path / firmware diagnosis — 2026-10-23

Run 39964561670 passed the corrected native crypto regression, but installed
readiness stopped with the fixed firmware category. The verified bundle contains
both required firmware files. Upstream QEMU's ordinary main receives narrow argv;
its firmware search uses narrow access/open, while the installed test deliberately
uses Chinese paths. Its version resource has no UTF-8 process manifest. This is
a path-encoding hypothesis pending the native differential test, not proof that
WHPX or firmware has passed. Retain the Unicode install case and exact readiness.

Add a build-only UCRT regression using the actual pinned compiler: launch identical
narrow-argv/access/fopen probes with and without a process UTF-8 manifest against
an owned Chinese/emoji file. Record only numeric code page and fixed booleans.
Require complete reads and arguments in the manifested probe, and inspect the
actual built QEMU's RT_MANIFEST resource. Do not change Windows system locale,
feature settings, host permissions, approved environment or paths to ASCII.
The per-process mechanism is documented by Microsoft:
https://learn.microsoft.com/en-us/windows/apps/design/globalizing/use-utf8-code-page

Pin the original and patched version.rc plus manifest hashes, apply only to fresh
verified QEMU source, bind the patch and native regression receipt into assembly,
and ship the recipe, probe and upstream source/license sidecars. Test rejection
of drift, links, repeat application, missing native facts and failed reads before
building. Then run unchanged installed WHPX baseline/failure/repair, native UI,
restart and uninstall gates. No TCG, host repository execution or real accounts.

### Installed Windows explanation contract — 2026-10-23

Run 39970118634 confirms the Unicode diagnosis: the same pinned UCRT probe lost
argv and could not read the Chinese/emoji file at code page 1252; with the process
manifest, code page 65001 preserved argv/access/read and the actual QEMU manifest.
The installed WHPX guest gate and backend contracts then passed. Acceptance next
stopped at the coding confirmation: the driver still expected “This enables
configuration only” and “separate approvals”, while the unchanged application
shows “This opens configuration only” and “require their own approvals”.

Keep application UI and consent unchanged. Share the fixed explanation predicate
between installed acceptance and the existing native Electron TaskDialog fixture,
checking the correct configuration-only, no-VM, no-Docker, no-model and independent
approval statements in the exact owned dialog before clicking. Add a regression
against actual createCodingMenuItem output and native missing-fragment/wrong-owner
negative cases so this fails before the expensive runtime build. Do not remove the
explanation gate or substitute button presence. Repeat all existing release gates.
