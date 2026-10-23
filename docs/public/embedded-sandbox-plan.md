# Built-in sandbox implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Deliver default built-in, hardware-virtualized Go testing on Mac and Windows, with explicit optional Docker, truthful evidence, native installers and documentation.

**Architecture:** Reuse existing controlled coding/approval/storage mechanisms. Introduce a versioned VM identity and a trusted `internal/vmruntime` process/guest protocol, then adapt that runtime to the existing durable C4 lifecycle without converting it to synthetic or Docker authority.

**Tech Stack:** Go 1.27.3, Swift/Virtualization.framework, QEMU/WHPX, Linux guest, Electron/Node 22.23.2, existing React workbench.

**Spec:** `docs/public/embedded-sandbox.md`.

## Global Constraints

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

- [ ] Add behavioral regressions preserving a serialized legacy profile digest; reject mixed Docker/VM fields, wrong platform pair, changed manifest, approval reuse and unknown source. Test expected identity equality, not merely `err != nil`.
- [ ] Run `go test ./internal/domain ./internal/engine ./internal/sandbox ./internal/httpapi ./cmd/foundry`, recording the expected failing assertions.
- [ ] Implement optional profile identity and a separate VM policy digest branch. Replace binary Docker/synthetic conditions with exhaustive backend/source checks. Freeze trusted startup VM identity into new tasks; reject client substitution.
- [ ] Adapt durable lifecycle to a backend-neutral internal interface, keeping Docker exported contracts and tests intact. VM dispatch requires verified runtime/profile equality, exact candidate admission and current approval; `Probe` and `Create/Start` cannot happen while metadata is read. Cleanup only uses an existing exact owned session; recovery without proof stays unconfirmed.
- [ ] Add positive protocol-injected runtime tests of readiness → baseline → approved candidate → test failure → repair → successful re-test and retained original evidence; assert unknown/zero/partial results never qualify.
- [ ] Run focused tests, then full Go/race suites once integrations compile; stage only this ownership area and report reviewable changes.

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

- [ ] Add table tests for traversal, symlink, missing/unlisted assets, bad platform/digest, inherited PATH/env injection, forged HMAC, mismatched nonce, duplicate/partial/oversized frames and bounded cancellation.
- [ ] Run `go test ./internal/vmruntime` before implementation and record failures.
- [ ] Implement strict regular-file manifest validation and private bounded snapshot serialization. Fixed helper commands only; no environment executable override. VM instance ID is random and receipts are private. Keep exact native process handles; no PID/name cleanup.
- [ ] Implement Mac helper with VZLinuxBootLoader + bundled initramfs, 1 CPU/2 GiB, serial-only transport, no network/disks/shares beyond immutable boot files. Use virtualization entitlement and parent EOF/deadline stop. Record validated startup/terminal facts rather than interpreting console messages as successful start.
- [ ] Implement Windows fixed QEMU `-accel whpx` (never TCG), no NIC/default devices/monitor, fixed kernel/initramfs and serial transport, no inherited handles. Resource-limited kill-on-close Job Object must be attached before guest starts. Stop for unsupported WHPX; never enable OS features.
- [ ] Test lifecycle with injected native-process/guest protocol seam, exact cleanup, interrupted/persistence recovery, strict argv and no fallback. Production provenance cannot be constructed from that mock seam.
- [ ] Run package tests and cross-compile Windows; report native helper compile requirements and complete source diff.

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
- [ ] Record red tests with `go test ./internal/c4verify ./internal/vmguest ./cmd/foundry-vm-guest` and `node --test desktop/test/*guest*.test.cjs`.
- [ ] Implement PID1 setup: proc/device permissions, private control console, tmpfs mounts, cgroup v2 limits, immutable root/workspace/toolchain, unprivileged child and no-new-privileges. Unsupported controls fail before tests. Only privileged supervisor retains frame secret; child descriptors/environment contain none. Native Linux syscalls are compile-tagged; no guest runner can be invoked as a host substitute.
- [ ] Run exact approved Go verifier in `/workspace`, collect complete Go JSON event evidence, HMAC the bounded result, and power down. Probe checks mandatory security controls without executing repository code. Parse all inputs strictly and reject files outside allowlisted ordinary relative paths.
- [ ] Build deterministic newc initramfs using input files with stable ownership, permissions and timestamps; fail on unexpected source archive members. Include signed/pinned kernel modules only when needed, never a mutable network boot URL. Test archive determinism, traversal and invalid input rejection.
- [ ] Run guest package tests and cross-compile Linux arm64/amd64 with `CGO_ENABLED=0`; report exact kernel/runtime input requirements for native acceptance.

