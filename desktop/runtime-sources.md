# Native runtime notices and corresponding source

Foundry Desktop uses Electron 45.0.0 (Chromium 156.0.8078.48, Node.js 24.22.0).
Foundry's own source is Apache-2.0; the bundled runtime and its components retain
their respective licenses. See **Help → Third-party Notices** in the installed
app. `Electron-LICENSES.chromium.html` is preserved in full, alongside Electron's
MIT license, the exact Go toolchain LICENSE/PATENTS and Foundry's existing notices.
The runtime HTML includes Node's bundled legal material and FFmpeg's credits and
LGPL text. Build-time npm tools are not bundled into `app.asar`.

## Source supplied with the installers

Each platform's CI artifact includes a `runtime-sources` directory on the same
download terms as its installer. `manifest.json` pins source URLs, immutable
revisions, SHA-256 digests and the source-tree destinations. `SHA256SUMS` identifies
the unmodified downloaded bytes; it is not an upstream signature or a claim of
reproducible binaries. No long-term written source offer is substituted for these
files. Keep the source directory when redistributing the corresponding installer.

Six Gitiles `.tar.gz` inputs regenerate timestamps even at an immutable revision.
Their committed `canonical` pins fix the source-tree identity: entry count,
total file-data bytes and a SHA-256 over UTF-8 JSON array records, sorted by
bytewise effective path. Each newline-terminated record contains path, raw type,
mode, uid, gid, uname, gname, size and file-content SHA-256; the hash starts with
`foundry-gitiles-tar-v1\n`. Filesystem mtime, archive order, gzip metadata and the
validated PAX transport wrapper are not source-tree identity. Source contents,
types, paths and all recorded permissions/ownership remain fixed.

Verification uses bounded decompression (256 MiB) without extraction. Only POSIX
regular-file/directory headers and a single local PAX `mtime`/`path` extension
are admitted. Checksums, numeric fields, UTF-8, padding and end markers are
checked; links, unknown extensions, unsafe/duplicate paths and file/parent
conflicts fail closed. Downloads have a separate 64 MiB maximum. Committed raw
hashes/sizes retain the original reviewed archive provenance; delivered receipts
record `originalTransport` and the actual saved bytes/hash. Packaging rechecks
the committed canonical pins, never trusts the sidecar receipt as authority.
All other inputs still require their exact original byte count and raw SHA-256.

The Windows installer has an additional `installer/` source subdirectory,
described below. Providing it with the Mac artifact as well does not imply those
Windows components are present in the Mac runtime.

The kit supplies the exact Chromium FFmpeg fork, Electron source/patches,
Chromium controlling build scripts and root configuration, Windows stub
generator, linked patched Opus source, NASM source/build integration, and notices.
The original archives preserve upstream license/copyright files, including
optional sources not selected by the shipped configuration. This is not a
standalone compiler/toolchain or a blanket license/patent-clearance claim.

Exact runtime source chain:

- Electron: `54f961b44b83e4ece81065ab60a182fd0c38c01e` (v45.0.0).
- Chromium: `d0c02bfad08ec92a6ffcf989e53cd995e6381a4c` (156.0.8078.48).
- FFmpeg fork: `9db86ce5b5b454dfc96c435f9f0723012980c37f`.
- Electron's FFmpeg modification: `patches/ffmpeg/link_with_loader_path.patch`
  inside the supplied Electron archive; apply through the pinned Electron patch
  system exactly once.

## Rebuilding / replacing the shared FFmpeg library

Use the versioned development/build instructions **inside the supplied Electron
source**, including its `DEPS`. Set up the documented platform SDK/compiler and
GN/Ninja or Siso/depot_tools separately. Extract supplied inputs to the locations
listed in `manifest.json` within that pinned build environment; synchronize other
project/toolchain prerequisites as specified by Electron. Import
`//electron/build/args/release.gn` with `target_cpu="arm64"` for macOS or `"x64"`
for Windows. Tarball-only builds use the documented
`override_electron_version="45.0.0"`. Follow those pinned instructions to generate
the build and build Electron's shared FFmpeg target.

