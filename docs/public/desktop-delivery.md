# Desktop delivery design

Status: 0.2.0 dual-runtime packaging and scoped Mac native/installation/upgrade acceptance are recorded. Published packages require their exact release receipts and platform checks; this design document does not assert a future hosted CI result.

## Outcome and boundary

Package the existing React workbench and Go controller as a local desktop application, first for macOS Apple Silicon, then Windows x64. Installation must not require Node.js, npm, Go, or a C compiler. Installing the package grants no provider, Docker, script, Git-write, deployment or migration authority. The later [controlled coding mode](live-coding.md) is a separate explicit entry; it never relabels simulated history as real execution.

The workbench keeps its existing layout and independent approvals. The desktop application provides an ordinary persistent workbench, a separately retained controlled coding workbench, and an explicitly labeled isolated local simulation entry. Simulation evidence remains `synthetic_stub`; it is never presented as a compiler or Docker result. Source repositories remain read-only and simulation evidence is retained.

## Architecture

Use a sandboxed Electron window with the unchanged built SPA, and a bundled native Go child process. This avoids a controller rewrite and gives macOS and Windows the same web rendering behavior. A native WebView per OS would add two lifecycle/security integrations; a system-browser launcher would not provide a self-contained desktop window.

- The child chooses a literal `127.0.0.1` ephemeral port and reports readiness through its pipe. No port-reservation race or scraping log messages.
- An ephemeral launch capability is exchanged through private pipes and attached by the desktop main process, not exposed in renderer JavaScript, URLs, or logs. Existing host, origin, session and JSON checks remain in place.
- Renderer sandbox and context isolation stay enabled; Node integration, webviews, arbitrary external navigation, permissions, and generic downloads are denied. Only authenticated, same-window patch/report attachment responses from the exact local controller may open a native save dialog. No arbitrary shell/IPC bridge is exposed.
- Startup uses an explicit environment allowlist and fixed arguments. It cannot inherit operator activation flags or provider credentials. No automatic update, analytics, sign-in, retry, or paid fallback is introduced.
- One desktop owner per profile. Closing/quitting shuts down its own child through the lifetime pipe; errors stop the session and retain evidence. No process-name kills or deletion of another instance's locks.
- Installed resources are read-only inputs. Persistent state lives in a dedicated per-user desktop data directory, separate from existing CLI data. Removal/reinstallation does not erase state or evidence.

## 0.2.0 resources and gates

