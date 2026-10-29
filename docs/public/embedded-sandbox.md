# Built-in sandbox design

Status: scoped Mac Go v1 and all twelve v2 native cases passed on executable/runtime source `6e60138`. Windows distribution requires its own actual native hosted checks. Exact release receipts, not this design description, identify accepted component/package bytes.

Foundry defaults new coding tasks to an application-bundled, disposable Linux VM. Docker remains an explicitly selected Mac Go alternative; the new v2 language profiles are VM-only. The workbench, repository import, protected tests, candidate generations, model context approval, plan/execute approval, and final delivery approval remain unchanged. Existing Docker and synthetic tasks retain their original backend and evidence.

## Supported execution boundary

| Host | Built-in runtime | Guest | Requirement |
| --- | --- | --- | --- |
| macOS Apple Silicon | signed native helper using Virtualization.framework | Linux arm64 | hardware virtualization and the helper virtualization entitlement |
| Windows x64 | bundled QEMU, WHPX accelerator only | Linux amd64 | Windows Hypervisor Platform and hardware virtualization already available |

There is no host-shell runner, TCG fallback, automatic Docker fallback, automatic Windows feature installation, or automatic reboot. Missing assets, unavailable virtualization, a changed manifest, and unconfirmed cleanup block execution. A Mac user may explicitly select Docker for a new Go task; doing so creates different approval material. The VM is a security boundary, not a claim of immunity to hypervisor or guest-kernel vulnerabilities.

The first built-in runtime supports the same bounded, committed, standard-library Go repositories as the existing controlled coding workflow. It does not claim arbitrary language/package installation, browser testing, native macOS/Windows application tests, or a general-purpose terminal. Model transport remains separately authorized; using a local test model does not turn VM evidence into model-provider evidence.

## 0.2.0 language boundary

