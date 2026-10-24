# Desktop subscription parity and upgrade compatibility

Status: implemented. Installer acceptance is bound to the exact-commit workflow and native reports; real provider results must not be inferred from fixtures.

## Contract

macOS and Windows expose the same controlled-coding subscription preparation, independent connection approval, browser handoff, independent catalog approval, and separately approved task-account continuation. Normal workbench mode remains non-executing. Existing UI layout is retained. There is no automatic OAuth retry, credential refresh, paid-API fallback, or host execution fallback.

Original task snapshots, baseline records, evidence, and consumed budgets are immutable. An upgrade must not turn a missing frozen runtime into a corrupt-snapshot diagnosis or rewrite the task to the current runtime. New tasks use the current verified runtime; old tasks may use only a fully verified runtime whose complete identity equals their frozen profile. Fresh operational readiness/approvals remain necessary where required.

## Windows credential storage

Windows uses its native Credential Manager, not plaintext application files. Native blobs retain their documented size limit. A bounded segmented representation supports larger subscription bundles using only deterministic Foundry-owned targets derived from the existing opaque secret reference. A small versioned commit record binds length, chunk count, and digest. Reads reject pending, partial, reordered, malformed, oversized, or tampered records. Legacy small records remain readable.

The account service already journals a unique credential-version write before storing a bundle. Segmented cleanup must cover every bounded target for that exact reference even when publication was interrupted, and must report failure until every owned segment is absent. It must not enumerate or touch unrelated credentials. No secrets, tokens, authorization URLs, or credential contents enter diagnostics, SQLite, screenshots, or public reports. Native CI uses only generated diagnostic references and synthetic values.

## Frozen runtime retention

A private content-addressed runtime archive retains verified installed bundles across upgrades. Publication uses a fresh staging directory, verified assets, exclusive final publication, and strict path/ownership protections. No download or execution is part of archiving. A damaged archive never becomes an executable fallback. Archives are not automatically deleted.

The controller loads a current default runtime plus exact retained identities. Readiness, execution, and cleanup resolve the runtime from the frozen task profile, not a mutable global default. Missing or altered archives fail closed with a useful recovery diagnosis. Platform/policy/protocol and every identity digest must match; matching a release name alone is insufficient.

For installations predating archive support, an explicitly supplied locally verified prior bundle may be retained without rolling back the application or mutating the database. Imports are local-only, verified against the expected complete frozen identity, and recorded outside original evidence. No broad filesystem search or arbitrary renderer-selected executable is permitted.

## Acceptance and delivery

Use disposable data and local protocol fixtures for subscription success, expiry, cancellation, partial credential writes, crash recovery, malformed responses, and permission denials. Verify both native stores only with Foundry-owned synthetic items. Check large Windows bundles on native Windows CI. Test old/new runtime coexistence, missing/tampered archive denial, unchanged raw task records and budgets, and no unsolicited provider/VM calls.

Run full Go, frontend, desktop, public-export, security and installer gates without lowering standards. Windows installation and WHPX guest acceptance run on Windows; macOS hardware VM results are reported separately from CI package contracts. Package provenance and hashes identify the exact reviewed commit. Preserve an app/data backup before updating the user's Mac installation. Only reviewed public sources are pushed; no private history, account data, original evidence or Release is published.
