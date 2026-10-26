# Single-call diagnostics within a task budget

## Design and scope

The fixed-text subscription acceptance operation must not form a second,
unmetered model route beside a coding task. New production inference proposals
must name an existing task and its expected revision. The controller freezes the
task identity, epoch, revision, effective model snapshot, and used/max-call budget
into approval material. The selected task must be quiescent and bound to the
same account, route, authorization identity, and `gpt-6.1-sol` model. Preparation
and approval read metadata only: neither consumes a call nor leases a credential.

Eligible tasks are paused or blocked, with at least one effective schema-2 role
selection matching the current account, route, authorization binding and fixed
model. Other roles may use other providers. The full effective snapshot digest,
including any explicitly applied continuation, is frozen; an unapplied
continuation does not change the selection. Active invocation reservations,
pending/prepared local filesystem operations, running coding attempts, and
dispatched C4 operations/controls without results are blocked. A never-dispatched
C4 draft alone does not require VM cleanup or a self-test. Terminal unknown model
receipts remain consumed history, not an active request or a refundable allowance.

The existing request remains a single `POST /v1/responses`, with `stream:true`,
`store:false`, and `Reply exactly: FOUNDRY_INFERENCE_OK`. It contains no task goal,
repository source, history, tools, or baseline output. The request is diagnostic
only; it never resumes Researcher, Planner, workers, tests, or a VM.

Execution must atomically claim the acceptance operation and debit exactly one
call from the original task's existing maximum. It must reject a missing task
binding, stale revision/snapshot, changed account/authorization, an active task,
or an exhausted budget before any credential read or send. A durable operation
identity prevents duplicate debit and replay after concurrent clicks or restart.
The debit and acceptance claim either both commit or neither does. Any uncertain
send, transport failure, invalid response, or interrupted result retains that
debit; it cannot become a refunded call or automatic retry. The task's original
baseline, approvals, invocation receipts and evidence are not rewritten.

The receipt uses the same bounded exchange/transport/response diagnostic types
as task inference: random request correlation ID, phase observations, verified
certificate fingerprints, protocol/header classifications and parser outcome.
No raw credentials, headers, certificate names, network addresses, response
bodies or arbitrary error strings enter diagnostic evidence. Historical records
remain readable and are not backfilled. Old production inference material that
lacks a task budget binding is history, not executable authorization.
Legacy unbound records, including running ones, are not rewritten or finalized
by recovery. A legacy running claim continues to block unresolved account effects;
this feature neither repairs nor bypasses it. New bound interrupted claims recover
to unknown while retaining their committed debit and unique reservation.

HTTP 200 is not success. Missing or wrong Content-Type stays failed/unknown;
there is no body sniffing or accepting partial SSE. Existing completed-response,
model, assistant-content, credential-echo and output-match semantics remain
unchanged. A valid completed response with nonmatching output retains the existing
transport-success receipt and a separate `output_matched:false`; it is not retried.
Token usage remains unknown unless a validated provider receipt says
otherwise; a short prompt, response byte limit or timeout is not a provider-side
token or monetary cap.

## Workbench and approval boundaries

Keep the current settings/workbench layout. The existing single-call form gains
an explicit task budget binding and expected revision; its material shows the
task, current used/max budget, one-call charge, and fixed outgoing text. A result
shows the consumed budget and sanitized diagnostic summary. Missing, stale,
unsupported or malformed material is non-executable. Preparing, approving,
executing and reconciling remain distinct operations; rendering never refreshes,
polls, queries models, or dispatches a request.
The explicit task ID/revision fields do not choose a task automatically. The
prepared material shows the server-validated used/max budget and snapshot; the
receipt shows the charged budget at that operation, not a live budget refresh.

## Implementation and offline acceptance

1. Extend the versioned material/receipt contracts and task-bound validation;
   preserve old serialized digests by omitting new absent optional fields.
2. Atomically couple execution claim to the task budget and audit event, with
   durable replay guards and fail-closed pre-send revalidation.
3. Reuse bounded diagnostics in the fixed-text HTTP entry without changing its
   request, timeout, redirect/retry policy or response-success classification.
4. Wire the existing UI and schema validators to explicit budget selection and
   sanitized evidence. Keep historical operations readable but non-executable.
5. Use only synthetic accounts, temporary databases and local HTTP/TLS fixtures.
   Cover 3/7 remaining unchanged through prepare/approve and becoming 4/7 exactly
   once on execution; 7/7 denial; concurrent attempts; stale bindings; malformed
   diagnostics; missing media type; abrupt EOF; failed persistence; restart; and
   unchanged historical baseline/evidence. Run Go/race/vet, web, desktop and
   isolated synthetic UI checks. No real account, renewal or local VM is used.
6. After review and successful checks, push only public source, check the existing
   GitHub workflows, and install the exact verified Mac artifact while the app
   remains closed. Original user data is not opened by an application process.

This change does not enable a real call. New production use still requires a
valid credential, valid prerequisite evidence, a current task binding and an
independent execution approval. Correlation can support a separately authorized
provider inquiry; a client-side trace alone cannot establish which remote
gateway omitted a response header.

## Desktop activation correction

The desktop coordinator must explicitly prepare the `inference` phase before
the existing single-call form can propose material. Operator startup flags are
not a substitute: desktop mode rejects operator flags, and the non-desktop
recovery path rejects coding-task databases. Neither restriction is relaxed.

Add a metadata-only "independent diagnostic preparation" action beside the
existing connection and catalog actions. It constructs the same production
inference service with the host-provided credential resolver, retires previous
in-process authority, and preserves active-session and concurrent-preparation
exclusion. Preparation does not lease credentials, probe Keychain, query the
catalog, dispatch inference, resume a task or start a VM. No authority survives
a process restart. The separate proposal, approval, one-call budget debit and
execution checks remain unchanged.

Implementation and acceptance order:

1. Reproduce the missing coordinator, HTTP and UI wiring in offline regressions.
2. Add the inference phase, explicit readiness metadata and preparation button;
   keep unsupported phases, stale account revisions and foreign routes denied.
3. Test pending denial, independent approval, one dispatch, 3/7 to 4/7 debit,
   duplicate rejection, phase replacement, restart and missing-media failure.
4. Run Go/race/vet, frontend and desktop tests and synthetic interface acceptance.
   Publish only reviewed source and use the verified installer. A real diagnostic
   still needs current prerequisite evidence and its own user authorization.

When a pinned upstream source host rate-limits packaging, the desktop workflow
can be manually dispatched with `runtime_sources_run_id` naming a previously
verified same-repository `foundry-runtime-sources` artifact. This is an explicit
input selection, not an automatic retry or network fallback. GitHub archive
digest mismatch is fatal. Both runtime and installer inputs are then reverified
using this checkout's pinned manifests, byte limits, hashes and canonical
archive checks before a fresh sidecar is produced. Artifact-supplied manifests
do not set expected hashes. Missing, expired or changed inputs stop packaging;
all downstream build, installation and security checks remain required.
