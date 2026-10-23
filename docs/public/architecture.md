# Architecture

FOUNDRY is a local HTTP application with a deterministic Go controller and a React single-page UI. The production entrypoint is [`cmd/foundry`](../../cmd/foundry/main.go); the compiled frontend is served by [`internal/httpapi`](../../internal/httpapi), and durable state is managed by [`internal/store`](../../internal/store).

## Components

- **UI:** [`web/src`](../../web/src) renders tasks, settings, approvals, evidence, and downloads. Browser input is a request, not authority.
- **Controller:** [`internal/engine`](../../internal/engine) serializes planning/execution, checks immutable bindings, owns state transitions, and issues local artifacts.
- **Workspace:** [`internal/workspace`](../../internal/workspace) snapshots the selected committed Git tree, validates scope/protected paths, and manages immutable candidate generations. The selected source repository is never the edit target.
- **Model boundary:** [`internal/model`](../../internal/model) validates protocols, bounds responses, resolves legacy profiles or frozen account routes, and classifies provider outcomes. Secrets use the platform implementation in that package.
- **MCP boundary:** [`internal/mcp`](../../internal/mcp) implements bounded discovery/calls. [`internal/mcptest`](../../internal/mcptest) owns the only task-executable production fixture, enabled explicitly with `--local-mcp-test`.
- **Verification:** [`internal/sandbox`](../../internal/sandbox) runs approved acceptance commands in an already-present Docker image using `--pull=never`, `--network=none`, a read-only candidate mount, dropped capabilities, resource limits, and an unprivileged user.

The application database, lock, mission workspaces, sealed evidence, and exports live under `FOUNDRY_DATA_DIR`. The HTTP server accepts only literal `127.0.0.1:<port>` and defaults to `127.0.0.1:4317`.

## Approval graph

```text
create task
  -> freeze commit + manifest + scope + profiles/policy + budgets
  -> explicit context approval
  -> research/planning
       -> optional exact MCP proposal
       -> separate one-call approve/reject
       -> research resumes
  -> proposed plan
  -> separate plan-execution approval
  -> candidate edits + isolated verification + QA/Auditor reviews
  -> separate delivery approval
  -> local patch/report export
```

Each approval is bound to controller-computed digests and current revisions. Context approval does not authorize a tool call or execution. A one-call MCP grant does not authorize a second call, a plan, verification, or delivery. Plan approval binds the exact plan, frozen inputs, selected candidate generation, and resolved Docker image. Delivery remains a final explicit action after successful verification and review.

## Roles divide work, not permission

| Role | Current responsibility | Hard boundary |
| --- | --- | --- |
| Main | Explains goals, plans, evidence, and blockers | Chat text cannot approve execution or delivery |
| Researcher | Optionally inspects frozen repository facts read-only and cites real paths | Cannot execute code, expand authority, or browse the web |
| Planner | Produces an acyclic plan of at most six steps | Cannot approve or execute its own plan |
| Worker | Proposes complete file contents one plan step at a time | Limited to exact step files; no host shell or source-repository writes |
| Debugger | Proposes bounded repairs from failure evidence | At most two rounds; cannot delete tests or weaken acceptance criteria |
| QA | Reviews the current candidate and deterministic verification evidence | Cannot invent test results; insufficient evidence must fail |
| Auditor | Independently reviews changes, scope, safety, and evidence | Cannot write code or self-authorize delivery; may block delivery |

Role separation is not permission separation. The controller enforces every state transition, approval, budget, frozen binding, verification record, and artifact export.

## Immutable task snapshots

At task creation, FOUNDRY snapshots the committed Git base, in-scope manifest, task specification, model selection per role, and confirmed role policy. Account-backed selections include the exact account revision, route, credential generation or authorization binding, protocol, model, and provenance digest. Later settings or policy changes apply only to new tasks; they do not silently rewrite existing task authority.

If a stored model snapshot is invalid or has an unsupported version, startup enters a diagnostic read-only state instead of attempting repair or ordinary recovery. The controller verifies candidate-generation and artifact bindings before execution or delivery.

## Network capability is selective, not globally disabled

