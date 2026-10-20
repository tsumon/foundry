# OpenAI connection approval handoff and human-login lifetime

## Release boundary

This increment connects the existing acceptance ledger to a browser-driven subscription connection session. It prepares the live workflow without opening the live gate. All executable acceptance paths remain constructor-owned, literal-loopback simulations with synthetic credentials. Normal Foundry still rejects real connection before provider traffic, host registration, callback binding or credential access. No environment variable, CLI flag, HTTP field or UI toggle enables it.

Real login and this machine's native Keychain check require separate future user confirmation. Directory queries, inference, automatic refresh, automatic retry and paid-API fallback remain closed. Kimi, scripts, Git writes, task approvals and deployment/database approvals are unchanged. The workbench navigation and independent proposal → decision → explicit execution remain intact.

## Approval and session contract

Reuse the existing acceptance operations and durable one-attempt claims. Do not create a second approval system or treat an account's connected state as authority.

- A constructor-owned subscription route can declare `connection_mode: browser_handoff`. This mode is server-owned, applies only to `subscription_only` connection material, and is bound into the material digest. Clients cannot request or override it.
- Freeze `login_timeout_seconds: 600` alongside the existing thirty-second network-operation bound, one attempt, exact account/route/revision, six scopes and resource. Old material omits both new fields and retains its historical digest and synchronous behavior.
- The ten-minute approval expiry limits when execution can begin. The separate ten-minute login window begins at explicit execution; approving does not start a timer, bind a socket, open a browser or read credentials.
- Execute atomically consumes the approval before starting the owned protocol session and promptly returns the running operation. It must not hold an HTTP request open during the human login window. A disconnected browser request does not cancel or duplicate the session.
- The service owns the session's cancellation, completion and shutdown. A callback may complete only this exact account, operation and claim. Repeated execution, competing operations for the same account, changed material/revisions and cross-account requests fail closed.
- A read-only session response may expose only sanitized state, expiry and an exact queryless `http://127.0.0.1:<port>/auth/start` handoff URL. No raw provider authorization URL, OAuth state/code/nonce/PKCE, tokens or secret-store references enter HTTP JSON, durable receipts, browser storage or logs. The handoff is ephemeral and is never reconstructed from history.
- Reading status is explicit and side-effect free. Opening the local handoff requires a separate user click. No automatic browser launch, polling, retry, refresh or execution occurs after approval or a status read.
- Cancellation is an explicit revision/digest-bound action on the running operation. It cancels and joins owned callback work before recording completion. A lost response is reconciled by reading, never by repeating the mutation.

The production adapter keeps its fixed official protocol profile. The browser-driven fixture uses the same owned SIWC connection core and credential lifecycle, not a fake success receipt. Its official `resource` is an audience string, not permission to contact a remote service. The closed production entry remains distinct from executable local simulation.

## Completion, evidence and recovery

The session is process-owned, not request-owned. Account and acceptance services must shut down/join active sessions before closing storage. Recovery first stops owned work, then reconciles durable operations; it never restarts OAuth or exchanges a code again.

| Event | Required result |
| --- | --- |
| Valid synthetic consent and proof | Persist fresh matching authorization, then complete the exact consumed acceptance operation; existing proof cannot satisfy it |
| User declines before exchange | Terminal rejected/not-sent evidence as appropriate; no usable subscription authorization |
| Login expires before exchange | Close the listener, clear the handoff, consume the attempt; do not claim a remote rollback |
| Cancellation or timeout during exchange/write | Cancel and join; mark unknown if completion is uncertain; retain exact cleanup intent |
| Callback after expiry/cancellation, duplicate callback | No second exchange and no late credential commit |
| Browser closes or loses execute response | Session continues only within its existing deadline; explicit read shows current state, no replay |
| Process restart | Previously running operation becomes consumed/unknown; no handoff restoration or automatic retry |
| Credential or receipt persistence failure | Fail closed; retain recoverable cleanup intent, never report success from an incomplete write |

Receipts keep local-simulation provenance, unknown quota, unverified remote billing and blocked live access. Requested permissions and actual grants remain distinct. `offline_access` never activates refresh. Local cleanup does not revoke provider consent, and success does not unlock a directory or inference operation. Uncertain outcomes need review and a fresh independently approved operation, not a repeat of the old one.

## Workbench behavior

Keep the connection controls in Settings → Models → account detail. Extend the existing approval material with the human-login and network limits. For a running handoff show a local-simulation label, absolute expiry, safe “open local authorization” action, explicit status read and cancellation. Terminal or missing sessions have no usable handoff action. Unknown or malformed responses disable mutations and require explicit reconciliation. Do not add navigation, charts or live login controls.

Use an additional disposable synthetic account for manual-browser acceptance so existing automated fixtures retain their meaning. Its authorization page must clearly identify itself as synthetic and never ask for a real email, password, OTP or account selection. The future real provider page, once separately authorized, is where the user will select their own account/workspace.

## Implementation sequence

1. Record this contract and the implementation/test plan before code changes.
2. Add server-owned material/route metadata, digest validation and an asynchronous owned connection controller over existing SIWC and acceptance services. Wire strict status/cancel HTTP operations and runtime shutdown. Keep the real entry closed.
3. Add the manual-browser local fixture and tests for approval/permission boundaries, one attempt, human timeout, browser disconnect, cancellation, late callbacks, storage failures and restart. Only synthetic credential-store doubles and loopback/offline transports are permitted.
4. Extend the existing account card/API validation without changing navigation or independent approval. Test malformed material/session responses, manual-only actions, lost responses and terminal presentation.
5. Run full tests, race checks, vet, frontend build, desktop/narrow browser acceptance, clean-source export and secret/dependency scanning. Publish only reviewed public source and inspect CI for the exact pushed commit. Do not publish private evidence or create a Release.

## Remaining live acceptance gates

These tests do not certify OpenAI eligibility, real subscription consent, native Keychain readiness, quota or billing. Before any real execution the user must separately confirm the requested subscription scopes, account/workspace selection, native storage check and a one-attempt connection approval. A later release must explicitly enable the reviewed live admission path; this release has no such switch. Directory, inference and refresh remain separate and unauthorized.

Protocol references, checked 2026-10-20: [official local-app sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in) and [errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery). The human-login lifetime is Foundry policy, not a claimed provider guarantee.