The normal runtime uses `is_component_ffmpeg=true`, `ffmpeg_branding="Chrome"`
and `proprietary_codecs=true`. The separate `build/args/ffmpeg.gn` describes an
alternate configuration, not this runtime's configuration. The selected Mac/ARM64
and Windows/x64 FFmpeg configs disable GPL, nonfree and version-3 code; this is
configuration evidence, not patent advice or a binary reproducibility assertion.

FFmpeg is dynamically linked. An interface-compatible replacement belongs at:

- macOS: `Foundry.app/Contents/Frameworks/Electron Framework.framework/Versions/A/Libraries/libffmpeg.dylib`.
- Windows: `ffmpeg.dll` beside `Foundry.exe` in the installed application folder.

Foundry's application-resource manifest does not hash or restrict replacing this
native shared library. The application imposes no additional prohibition on
modification for your own use or reverse engineering to debug such modifications
where the LGPL requires that permission. Changing a Mac bundle invalidates its
existing integrity signature; build and sign your modified copy with your own
development identity or local ad-hoc signature following Apple's tooling. Do not
disable OS protections. A modified build is not the original verified artifact.

The shared-library arrangement follows LGPL-2.1's shared-library mechanism; it
does not replace the separate source/notice obligations for the distributed
library. No standalone offline rebuild, exact reproduction of upstream binaries,
or modified-library runtime compatibility test is claimed here.

## Maintainer checks

### Windows installer components

The native installer pins electron-builder's NSIS toolset `1.2.1` (NSIS 3.12).
Its source sidecar contains NSIS source commit
`e3f60402bcdf7be822d159b531c6e38ddf32de12`, the exact StdUtils 1.14 release with
its matching source tar, Nsis7z 19.00 wrapper and LZMA SDK 19.00 sources, and UAC
plugin source. Original notices, including StdUtils's author clarification, are
preserved byte-for-byte under `third_party/installer-notices` in the installed
legal directory. Do not infer the whole installer is Apache-2.0 or MIT.

Use NSIS's supplied SCons build instructions for its core/built-in plugins;
StdUtils's release includes its source tar and Visual Studio project. Nsis7z's
original documentation requires overlaying the SDK archive's `C/` and `CPP/`
into `Contrib/nsis7z/`, then using its included VS2017 solution. UAC supplies its
source and zlib license. These are corresponding source/configuration materials,
not a promise that an offline compiler/SDK is supplied or that binaries were
reproduced. Keep the original source terms and notices with modified copies.

Foundry's build configuration and exact, hash-checked template modification are
in the public `desktop` sources. This first Alpha creates no shortcuts or file
associations. Its build-owned template copy removes the unused WinShell
AppUserModelID cleanup call and the cosmetic SpiderBanner block; each original
template is hash-pinned before transformation. A current-user-only one-click
installer retains the standard progress UI. It does not admit an all-users mode
or an elevation helper. Other uninstall and data-retention behavior stays
unchanged. Compiler configuration files and custom tool/signing overrides are
refused. Compiler traces for both installer and uninstaller reject unreviewed
plugins, including WinShell, SpiderBanner and nsProcess. The installer never
ships the whole build-tool cache or an elevation helper.

### Windows CGO runtime notices

The bundled Go executables use native CGO/SQLite. System-only PE DLL imports do
not prove static runtime code is absent. The installed legal directory therefore
includes `third_party/windows-runtime-notices/`: MinGW-w64 13.0.0's aggregate
static-runtime and winpthreads notices, plus GCC 15.2.0's GPLv3 and Runtime Library
Exception 3.1 texts. Their immutable upstream URLs and byte hashes are in the
adjacent manifest. Native builds require those versions and record the actual
compiler and available runtime archive hashes without asserting every member is
linked or that an unobserved compiler download has a particular digest. The
compiler itself is not shipped. Exception conditions and the original notices
remain applicable; these materials are not a blanket legal clearance.

### Preparing the sidecars

Run `npm ci --prefix desktop --ignore-scripts` to install the locked build-time
verifier without Electron installation scripts. Then run
`node desktop/runtime-sources.cjs /absolute/new/output` to download each pinned
input once and verify it; a failure stops without fallback or hash rewriting.
For the Windows source inputs, run
`node desktop/runtime-sources.cjs /absolute/new/output/installer installer`
after creating the main sidecar. Official release redirects are limited to three
explicitly allowlisted HTTPS hops; content is always checked against its committed
canonical or exact-byte pins before it is saved.
Set `FOUNDRY_RUNTIME_SOURCES` to a previously verified input directory to prepare
the sidecar without network. The installed app never runs this build-time script.
When updating Electron, review its actual platform notice files, source pins,
patches, configurations and shared-library paths before changing this manifest.