Production account routes marked `live` are blocked by default. The separately enabled [model-driven coding workbench](live-coding.md) admits only a durable claimed task invocation, exact frozen selection and sealed input under current context/execution authority. The router checks admission before credential access and again before dispatch, consuming a single dispatch. It does not open catalog/probe access or enable automatic paid fallback. Normal desktop launch does not install this admission capability.

Legacy accountless profiles are a separate compatibility path. They can make model-directory and inference requests to their configured provider when an approved workflow reaches that model operation. Remote endpoints require HTTPS and credentials; literal-loopback HTTP is allowed for local providers. Therefore the application must not be described as entirely offline.

External MCP connections can perform explicitly approved catalog discovery, but their tools are not eligible for task-time `tools/call`. The only task-executable MCP in this alpha is the exact controller-owned local test descriptor. Its fixture is not a model service.

The read-only model-account acceptance inspection is also not a network permission. It reports local account, route, credential-generation, and authorization metadata without reading secret values, contacting a provider, reserving a call, or changing state. Its `live_access: blocked` result remains configuration evidence only. See [model acceptance](model-acceptance.md).

## Failure, uncertainty, and recovery

Task-scoped model, repository-read, and MCP calls reserve identity and budget before dispatch. A call that was durably reserved but not dispatched can recover as interrupted-before-dispatch. Once dispatch may have begun, an incomplete or crashed call recovers as outcome unknown. Failure and unknown outcomes remain consumed; the controller does not automatically retry, replay, reconnect, substitute a provider, refund the budget, or claim remote rollback.

Recovery performs local reconciliation. The legacy runner can perform owner-scoped cleanup; controlled C4/coding tasks instead require separate explicit cleanup authority and an exact-owned container ID. Recovery does not initiate a model request, MCP request, new sandbox run, image resolution, Git operation, or delivery. A user must explicitly continue from a verified resumable checkpoint.

## C4-A/B synthetic verification boundary

[C4](coding-verification-c4.md) adds optional immutable profile/operation records
to the existing mission CAS store, not a second approval database. A C3-admitted
task prepares baseline material, explicitly approves it, then separately executes
it before context/planning. Baseline is never candidate evidence. Execute approval
also binds the profile and baseline; delivery still requires current candidate
evidence, C2 QA/Auditor references, and its own approval.

A deterministic command plan and bounded lifecycle executor separate injected
local simulation from the default-closed, strictly configured Docker adapter.
There is no HTTP/environment bypass, model-router escape or legacy-runner fallback.
Unknown/persistence/cleanup outcomes stop without replay; explicit cleanup checks
the separately known exact-owned ID only. Production startup with C4 records is
diagnostic/read-only unless it matches separately admitted controlled activation.
A historical, bounded Double-project C4-C acceptance exercised real tests and
exact-owned cleanup with simulated roles; it grants no future execution authority.

The disposable acceptance runtime uses a separate stdlib-only Go source fixture,
seven accountless synthetic role snapshots, a dedicated local model gateway and
a content-comparison fake driver. It never invokes Go or Docker for that fixture.
The existing decision panel, verification view and persisted activity stream show
these facts without new navigation. All real C4 readiness remains `not_checked`.

The isolated [repository-task mode](repository-task-orchestration.md) uses the
same store/controller/HTTP/workbench with a descriptor-only gateway and pure
frozen-content evaluator. Goals, nested paths, dependencies and full-file edits
are data; no account/router/MCP service or real runner is constructed. The existing
controller owns approvals, immutable generations, two repair rounds, failure
archives, independent review bindings and patch delivery. Existing-source imports
consume committed HEAD only, preserve dirty working files, and reject descriptor
drift without running Git status/diff or filters. This generalization applies only
to `synthetic`, control-version-0 tasks. The old controlled fixture remains
Double-only. A distinct `c4_go_model_v1` capability admits bounded committed Go
projects with account-backed model proposals, without preset descriptor solutions
or a fallback to the legacy runner. Its separate readiness/baseline and task
approvals compose the same immutable generations, tests and repair/review loop.
Local HTTP-plus-driver integration tests are not real-provider/Docker acceptance.

## Output boundary

Model responses, repository observations, MCP data, Skill text, and browser content remain untrusted. The controller validates bounded structured outputs before using them and seals evidence to exact invocation and binding identities. Candidate changes stay in controller-owned storage. Delivery exports a patch and report for human review; it never applies them to the source repository.
