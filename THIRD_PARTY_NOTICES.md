# Third-Party Notices

This source distribution depends on third-party software. The exact dependency
versions, lock evidence, platform selectors, observed license-text paths, and
SHA-256 digests are recorded in [`third_party/inventory.json`](third_party/inventory.json).
The corresponding unmodified legal texts are stored by content digest under
[`third_party/licenses/`](third_party/licenses/); byte-identical texts are
deduplicated.

The inventory covers:

- the npm runtime closure (`react`, `react-dom`, and `scheduler`);
- the complete npm build and test lock tree; and
- four Go runtime modules plus two modules present only for dependency tests.

## Coverage limits and release blockers

This material is attribution evidence, not a legal audit or blanket license
clearance. Package metadata such as an SPDX expression is not treated as a
substitute for the actual legal text.

The npm lock contains 148 package entries. Legal texts were checked for the 105
entries installed by the reviewed macOS arm64 lock installation. The remaining
43 entries are optional packages for other operating-system or CPU targets;
they were not installed, their legal texts were not verified, and they are not
redistributed in this source candidate. Each is marked
`unverified-not-installed` in the inventory. Those unresolved texts are a
public-release blocker for any affected platform, and this distribution makes
no cross-platform compliance claim.

For installed package roots, the reviewed legal-file set includes
`LICENSE*`, `LICENCE*`, `COPYING*`, `NOTICE*`, `PATENTS*`,
`THIRD-PARTY-LICENSE*`, and `THIRD-PARTY-NOTICES*`. The 105 installed npm
entries map to 110 legal-text references. Together with the 8 Go references,
the inventory contains 118 references to 86 deduplicated byte-exact text
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
