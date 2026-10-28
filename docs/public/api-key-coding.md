# API Key coding

API Key is the primary setup path for new coding tasks. Subscription sign-in
remains experimental and is not a prerequisite. Existing subscription accounts,
task snapshots, invocation budgets and evidence remain unchanged.

Explicit compatible Responses gateways are also supported using a `custom`
account and `compatible_api_responses` route. Each task freezes the receiver,
credential version and model; it never tries a different upstream on failure.
Remote endpoints require HTTPS. HTTP is limited to literal `127.0.0.1` with a
fixed port for user-owned gateways such as Magpie. Gateway model names are user
supplied, not evidence of the identity or billing of the upstream service. Foundry
cannot enforce the gateway's internal routing; use a pinned provider entry if
upstream fallback must be excluded. No app configuration is imported automatically.

## Connection and authority

1. Create an OpenAI account at `https://api.openai.com/v1`, or a Custom account
   with the exact compatible Responses base URL, declaring API credits or
   metered API billing. Save a new key version in Foundry's native credential
   store (macOS Keychain / Windows Credential Manager).
2. In API quick setup, explicitly choose that account and enter a model ID
   available to the API project. A ChatGPT subscription model listing does not
   prove API availability. The setup saves a Responses route and model profile;
   it does not log in, discover models, run a probe or send an inference.
3. Choose the saved profile in role settings for new tasks. Existing tasks keep
   their original model snapshots and consumption; no subscription task is
   converted to paid API automatically.
4. Import a supported standard-library Go repository with protected tests. The
   existing readiness, baseline, shared-context, execution and delivery approvals
   apply. Tests run in the selected isolated backend, never on the host.

The API uses Bearer authentication on the controller, `POST /v1/responses`,
`store:false` and the existing per-call `max_output_tokens`. Keys are never put in
model prompts, browser storage, reports, public sources or frontend configuration.
The API request allowance bounds dispatches, not money or the provider's billing.
Set provider-side project spending limits separately. No automatic retries, model
substitution, route fallback or subscription-to-API billing switch is permitted.

## Failure and recovery

Saving a route and saving a profile are separate local operations. If an outcome
is uncertain, setup stops and offers a read-only reconciliation of saved metadata;
it does not replay either write. A partially saved route is retained and reusable.
Credential writes retain their existing append-only operation reconciliation.
Revoked credentials, quota errors, incomplete or ambiguous responses and unknown
transport outcomes stop the task; they cannot become successful evidence.

Subscription login is available under an explicitly expanded experimental section
for existing users. Its previous transport issue is not claimed to be fixed by
switching protocols. The original task budget is not reset or reused by API setup.

## Verification and limits

Local protocol fixtures cover API dispatch through the production router and
engine: Researcher, Planner, Worker, failed candidate, Debugger, recheck, QA,
Auditor and sealed delivery. Native installer checks are separate from real
provider acceptance. A working local fixture is not a successful paid API call.
The [2026-10-28 Mac acceptance](compatible-api-acceptance.md) completed a real
CPA/Grok-backed code change, VM tests, QA/Auditor review and sealed delivery in
five successful calls. The candidate passed on its first run; real-provider
Debugger repair remains untested. This does not certify every compatible gateway
or the separate experimental subscription transport.

Protocol references: [authentication](https://developers.openai.com/api/reference/overview),
[Responses](https://developers.openai.com/api/reference/resources/responses/methods/create).
