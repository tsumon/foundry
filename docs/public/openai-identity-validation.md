# OpenAI identity validation compatibility

## Evidence and scope

An `identity_validation / auth_invalid` receipt proves that local identity validation failed, not which claim was wrong. Existing receipts have no sub-check evidence and must not be rewritten or retrospectively attributed to a specific predicate. No failed authorization code or token is retained for reproduction. This increment uses newly generated synthetic tokens and loopback issuers only.

Code inspection identified interoperability risks in requiring JWK `alg` and `use`, requiring JWT `typ`, and decoding `aud` only as a string. Local regression tests must demonstrate these defects before changes. Fixing them is not proof that a past real failure had the same cause or that a future real connection will succeed.

## Validation contract

Keep the pinned issuer, discovery/JWKS origin, issued client ID, original nonce, signature verification and expiration checks. Keep RS256 as the only accepted signing algorithm; do not add algorithms, trusted issuers, clock tolerance, remote key hints or retry/refetch behavior.

- A JWT may omit `typ`; when present it must be `JWT`. Reject unsupported critical headers rather than interpreting them implicitly.
- Select a unique RSA key by `kid` from the pinned JWKS. Missing JWK `alg` or `use` is allowed; a present value must be `RS256` or `sig`, respectively. A present `key_ops` must allow verification and must not contradict the signing use. Malformed, null, empty or conflicting declarations are not omissions. Keep at least 2048-bit RSA and unambiguous key selection.
- Accept `aud` as the exact issued client ID or a single-element string array containing that ID. Foundry has no additional trusted audience configured, so other audiences remain rejected. If `azp` is present, it must equal the issued client ID. Never use `dynamic_agent_client` or the API resource as a fallback audience.
- Keep invalid signatures, issuer/nonce mismatch, expiry, future not-before, malformed claims, unsafe subject and identity replacement rejected. No unverified claims become account metadata.

## Safe failure detail

Add optional, output-only `identity_check` to the existing version-1 diagnostic. It is allowed only on `identity_validation`. Values are a closed vocabulary: `token_format`, `jwt_header`, `signing_key_fetch`, `signing_key_invalid`, `signing_key_not_found`, `signature`, `claims_format`, `issuer`, `audience`, `subject`, `nonce`, `expiry`, `not_before`, `identity_binding`, `metadata_reflection`.

Retain the existing safe code and independent durable credential/cleanup evidence. Store only the check name, never the expected or received claim, key ID, key material, token, code, URL, provider body or raw error. Old receipts without a check remain valid; unknown values fail closed. This field must not be accepted in write requests or alter approval material/digests.

Keep the existing workbench layout and controls. Add a static Chinese label for the recorded check to the failure panel. The overall failure notice must defer to recorded local evidence instead of claiming local cleanup is unknown when it is recorded as complete. Remote grant state remains unverified, even after successful local cleanup.

## Authority and recovery

Preserve one proposal, one independent approval and at most one dispatched attempt. Failures remain consumed; status reads do not exchange a code again. Production entry remains off by default. No real login, catalog query, inference, refresh, retry, paid API fallback, scripts or Git writes are enabled. Tests use synthetic secret stores, not macOS Keychain. Existing real account records are not test fixtures and are not modified.

## Implementation and acceptance

1. Write signed local regression cases for omitted optional metadata and array audiences; observe failure on the old validator. Pair each accepted case with conflicting/malformed metadata and all existing identity-boundary rejection cases.
2. Correct only the parsing/selection predicates and carry a bounded sub-check into new terminal diagnostics. Exercise one full synthetic connection success and failed-proof cleanup; assert no replay, no downstream model request, no credential commit on failure and correct durable evidence.
3. Extend diagnostic domain/output-only/persistence tests and the current failure panel. Test old records and malicious/unknown values, plus unchanged approval controls.
4. Run Go unit/integration/race/vet, frontend tests/build and export tests. Inspect a synthetic workbench failure on desktop and narrow viewport; verify no contradictory cleanup text or new execution control.
5. Export only allowlisted source/public documentation, scan files and public Git history for secrets, check dependencies/workflow, then push reviewed source and check the exact commit's CI. Do not create a Release. A new real attempt requires fresh material and separate user authorization.

## References (checked 2026-10-20)

[OpenAI registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in) requires verification against the issued client and attempt nonce; [OpenAI website identity verification](https://developers.openai.com/siwc/website) describes signature and claim validation. [RFC 7517 §4](https://www.rfc-editor.org/rfc/rfc7517.html#section-4) defines optional JWK metadata and key operations. [OpenID Connect Core §3.1.3.7](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation) specifies audience validation, including rejection of additional untrusted audiences. [RFC 7519 §5.1](https://www.rfc-editor.org/rfc/rfc7519.html#section-5.1) defines optional JWT type metadata.
