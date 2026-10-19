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

## Immutable task snapshots

At task creation, FOUNDRY snapshots the committed Git base, in-scope manifest, task specification, model selection per role, and confirmed role policy. Account-backed selections include the exact account revision, route, credential generation or authorization binding, protocol, model, and provenance digest. Later settings or policy changes apply only to new tasks; they do not silently rewrite existing task authority.

If a stored model snapshot is invalid or has an unsupported version, startup enters a diagnostic read-only state instead of attempting repair or ordinary recovery. The controller verifies candidate-generation and artifact bindings before execution or delivery.

## Network capability is selective, not globally disabled

Production account routes marked `live` are gated before secret access or network dispatch with `live_provider_access_pending_confirmation`. The production router has no option that enables those live routes and has no automatic paid fallback.

Legacy accountless profiles are a separate compatibility path. They can make model-directory and inference requests to their configured provider when an approved workflow reaches that model operation. Remote endpoints require HTTPS and credentials; literal-loopback HTTP is allowed for local providers. Therefore the application must not be described as entirely offline.

External MCP connections can perform explicitly approved catalog discovery, but their tools are not eligible for task-time `tools/call`. The only task-executable MCP in this alpha is the exact controller-owned local test descriptor. Its fixture is not a model service.

## Failure, uncertainty, and recovery

Task-scoped model, repository-read, and MCP calls reserve identity and budget before dispatch. A call that was durably reserved but not dispatched can recover as interrupted-before-dispatch. Once dispatch may have begun, an incomplete or crashed call recovers as outcome unknown. Failure and unknown outcomes remain consumed; the controller does not automatically retry, replay, reconnect, substitute a provider, refund the budget, or claim remote rollback.

Recovery performs local reconciliation and, where previously approved execution may have left a container, owner-scoped Docker cleanup. It does not initiate a model request, MCP request, new sandbox run, image resolution, Git operation, or delivery. A user must explicitly continue from a verified resumable checkpoint.

## Output boundary

Model responses, repository observations, MCP data, Skill text, and browser content remain untrusted. The controller validates bounded structured outputs before using them and seals evidence to exact invocation and binding identities. Candidate changes stay in controller-owned storage. Delivery exports a patch and report for human review; it never applies them to the source repository.