Version 0.2.0 adds Node 24.22.1 `node:test`, TypeScript 5.9.3 fixed typecheck then `node:test`, and Python 3.13.5 `unittest` beside existing Go v1. Only one supported single-package project/framework is admitted. Pre-existing pure-JS npm lock-v3 trees or exact locked pure wheels enter through explicit local-copy consent; the guest has no package manager, registry access or installation scripts. Native extensions, project plugins, monorepos and other frameworks are unsupported. Follow the [frozen input contract](desktop-guide.md#candidate-020-language-and-dependency-inputs).

Source inspection binds all tests/support, manifests, locks, TS config and dependency/admission digests to the exact installed runtime inventory. TS fixes the complete sorted `.ts` input list, checks types (including dependency declarations), then runs frozen emitted tests. Python runs unittest with isolated interpreter options. Compile/setup/discovery errors, empty results and incomplete streams are infrastructure failure, never a passing test or repair evidence. [Mac native and scoped real TypeScript gateway results](alpha-status.md) are recorded separately. Final package/source linkage and actual platform CI remain required before publication.

## Identity and compatibility

Go v1 profiles use `mode: embedded_vm`, `id: c4_go_model_v1`, and an optional `embedded_vm` identity. Its fields are `schema_version`, `manifest_digest`, `host_platform`, `guest_platform`, `runtime_digest`, `kernel_digest`, `rootfs_digest`, `verifier_digest`, `protocol`, and `policy_version`. Digests are SHA-256 hex. Protocol is `foundry-vm-v1`; policy is `go-vm-v1`. Host/guest pairs are exactly `darwin/arm64` → `linux/arm64` and `windows/amd64` → `linux/amd64`.

VM profiles have empty Docker path, endpoint and image ID; mixed profiles are rejected. The startup-owned, verified bundle supplies this identity, never a browser request or model response. Existing C4 v1 JSON, policy and digest computation remain byte-compatible when `embedded_vm` is absent. A distinct VM policy participates in new profile digests. Changing runtime, kernel, guest, verifier, backend or policy invalidates approval material; old tasks are never silently upgraded.

`embedded_vm_c4_control` is distinct from `docker_c4_control` and `synthetic_c4_control`. The historical `container_id` and `container_name` JSON fields remain compatibility keys for an exact VM instance handle/name; UI/report labels describe the actual backend. A VM handle is a cryptographically random 64-hex identifier, never a PID or a guessed process name.

V2 profiles are `c4_node_model_v2`, `c4_typescript_model_v2` and `c4_python_model_v2`, with schema-2 identity, `foundry-vm-v2` protocol and `multilang-vm-v2` policy. One separate `sandbox-v2` image carries all three runners. Its canonical runtime inventory binds the supervisor, interpreters/compiler and runner contracts. The full task additionally freezes dependency, protected-input and execution-contract digests. Installed catalog rows are templates, not full executable task profiles or successful readiness attestations. Retained receipt/runtime relations, rather than the current catalog, validate an old task; missing or altered retained assets block it.

## Bundle and supply chain

The application resource manifest covers every helper, runtime executable/DLL, kernel, initramfs and legal/source inventory. The VM bundle has its own strict, bounded manifest with relative regular-file paths, SHA-256 digests and exact platform/protocol/policy. Symlinks, unlisted executable files, traversal, changed binaries and inherited executable search paths are rejected. Asset verification is not a readiness probe and does not launch a VM.

macOS uses Apple's system framework rather than a separately installed daemon. Windows uses a pinned QEMU release and only its required audited binary dependency closure; it does not require MSYS2 on the user's machine. Only WHPX is admitted. Upstream explicitly excludes TCG from its supported virtualization security boundary, so software emulation cannot be used to make a native acceptance gate pass.

Guest inputs are pinned Linux kernel and Go 1.27.3 archives, the source-built static Foundry guest supervisor/verifier, and a deterministic initramfs recipe. Acquisition uses official project/distribution sources, fixed hashes and bounded downloads. Builds do not fetch dependencies from inside guests. Third-party licenses and corresponding source materials accompany distributed GPL components. Known applicable security issues block distribution; source URLs alone do not substitute for required corresponding source delivery.

Windows builds QEMU 11.1.2 from pinned source in a dedicated, pinned MSYS2 build environment. The build disables TCG, optional devices, GUI, network/share integrations, audio and plugins, then checks generated configuration and the actual PE dependency closure. The general-purpose QEMU binary package is not distributed. Q35's compiled AHCI support is not instantiated: the fixed host command uses `sata=off,usb=off`. A Linux kernel/source sidecar and, on Windows, QEMU/library corresponding sources accompany the installer. Each packaged file is hash-bound; no installed app performs these downloads or builds.

Windows runtime zlib is a Foundry source-built backport: zlib 1.3.2 plus upstream commit `df84af25dc1942490e1d1c899a07619152a46148` for CVE-2026-85091, retaining the pinned MSYS2 large-file ABI patch. The old MSYS2 zlib package is build-only and cannot supply the distributed DLL. Packaging requires native zlib example/gzip/ABI/regression tests, matching exports and a measured DLL receipt; the source sidecar contains the exact source, patches and build recipe. These gates do not imply that a Windows build or WHPX acceptance has already passed.

## Guest and process restrictions

The runtime has no network adapter, host filesystem share, USB, clipboard, Docker socket or inherited credentials. Repository input is a bounded ordinary-file snapshot transferred over a private serial protocol, not a writable host mount. Only approved candidate bytes enter the VM. The guest supervisor creates a root-owned read-only workspace; protected tests are checked before execution and candidate digest is bound to material.

The VM has one vCPU and 2 GiB RAM. The test cgroup is bounded to 1 GiB memory, 128 processes and one CPU; swap is unavailable. Test execution is UID/GID 65534 with no new privileges. The guest uses read-only toolchain/workspace, a 64 MiB noexec temporary filesystem and 256 MiB build scratch. Host runtime lifetime is at most 180 seconds; test verification is at most 120 seconds, including `go test -json -count=1 -timeout=90s ./...`. Retained output is at most 64 KiB. Go dependency acquisition, toolchain switching, workspace/module overrides and network are disabled. V2 admits only the pre-imported read-only dependency bundle. No project install scripts or model-provided commands run; protected tests and helpers execute only through the fixed owned runner.

The privileged guest supervisor owns the serial channel and launches unprivileged fixed verifier children with explicit environment and file descriptors. Each verifier phase has a fresh, exclusive cgroup with the same limits read back before admission; a previously killed group is never reused. This avoids a pinned-kernel `CLONE_INTO_CGROUP`/`cgroup.kill` sequence-counter defect without removing placement-at-creation or descendant cleanup. Child stdout/stderr are data, not control frames. A per-operation secret and nonce authenticate the final bounded result frame; neither is exposed to the test environment, command line, files or inherited descriptors. This prevents a test's printed JSON from forging a host result; it does not attest a compromised guest kernel. `/proc` and device permissions prevent unprivileged tests reading supervisor state or writing the console. Startup verifies required mounts, cgroup limits and permissions; any missing control fails closed.

New runtime builds select `console=null` on both platforms. The supervisor still opens `/dev/hvc0` on Mac or `/dev/ttyS0` on Windows as its private binary transport, while the kernel console is separate. The pinned HVC console intercepts `0x0f` before tty raw mode and consumes a following SysRq key; disabling SysRq actions alone does not restore those bytes. Explicit null selection prevents console fallback and kernel output on the protocol device. Authenticated framing, exact EOF and cleanup requirements remain unchanged. Corrected runtimes have fresh identities and passed scoped Mac native validation; Windows requires its own native checks. Retained older Mac helpers keep their original boot policy and task bindings, and are not retrofitted or silently upgraded.

Both image assemblers seed one root-owned `/dev/null` character device (1:3, mode 0600, empty payload). With no kernel console, Go 1.27.3 needs that node to repair standard descriptors before the supervisor can mount devtmpfs; an empty `/dev` causes startup to fail before readiness. The normal devtmpfs mount and restricted device policy then take over. This fixed build-owned entry does not admit device files from runtime or project archives. Corrected v1/v2 rootfs and manifest identities passed scoped Mac package/native validation; Windows requires its own native evidence. Prior images and task bindings remain immutable.

Native helpers have fixed invocation contracts, no shell and no arbitrary command argument. Windows binds the exact child process to a kill-on-close, resource-limited Job Object. macOS supervises the owned VM directly and stops on parent-channel closure. VM monitor interfaces are not exposed to guests or network listeners. Cancellation/deadline terminates only the exact owned instance. Persistent private instance records retain receipts, not secrets or authorization codes.

V2 preserves these fixed limits. Before loading project code, its static supervisor freezes source, toolchain and dependency mounts and enforces UID/no-new-privileges and private control/report FD separation. The TS compile phase produces a separately frozen output tree before Node tests. Per-phase cgroups and bounded stream draining must finish with exact response EOF, confirmed owned process termination and successful Close; a bounded receipt cannot recreate live session authority. Typed authenticated phase facts become available only after that lifecycle completes. Native acceptance must establish the actual kernel/mount/page/inode/resource and receive-close/join behavior, not just static flags.

## Approval and lifecycle

1. New-task metadata advertises default backend, installed bundle identity and prerequisites without executing anything.
2. For v2, separate consent imports the pre-existing dependency bundle; committed-source inspection then freezes baseline, editable scope, protected tests/helpers/config and dependency bindings.
3. Independent readiness material binds runtime identity and policy. Approval permits one real guest boot/self-check and owned shutdown, not a repository test.
4. Baseline material binds repository digest plus fresh readiness. Approval permits one baseline execution.
5. Model context and plan approvals remain independent. Real provider calls remain disabled until separately authorized.
6. Candidate approval binds the exact candidate generation, profile and readiness. One execution may supply authentic failure evidence to the bounded Debugger/repair/reverification workflow; it does not grant arbitrary commands or retries after infrastructure failure.
7. QA/Auditor and delivery gates require a complete positive test count, current candidate binding and confirmed cleanup. A display card or model statement cannot establish a pass.

Durable dispatch is committed before external effects. An ambiguous start, incomplete frame, timeout, OOM, expired authority, changed asset or persistence failure stops the workflow; no automatic retry, backend change or paid API switch follows. A test failure with a complete validated event stream is distinct from infrastructure failure and may enter the existing approved repair budget.

## Recovery and evidence

Each real operation records backend, bundle/profile digest, host/guest platform, exact instance ID, dispatch/start/result/cleanup facts and bounded redacted diagnostics. Evidence distinguishes `local model protocol fixture` from `real built-in VM tests`. Legacy Docker evidence remains untouched.

Owned process handles and runtime-generated instance identity authorize normal termination. Recovery never kills by process name or reuses a bare PID, never deletes an image/cache/project, and never declares unknown ownership cleaned. Controller interruption preserves uncertain evidence and requires explicit recovery when exact ownership or termination cannot be established. Restarts cannot replay consumed approvals. Temporary instance receipts are retained until separately scoped evidence retention applies.

## Acceptance and packaging

Unit/protocol tests cover platform/profile tampering, missing assets, no host fallback, unapproved actions, malformed or forged guest frames, zero/partial test counts, output overflow, cancellation, failed cleanup and restart ambiguity. Local model fixtures exercise plan → patch → failing test → repair → re-test → audit/delivery without real credentials.

Native acceptance must boot the bundled guest from the installed app, show a real protected-test failure, repair only the writable file, pass re-verification, and confirm owned shutdown. It also checks forbidden host/network access and limits. macOS acceptance runs on a capable physical Apple Silicon host. Windows acceptance requires actual WHPX guest boot on a capable Windows runner; successful compilation, mocked output or installed UI smoke alone is insufficient. Hosted runners may lack supported nested virtualization; that is a blocked native gate, not permission to use TCG or skip it.

DMG and EXE retain existing UI/security controls and include runtime integrity and license materials. Release acceptance must verify install, launch, persistent data preservation, workbench UI and uninstall behavior on the exact final bytes. Any docs-only repackaging keeps component acceptance attributed to `6e60138` and requires measured inventories/output equivalence plus fresh package/install receipts; it is not a new native/provider run. Builds remain ad-hoc/unsigned unless signing identities are separately supplied. README capability claims are updated only to the evidence obtained; real account/model acceptance must be evidenced separately. This page grants no operation authority.

The unchanged installed v1 gate plus the [twelve-case v2 matrix](release-readiness.md#candidate-020-gates) remain mandatory. Model replies in that matrix are local protocol fixtures. Historical Go real-provider results do not establish new-language or Windows provider proof. Both runtime source sidecars accompany distribution outside installed Resources; see [source preparation](../../desktop/runtime-sources.md#v2-offline-runtime-and-source-sidecars).

## Primary references

- [Apple Virtualization](https://developer.apple.com/documentation/virtualization/running-linux-in-a-virtual-machine)
- [Virtualization entitlement](https://developer.apple.com/documentation/virtualization/adding-the-virtualization-entitlement-to-your-project)
- [QEMU security boundary](https://www.qemu.org/docs/master/system/security.html)
- [WHPX requirements](https://www.qemu.org/docs/master/system/whpx.html)
- [QEMU distribution](https://www.qemu.org/download/)
- [GitHub hosted runner limitations](https://docs.github.com/en/actions/concepts/runners/github-hosted-runners)