Example verifier regression:
```go
r := RunForPlatform(context.Background(), fixtureProcess("go version go1.27.3 linux/amd64\n", passingEvents), "linux/amd64")
if r.Class != "passed" || r.Tests != 1 { t.Fatalf("unexpected verified result: %+v", r) }
```

### Task 4: Pinned runtime assets, desktop/UI and native acceptance

**Files:** `desktop/sandbox/assets*.cjs`, `desktop/sandbox/inputs.lock.json`, desktop build/runtime files, `.github/workflows/desktop.yml`, frontend coding-task/approval/report components and tests, `README.md`, `README.zh-CN.md`, public safety/desktop/live-coding docs, third-party notices. Private native evidence is outside the repo.

**Interfaces:** Consume strict VM bundle manifest and guest recipe. Bundle resources under `sandbox/` covered by desktop manifest and source inventory. Only display server-frozen identity; default selector is embedded VM, explicit Docker remains. Never fabricate readiness from installed metadata.

- [ ] Test missing/tampered sandbox resources, entitlement preservation, Windows binary dependency closure, selected-backend payload and truthful evidence labels. Record failing tests first.
- [ ] Pin official binary/source archives with recorded hashes, version and license provenance. Assess published vulnerabilities against included components/devices; block known applicable risks. Download/build only explicit required inputs, no Docker, account access or runtime model call.
- [ ] Package Mac signed helper and Windows QEMU/dependency closure plus kernel/initramfs. Verify all installed runtime files, signed helper entitlement and absence of unexpected executables/shared folders/network arguments. Include corresponding source archives required by licenses.
- [ ] Keep current workbench layout; render built-in runtime identity, prerequisites and approval boundaries. Baseline/candidate evidence names the true sandbox. Missing runtime is actionable blocked state, not synthetic pass.
- [ ] Run full Go/race/vet, frontend/desktop tests, npm audits, secret/vulnerability/workflow scans. Review the complete diff independently and resolve findings before packaging/push.
- [ ] On physical Mac install new DMG without losing existing data, boot exact installed guest and execute a disposable protected-test failure/repair/re-test flow using local model responses. Assert no protected changes, network/host access denied, timeout/cleanup bounded. Save private exact manifests and evidence.
- [ ] Push reviewed clean source and run native Windows EXE install/boot/test/cleanup acceptance using WHPX on a capable Windows runner. Do not skip, downgrade to TCG or call synthetic tests native evidence when unavailable. Inspect complete Verify and Desktop workflows for the exact commit.
- [ ] Update bilingual README to evidence-backed capabilities, unsigned-package status and explicit prerequisites. Publish no Release. Deliver local DMG/EXE artifacts and actual acceptance results, or a specific blocked native requirement with no false completion claim.

Example installed resource test:
```js
fs.writeFileSync(path.join(bundle, "sandbox", "kernel"), "tampered");
assert.throws(() => verifyResources(bundle, expectedManifestHash), /integrity/);
```

## Completion gate

Completion requires reviewed source, passing safety checks and CI, two installed packages, authentic installed native guest failure/repair/pass/cleanup evidence for both host platforms, and truthful bilingual README. A missing host capability or real-account authorization is stated precisely; neither is hidden by mocked success. Real account acceptance remains outside this authorization and does not prevent accountless sandbox verification.
