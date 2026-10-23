# Third-Party Notices

This source distribution depends on third-party software. The exact dependency
versions, lock evidence, platform selectors, observed license-text paths, and
SHA-256 digests are recorded in [`third_party/inventory.json`](third_party/inventory.json).
The corresponding unmodified legal texts are stored by content digest under
[`third_party/licenses/`](third_party/licenses/); byte-identical texts are
deduplicated.

The inventory covers:

- the npm runtime closure (`react`, `react-dom`, and `scheduler`);
- the complete **web** npm build and test lock tree; and
- five Go runtime modules plus two modules present only for dependency tests.

The desktop build lock is separate (`desktop/package-lock.json`). Its npm build
tools are not installed in the shipped application. This source inventory is not
a claim that every desktop build dependency has been legally audited.

## Desktop runtime distribution

The installed app includes this file, the complete `third_party/` tree, Foundry's
Apache-2.0 license, the exact Go toolchain LICENSE/PATENTS and the target Electron
archive's MIT license and **complete** `LICENSES.chromium.html`. The latter
includes Chromium, Node.js and native runtime component notices. Open **Help →
Third-party Notices** to inspect the offline copies. Electron's runtime is not
covered by Foundry's Apache-2.0 license.

Each platform installer artifact is accompanied by pinned runtime sources and
their SHA-256 manifest. See [runtime source and replacement guidance](desktop/runtime-sources.md)
and the [immutable source manifest](desktop/runtime-sources.json). Keep those
sources when redistributing an installer. This supplies actual source material,
not a promise of a future three-year source offer. No blanket legal, patent or
binary-reproducibility clearance is claimed.

In the installed legal directory, open the adjacent `README-runtime-sources.md`
instead of following source-repository-relative links. Windows NSIS core and
selected plugin notices are under `third_party/installer-notices/`; their exact
source pins are in `desktop/installer-sources.json` in the public source tree.

Windows CGO binaries may incorporate static MinGW-w64 and GCC runtime code.
`third_party/windows-runtime-notices/` preserves MinGW-w64 13.0.0's complete
static-runtime aggregate, winpthreads notices, GCC 15.2.0's GPLv3 text and GCC
Runtime Library Exception 3.1, with immutable source pins and byte digests.
These components are not covered by Go's BSD license or Foundry's Apache-2.0
license. Native builds check the runtime version and record candidate archive
hashes. This is not a claim every archive member is linked, an exact compiler
distribution archive pin, or blanket clearance of all compiler-distribution code.

Git is an independently installed prerequisite, not a bundled MinGit runtime.
Build tools and platform-specific web compiler bindings are not application
payload merely because they appear in a lockfile. The Windows installer's native
stub notices are included with its package; its native build/acceptance evidence
is separate from the macOS build.

## Built-in sandbox runtime

Alpha.3 bundles a separate Linux guest, not Docker Desktop. Its pinned input
versions, official download sources, sizes and hashes are in
`desktop/sandbox/inputs.lock.json`. The installed `sandbox/legal/` directory
contains the Linux GPL-2.0 text, Go LICENSE/PATENTS, a component notice and the
exact corresponding-source sidecar inventory. The Mac helper uses Apple's
system Virtualization.framework and is Foundry Apache-2.0 source.

The `sandbox-sources` sidecar contains Linux 6.18, the 6.18.58 patch, the pinned
Alpine linux-lts build recipe/configurations/patches and Foundry's assembler
recipes. Windows additionally includes the minimal QEMU 11.1.2/WHPX source build,
required linked-library sources, license texts and exact build configuration.
Only the measured DLL dependency closure is shipped; the general-purpose MSYS2
QEMU GUI package and MSYS2 build environment are not installed with Foundry.
QEMU and the Linux guest are not relicensed under Foundry's Apache-2.0 license.

Keep both the Electron/installer runtime source material and `sandbox-sources`
with redistribution. The build records hashes and includes corresponding source
bytes rather than relying only on download links. This is not blanket legal or
security clearance. Native acceptance and published vulnerability assessment
remain separate from source/license inventory checks.

## Coverage limits and release blockers

This material is attribution evidence, not a legal audit or blanket license
clearance. Package metadata such as an SPDX expression is not treated as a
substitute for the actual legal text.

The npm lock contains 148 package entries. Legal texts were checked for the 105
entries installed by the reviewed macOS arm64 lock installation. The remaining
43 entries are optional packages for other operating-system or CPU targets;
they were not installed, their legal texts were not verified, and they are not
redistributed in this source candidate. Each is marked
`unverified-not-installed` in the inventory. Their legal-text coverage must be
resolved before redistributing those particular packages; do not infer they are
bundled into a desktop installer. This distribution makes no blanket
cross-platform compliance claim.

For installed package roots, the reviewed legal-file set includes
`LICENSE*`, `LICENCE*`, `COPYING*`, `NOTICE*`, `PATENTS*`,
`THIRD-PARTY-LICENSE*`, and `THIRD-PARTY-NOTICES*`. The 105 installed npm
entries map to 110 legal-text references. Together with the 10 Go references,
the inventory contains 120 references to 86 deduplicated byte-exact text
files in this distribution. In particular, `prettier@3.9.10` maps both its
top-level MIT `LICENSE` and its complete 362,597-byte bundled
`THIRD-PARTY-NOTICES.md`, which contains the upstream bundled MIT, ISC, BSD,
Apache-2.0, and BlueOak notices.

Two installed npm entries require explicit provenance notes:

- `saxes@6.0.0`: the complete license was taken from the immutable upstream
  commit matching both the npm `gitHead` and peeled `v6.0.0` tag. Its source,
  byte count, and digest are recorded in the inventory.
- `@rolldown/binding-darwin-arm64@1.2.14`: the binding archive contains no
  legal file. The inventory maps it to the complete `LICENSE` and
  `THIRD-PARTY-LICENSE` from the matching `rolldown@1.2.14` parent and immutable
  upstream tag. This is same-version provenance evidence, not a claim that the
  files were present in the binding archive.

The copied texts preserve upstream copyright, attribution, notice, bundled
third-party-license, and applicable patent-grant material. Consult the
inventory for the exact mapping from every dependency version to those bytes.

The Windows pipe regression fixture in
`desktop/test/fixtures/qemu-11.1.2-win-stdio.c` is the exact QEMU 11.1.2
`chardev/char-win-stdio.c`, with its original Fabrice Bellard copyright and MIT
permission notice retained. The application-specific pipe correction is in
`desktop/sandbox/windows-stdio.cjs`; its recipe accompanies the complete pinned
QEMU source. The exact `desktop/test/fixtures/qemu-11.1.2-version.rc` resource
fixture is also from QEMU 11.1.2 (GPL-2.0-or-later). The build-only UTF-8 manifest
recipe and native regression source are shipped in the corresponding-source kit.
These recipes accompany the complete pinned QEMU source archive in Windows
sandbox source sidecars.