The native package contains separate `sandbox/` (retained Go v1) and `sandbox-v2/` (one image shared by the three closed language profiles). Startup verifies both manifests, the v2 runtime descriptor and legal indexes before wiring the owned controller paths. A missing optional v2 resource cannot invalidate intact retained v1 evidence; new package builders require both bundles. No catalog read boots or probes either image. See [sandbox identity](embedded-sandbox.md) and [offline input/source-sidecar preparation](../../desktop/runtime-sources.md#v2-offline-runtime-and-source-sidecars).

Electron/runtime source, `sandbox-sources` and `sandbox-v2-sources` are separate distribution sidecars, outside installed Resources. Complete source inventories and `distribution-manifest.json` bind actual files to the installer and both bundles. Preserve old task runtimes and released 0.1.0 assets; build fresh candidate bytes after all source/version changes.

The installed native driver retains the unchanged v1 gate and adds a closed twelve-attempt v2 matrix with distinct bounded receipts. Each profile runs real tests with admitted dependencies and deliberately failing/repaired source, while model replies remain `local_protocol_fixture/not_real_provider`. Extra TS compile, Python setup/discovery and representative resource/stream/cleanup cases must produce their exact expected failure facts. Windows requires the full installed matrix before successful artifact upload; failure-only diagnostics expose only bounded receipt filenames, not packages or data directories. See the [matrix and pinned upgrade gates](release-readiness.md#candidate-020-gates). The complete matrix passed on Mac component source `6e60138`; see [scope and attribution](alpha-status.md). The final carrying package must retain exact component/source linkage and measured equivalence, with fresh package/installation checks; Windows requires actual native hosted CI.

## Platform security

macOS keeps its existing filesystem, committed-Git, and recovery protections. Packaging is ad-hoc signed for local integrity, not Developer ID signed or Apple-notarized. Windows uses a per-user installer without administrator elevation. No signing identity is invented, no Keychain credentials are enumerated, and OS trust protections are not disabled.

Windows uses native implementations of controller lock ownership, reparse-point-safe file admission, exclusive directory publication, private storage and durable publication ordering. Do not replace these with permission-check removal, check-then-overwrite, arbitrary PATH executables, or plaintext secret storage. Credential Manager has separate Foundry account/diagnostic namespaces and a strict 2,560-byte blob limit. Real Docker remains unsupported on Windows; enabling configuration cannot bypass that limit.

Git execution uses an explicit trusted installation, with configuration, hooks, prompting, filters, protocol access and lazy fetch constrained as before. Package prerequisites and missing-Git diagnostics must be explicit. Windows path admission must reject aliases, alternate data streams, reserved names, junction escapes and case collisions. Unix executable-mode semantics may not be silently discarded: reject unsupported imported modes until they can be represented and verified faithfully.

## Packaging and acceptance gates

1. Build the native controller and simulation binary, compiled SPA and desktop resources from the same reviewed source. Record versions and SHA-256 checksums; include licenses and notices.
2. Produce a macOS `.app` and DMG. Mount the DMG read-only, install a fresh copy, launch outside the source directory, verify first run, persistence, second-instance handling, clean quit, restart and the independently approved local simulation flow.
3. Build Windows natively in CI, produce a per-user NSIS `.exe`, install into a fresh non-ASCII/spaced path, test launch/readiness, persistence, approval guards, simulation delivery, quit/restart and uninstall preserving data. Cross-compilation alone is not native acceptance.
4. Keep existing full macOS tests/race/vet, frontend tests and security scans. Add desktop tests, dependency audit, native Windows boundary tests and installer smoke checks. Do not skip or lower existing gates.
5. Publish only reviewed clean source/documentation and CI artifacts. No private records, accounts, original evidence, signing secrets or local data in packages or Git. Creating a GitHub Release is a separate action and is not part of this milestone.

The bilingual README must state which installed workflows were accepted, explain unsigned/notarized status, and distinguish local simulation, historical bounded real tests, and capabilities that remain closed. Screenshots must come from the real application with disposable data, not imagined results.

### Native CI corrections

The pinned Electron package exposes an explicit runtime installer rather than a
`postinstall` hook. Each native job must run that installer after `npm ci` and
before runtime contract tests; the source-only verifier must not download an
Electron runtime. Installation remains version/checksum-bound by the locked
Electron package. Native package and installed-app checks remain mandatory.

Windows runner defaults can expose a short 8.3 temporary path (`RUNNER~1`).
Native CI creates a fresh, explicit long-form temporary directory before
installing dependencies and sets both `TEMP` and `TMP` for subsequent steps.
Production path admission still rejects short aliases; it does not silently
canonicalize them or relax reparse, ACL, reserved-name or protected-file checks.

Installed Windows acceptance retains a long, non-ASCII, spaced data path instead
of shortening it to avoid native filesystem limits. Fixed, isolated Git commands
must explicitly support long paths without changing system/user Git configuration
or accepting namespace aliases. Native regression compares the legacy disabled
setting with the controlled long-path setting and verifies a real, applicable
patch. A nonzero diff result with empty output must never be sealed as a valid
delivery; changed trees require nonempty patch evidence. Attachment failures must
identify the artifact type, byte count and digest without printing its content.

Native Windows UI acceptance must wait for the rendered workbench, not merely a
window title or a responsive process. A bounded OS UI Automation query binds the
window handle and owner PID, then requires the document's navigation, create-task
control and received-data/connected-event-stream state. First launch and restart
must both pass. Foreground-window client-area paint and retained screenshots are
additional checks, never substitutes for semantic readiness. A blank window fails
the gate; no renderer debug port, activation switch or relaxed approval is added.

The coding-mode extension also exercises the actual packaged native menu and its
configuration-only confirmation. Windows displays its bundled VM as configurable,
but not yet readiness-checked; the separate Docker path remains unavailable.
Tests switch back to the ordinary workbench, restart and
uninstall while checking retained coding data. Only a separately opted-in CI
diagnostic test touches its fresh synthetic Credential Manager probe; it never
enumerates account entries or accesses a real account. These checks do not certify
power-loss recovery or real provider use.

The shared race suite has a finite 1,200-second **per-package** budget. The
600-second normal-suite budget, uncached full test selection, race detector,
vet and 45-minute workflow bound remain unchanged. A native CI trace exhausted
the former cumulative race-package budget at the penultimate acceptance test,
only one second into that test; it was not evidence of a 600-second individual
test hang. This adjustment must be validated by a complete race pass, not by
skipping tests or treating a timeout as success.

## Gitiles source archive integrity

Gitiles regenerates archive timestamps, so six immutable-revision source archives
use a committed canonical SHA-256, entry count and total file-data size instead
of requiring an identical gzip transport. Verification never extracts archives:
bounded gunzip (256 MiB maximum), strict 512-byte POSIX headers and checksums,
only regular files/directories and single local PAX headers (`mtime`/`path` only).
Unsafe or duplicate paths, links, unsupported metadata, malformed padding and
trailing data are rejected. Bytewise-path-sorted canonical records bind effective
path, raw type, mode, numeric and named owners, size and file-content SHA-256.
Filesystem mtime, archive order, gzip metadata and the validated PAX wrapper are
excluded from source-tree identity. Original compressed hashes remain provenance, while the delivered
manifest and SHA256SUMS identify actual saved bytes. Packaging rechecks committed
pins, not the downloaded receipt. Other source inputs retain exact raw-byte pins.

## Official references

- [Electron security checklist](https://www.electronjs.org/docs/latest/tutorial/security)
- [Electron sandboxing](https://www.electronjs.org/docs/latest/tutorial/sandbox)
- [Electron supported releases](https://releases.electronjs.org/?channel=stable)
- [electron-builder DMG](https://www.electron.build/docs/dmg/)
- [electron-builder NSIS](https://www.electron.build/docs/nsis/)
