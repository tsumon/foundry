# Model-driven coding workbench

## Delivery goal and verification boundary

This work connects the existing account-backed model router, immutable repository
imports, candidate generations and fixed language sandbox verifiers in one desktop
task. It does not replace the workbench or treat installation as evidence of a
successful real-model task. One delivery milestone covers the whole path.

Real credentials and provider calls require the operator's separate permission.
Development and CI use owned loopback model servers, fake credential stores and
synthetic process boundaries. Separate native acceptance uses the production
VM runtime with the same loopback model fixtures: those are real Linux tests,
not real provider calls. Actual provider and sandbox acceptance are separate observations,
never inferred from configuration, an installer, a model name or a green fixture.

## Architecture

The normal desktop launch remains closed. A deliberate native-menu entry opens a
controlled coding workbench; this enables configuration, not model traffic or
sandbox commands. The renderer keeps its current sandbox, local capability header,
origin checks, download guard and lack of a Node/filesystem bridge. Account
metadata and credentials remain separate. Only an explicit credential operation
may use the native Foundry namespace; startup and read-only views must not read,
enumerate, refresh or clean up credentials.

The new task path accepts an inspected committed repository, a selected
account-backed role snapshot, a frozen built-in VM identity (default) or explicit
Mac Go Docker profile, and the selected fixed verification policy. It cannot consume descriptor-supplied solutions. The old
synthetic paths and their evidence remain unchanged. A separate model-backed C4
capability composes the router with the existing controlled verifier; globally
removing synthetic restrictions or falling back to the legacy runner is forbidden.

Live coding supports OpenAI and explicitly configured compatible Responses
gateways, with [API Key setup](api-key-coding.md) as the primary path and existing
subscription sign-in retained as experimental.
Tasks use an explicitly selected API-key route or an already authorized
subscription route. Choosing the subscription
route never authorizes the API-key route. No login, catalog lookup, token refresh,
provider retry or account/model substitution happens as a task side effect.
Existing connection/catalog/single-inference acceptances are not task approval.

## One task, separate authorities

1. V2 dependency-copy consent imports pre-existing local inputs separately. Committed-source inspection then freezes baseline, protected tests/support/config/locks, dependency bindings and context manifest. Neither action sends source to a provider or grants execution.
2. Sandbox readiness and baseline execution retain their separate material,
   approval, dispatch, result and exact-owned cleanup records.
3. Context approval binds the repository, selected accounts/models, context and
   call budget. Researcher and Planner may then produce proposals.
4. Execution approval binds the current plan revision, file scope, model snapshot,
   selected candidate and fixed test policy. Workers modify only isolated
   candidate generations. A successful passing baseline remains required.
5. Each candidate verification retains its own approval-bound execution facts.
   Only a confirmed test failure with confirmed cleanup may enter the existing
   at-most-two-round Debugger loop. Infrastructure failure is not a repair prompt.
6. QA/Auditor see the candidate changes and recorded tests. Delivery still requires
   its own approval and exports a patch/report; no source checkout or Git write is
   implicit.

Model transport requires an engine-created, durable, claimed invocation. Validate
the mission, epoch, role, current authority, frozen selection and sealed input
before credential resolution and again before sending. Consume at most one
dispatch for an invocation. A URL, role label, renderer checkbox or unclaimed
request is not transport authority. Denied, stale, expired or malformed authority
must produce no corresponding model/sandbox effect.

## Data, budgets and failure recovery

Reuse the existing full before/after changes, candidate hashes, model input/output
envelopes, request receipts, token usage and generation transaction journal.
Worker/Debugger/review prompts must consistently identify the current candidate
and bounded failure evidence; original baseline context alone must not be
presented as current code. Output remains strict structured data, with exact file
scope and protected-path enforcement; model text cannot execute a shell command.

Keep a finite task call budget, bounded prompt/response sizes, per-call timeouts
and the two-repair limit. Token usage is not remaining subscription quota. Do not
claim a monetary hard cap or output-token enforcement where the selected protocol
does not provide it. Show those limitations before context approval.
In particular, the [OpenAI subscription preview](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
requires streaming, non-stored requests and omits `max_output_tokens`; a client
call/timeout limit must not be presented as provider-enforced output or cost control.

Authentication expiry, allowance exhaustion, rate limits, transport ambiguity,
invalid model output, changed authority or unconfirmed sandbox cleanup stop the
task. Unknown outcomes retain the consumed call reservation. Restart records
interruption and never replays a model call, refreshes a token, reruns tests or
cleans sandboxes automatically. Recovery preserves all prior evidence and needs
fresh applicable authority; it never manufactures a pass.

A committed model-backed delivery is terminal for new model chats, readiness or
cleanup controls. Its frozen reports/patches remain readable; continuing work
requires a new task. This prevents later control preparation or a changed epoch
from invalidating the delivered review and provenance bindings.

## Supported project and platform limits

Released 0.1.0 uses the pinned Go 1.27.3 standard-library Linux contract. Candidate 0.2.0 adds closed single-package Node `node:test`, TypeScript fixed typecheck then `node:test`, and Python `unittest`, with explicitly imported frozen pure-JS lock-v3 trees or exact locked pure wheels. Tests/helpers, manifests, locks, config and dependency payload are protected. No guest network, package/install scripts, native extensions/plugins, monorepos or other frameworks are admitted. Existing non-root resource limits and import restrictions remain. See the [exact input contract](desktop-guide.md#candidate-020-language-and-dependency-inputs). Typecheck/setup/discovery and zero-case infrastructure errors stop without repair; catalog availability is not completed readiness or tests.

Native credentials on supported desktop platforms must use OS-backed Foundry-only
storage, not plaintext files. The default [built-in VM](embedded-sandbox.md)
supports Apple Silicon and Windows x64/WHPX. Its readiness/permissions do not
authorize Docker. Windows Docker remains unavailable; no host or software-emulation
fallback is allowed. Packaging and successful native execution are separate claims.

## Acceptance

- Owned local HTTP server exercises real protocol encoding/decoding through the
  production router and engine: research/plan, worker, failed candidate, debugger,
  recheck, QA/auditor, delivery. No descriptor supplies the production solution.
- Negative tests cover every approval boundary, replay, changed selection/input,
  cancellation, expiry, quota, malformed output, interrupted calls, protected-file
  edits, failed cleanup and restart without side effects.
- Existing synthetic histories still render and retain their original provenance.
- The installed app renders setup, decisions, model provenance, tests and recovery
  without exposing credentials. Windows and Mac packages undergo platform-specific
  checks, not merely cross-compilation.
- Source/security checks, the installed v1 plus twelve-case v2 native matrix and exact-commit CI must pass before candidate artifacts are described as accepted. Actual candidate native/installer/UI/upgrade/provider/CI results remain pending. Historical [Mac Go provider acceptance](compatible-api-acceptance.md) stays scoped to its original version/projects. Hosted Windows fixtures cannot prove access to the Mac loopback CPA route. New provider attempts require their own bounded authorization.
