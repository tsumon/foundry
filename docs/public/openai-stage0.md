# OpenAI stage 0: safety preparation, not live authorization

Stage 0 strengthens the existing local workbench before any real OpenAI connection. It does not enable real sign-in, model discovery, inference, refresh, paid fallback, tools, scripts or Git writes. Kimi is unchanged and outside this stage. Connection, catalog and inference continue to require separate single-use approvals and explicit execution. A successful local check is not a permission to advance to the next stage.

## Design

Reuse the existing controller, SIWC protocol implementation, model router, immutable approval materials and durable recovery records. Do not replace the engine, add a second approval system, or enable production traffic through configuration. Normal constructors retain their hard denial before credentials or network access. Local fixtures remain constructor-owned literal-loopback endpoints.

Three focused additions are required:

1. **Exact OpenAI endpoint boundaries.** The subscription router accepts only the canonical `/v1` base and its supported catalog/inference method-path pairs. Validate before credential leasing. Reject path suffixes, trailing/encoded aliases, query strings, fragments, userinfo and redirects. Simulation is still restricted to the exact constructor-pinned origin; the official live origin remains denied. Test malicious route metadata and changed origins with secret-read and network counters. SIWC discovery must not widen the pinned origin or accept credential-bearing URLs. Do not alter Kimi routing.
2. **Isolated Keychain diagnostic.** Provide a local command, not a browser API or a login button. It generates synthetic values only, in a dedicated Keychain service namespace distinct from model credentials, and verifies add/read/replace/read/delete/not-found using the same native storage implementation. No caller-supplied value or arbitrary secret identifier is accepted. No existing credential store or account cache is enumerated. Native access is noninteractive: locked/denied/unavailable storage fails closed without a password prompt or plaintext fallback. Missing secret is a typed result, not any generic read error. Reports contain only finite step/state codes and a generated diagnostic identifier, never values or raw errors.
3. **Honest account preparation display.** Extend the existing metadata-only acceptance report for OpenAI subscription accounts with exact-route readiness and three still-closed real phases. The existing account detail card displays the report, billing uncertainty and a Keychain-test explanation. It must say that this GET does not run Keychain tests and does not certify host storage or an account. No new navigation, polling, automatic execution or enabled real-login action.

## Keychain diagnostic lifecycle

The command requires an explicitly supplied new journal path outside the source checkout. Create an owner-only journal exclusively before touching Keychain; refuse symlinks/existing paths. The generated probe ID is the only credential identifier this journal can address; validate it strictly on recovery. Keep a process/file ownership lock so concurrent commands cannot clean an active check. Journal phase `pending` before the first write. A terminal report records `passed` or `failed`, and separately `cleanup: confirmed|unconfirmed`.

Always attempt to delete the generated item after the diagnostic, even after a partial write failure. Only a successful delete followed by a typed not-found read confirms cleanup. A denied/locked/unavailable read is not proof of deletion. Failure to write a terminal journal is a nonzero exit, never success. The journal never contains synthetic values, auth URLs or tokens.

After interruption, `--cleanup <journal>` performs only cleanup of that journal's exact diagnostic item. It never repeats the write/read/replace test or dispatches a provider request. An incomplete or unconfirmed journal is not a pass. A confirmed final journal can be read without repeating writes. There is no startup/background cleanup or retry. All ordinary unit tests use controlled storage doubles; the explicit native diagnostic tests only generated non-account data on the current Mac. Unsupported platforms report unavailable without fallback.

## Permissions, quota and recovery

- Keep the existing one-attempt claim, account/route/authorization/revision/digest binding, expiry, deny-on-storage-failure and running-to-unknown restart recovery.
- Local `extra_usage:off` is a declaration, not verified provider billing. Stage 0 records remote billing as `unverified` and quota as `unknown`. It never infers remaining balance from token usage.
- Local protocol tests cover identity-only grants, disabled/stale accounts, expired authorizations, unowned/wrong routes, quota exhausted, rate limits, permission failures, incomplete streams and lost completion writes. Denied paths must prove no credential access and no network call. No fixture success is relabeled live.
- No automatic retry, token refresh, repeated OAuth, model substitution, provider substitution or paid route fallback. An unknown call outcome remains consumed and cannot be replayed. Do not deliberately exhaust a real subscription to test errors.
- No scope broadening by model output. Testing a model is not approving task execution or changing frozen role-model snapshots.

## Verification and exit gates

Use tests first for each new behavior. Run focused tests, existing OpenAI protocol/approval/recovery regressions, all Go tests including race detector and vet, frontend tests and production build. Run the native Keychain diagnostic only with generated diagnostic credentials; report it separately from simulated storage faults. Check exact clean-source export, secret scanning and existing CI checks. Perform desktop and narrow-width browser acceptance on a fresh disposable fixture runtime, preserving existing workbench layout and the independent approve/execute controls.

Stage 0 is complete when these local checks pass and real access remains blocked. Subsequent live implementation review and explicit user authorization are still required for each of connection, catalog and one inference. No live-account or billing certification, additional platform support, GitHub push or Release is implied.

## Official protocol references

Checked 2026-10-19: [registration and scopes](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [models and completed inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), [credential and usage management](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions), [preview restrictions](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).

The subscription HTTP route requires `store:false` and `stream:true` and does not support `max_output_tokens`. Local time/size limits are not remote generation or billing caps. Foundry must register its own identity and must not import another application's credential cache. See [acceptance gates](model-acceptance-gates.md) and [preparation](model-acceptance.md).