## V2 offline runtime and source sidecars

Candidate 0.2.0 installs exactly one legacy Go runtime at `sandbox/` and one shared Node/TypeScript/Python runtime at `sandbox-v2/`. The latter binds a canonical `runtime-inventory-v2.json`, the static supervisor, three owned runners, Node 24.22.1, TypeScript 5.9.3 and Debian Python 3.13.5 (`3.13.5-2+deb13u5`) plus the measured loader/stdlib/library closure. The descriptor embedded in the image equals the external descriptor; ordinary file SHA-256, descriptor-domain digest, projected-runtime digest and final bundle identity are distinct. Asset validation does not boot a VM or establish test success.

Prepare verified runtime/source inputs with the explicit build-only `desktop/sandbox/multilang-inputs.cjs` command and committed lock, as described in [contributor setup](../CONTRIBUTING.md#pinned-v2-inputs). Supply a trusted absolute host Python 3.13+ path and pinned Go 1.27.3. Build v1 first using the existing native sandbox builder and retain its exact matching source sidecar. Then run the v2 builder on the supported native target with explicit local inputs:

```sh
export FOUNDRY_BUILD_PYTHON=/absolute/trusted/python3.13
export FOUNDRY_MULTILANG_INPUTS=/absolute/verified/multilang-inputs
export FOUNDRY_GO=/absolute/pinned/go
export FOUNDRY_SANDBOX_BUNDLE=/absolute/v1-output/bundle
export FOUNDRY_SANDBOX_SOURCES=/absolute/v1-output/sandbox-sources
export FOUNDRY_SANDBOX_INPUTS=/absolute/verified/native-inputs
node desktop/sandbox/build-bundle-v2.cjs /absolute/new/v2-output
export FOUNDRY_SANDBOX_V2_BUNDLE=/absolute/new/v2-output/bundle
export FOUNDRY_SANDBOX_V2_SOURCES=/absolute/new/v2-output/sandbox-v2-sources
```

The v2 builder is offline: it verifies cached Go modules and locked local archives, compiles only the owned static guest, constructs the deterministic image/descriptor and publishes its manifest last. It refuses missing, changed or unpinned input. On Windows, also provide `FOUNDRY_WINDOWS_VM_RUNTIME` pointing to the source-built minimal QEMU tree with its configuration, PE closure, stdio/UTF-8 and zlib proofs; an amd64 rootfs alone is not a native Windows bundle. Do not replace missing native input with broad prebuilt QEMU or software emulation.

Package only after both bundles and sources validate. The resulting `sandbox-sources` and `sandbox-v2-sources` directories stay beside build/distribution output, outside installed Resources, together with the Electron `runtime-sources` kit. V2's `legal/sources.json` binds every actual sidecar file; `legal/runtime-components.json` relates runtime inputs, corresponding sources and notices. The v2 sidecar includes exact upstream/distribution sources, TypeScript compiler input, inherited native/kernel source kit, Go source/toolchain archive, linked module sources, guest/runner sources and build recipes. Source URLs alone do not replace these files. Retain original licenses, including nested numeric COPYING variants.

`distribution-manifest.json` binds the installer, both installed bundle manifest hashes and sidecar inventories; SHA256SUMS binds the installer and distribution manifest. Installed acceptance compares sidecars against actual installed bundles. Startup validates sealed runtime/descriptor/notice assets and compact source indexes without reading source tarballs. The installed controller receives startup-owned `FOUNDRY_VM_BUNDLE` and `FOUNDRY_VM_V2_BUNDLE`; these are not interchangeable with build input variables or caller-selected execution authority.

Actual native boot, all-profile interpreter/compiler/isolation/resource/cleanup checks, installed UI and Alpha.3 plus stable upgrades remain mandatory [release gates](../docs/public/release-readiness.md#candidate-020-gates). Static builds or process-platform fixtures cannot satisfy them. Candidate 0.2.0 results remain pending until exact-source receipts exist; this guide invents no native Windows, provider or signing result.
