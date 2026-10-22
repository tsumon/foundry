# Safety model

FOUNDRY is experimental software for local source evaluation. It is not safe to treat model output, tool metadata, a successful build, or a green test as authority to modify or publish a repository.

## Trust boundaries

- The deterministic controller is the authority for state transitions, budgets, immutable bindings, and approvals.
- Model text, MCP responses, Skill files, repository content, provider metadata, and browser input are untrusted data.
- Context, an exact single MCP call, plan execution, and delivery use separate approvals. One never implies another.
- The selected source repository is read-only to the workflow. Candidate files live under the controller data directory, and delivery produces local patch/report artifacts only.
- Model selections and role policies are frozen per task. Changing settings affects future tasks, not existing task authority.

The implementation details are summarized in [architecture](architecture.md).

## Credentials and accounts

Production credentials are designed for macOS Keychain and must never be stored in SQLite, browser storage, logs, reports, or committed fixtures. This alpha has not been tested or certified with real provider accounts. Do not enter real API keys, approve real subscription access, or rely on account, entitlement, billing, balance, or reset-time displays for production decisions.

New account routes marked `live` are blocked before secret access/network dispatch in the production router. That is not a global offline guarantee: legacy accountless profiles retain their existing ability to contact configured HTTPS providers, and explicit MCP catalog discovery can contact a selected endpoint. Use only synthetic or intentionally isolated local endpoints during alpha evaluation.

There is no automatic paid fallback, provider substitution, or retry of an uncertain provider outcome.

## MCP and Skills

Saving or viewing an MCP connection does not contact it. Catalog discovery requires a separate explicit confirmation and is bounded. A failed refresh does not erase the last known successful catalog, and that old catalog is not proof that the current endpoint is healthy.

Generic external MCP execution is closed. Task-time MCP execution accepts only the exact controller-owned synthetic fixture enabled with `--local-mcp-test`, an immutable selected tool, controller-reviewed argument policy, and a one-call approval. The fixture cannot read arbitrary files, execute commands, use credentials, or call upstream services. Starting it does not supply a model fixture.

Skill registration stores bounded static text. Skill content does not run scripts, fetch links, grant a tool, or authorize a state transition.

## Docker verification

This section describes the legacy verification path. The versioned
[C4-A/B path](coding-verification-c4.md) is default-closed in production and never
falls back to that path. Only the disposable acceptance runtime injects its local
synthetic driver/models. `synthetic_stub` results are content-comparison records,
not Go/compiler/Docker execution or real readiness. A separately approved,
bounded Double-project C4-C acceptance did run real tests with simulated roles;
its baseline/failed/repaired evidence and confirmed cleanup are historical only,
not consent for any new project, image, host or execution.

C4 separately approves baseline material, then explicitly executes it; this is
not context, candidate-plan or delivery approval. Its fixed declaration adds an
explicit 256 MiB executable `/foundry-build` scratch permission while keeping
the root/candidate read-only and `/tmp` noexec. Static flags do not prove kernel
isolation. Unknown results and unconfirmed cleanup stop without retry/repair.
Separately requested cleanup checks only a known exact-owned ID and cannot replay
execution, turn unknown/failure into success, or authorize delivery. Production
startup without matching admitted controlled activation preserves diagnostic
read-only access rather than probing Docker or resuming it. No environment or
HTTP enable override is provided; strict startup configuration and separate
readiness/baseline/execution approvals remain required for real controlled use.

The [repository-task launcher](repository-task-orchestration.md) is an isolated
synthetic-only entrypoint. Protected descriptor text supplies proposals, never
permissions or executable commands. Assertions are frozen at import; candidate
changes cannot replace them. Only declared scoped source edits/additions are
supported, never protected tests/build inputs or deletion. Optional existing-path
import reads committed objects, ignores dirty/untracked bytes, preserves source
and index, and does not execute Git status/diff, hooks or conversion filters.
No account, router, Keychain, MCP, Docker or compiler is used by this mode.

Legacy execution/verification requires a running Docker daemon and the exact trusted image already present locally. FOUNDRY does not pull images. It runs the approved command with networking disabled, a read-only candidate bind mount, a writable restricted `/tmp`, no health check, no log driver, dropped Linux capabilities, `no-new-privileges`, an unprivileged user, and CPU/memory/process limits.

These restrictions reduce risk; they are not a proof that Docker, the image, the host kernel, the acceptance command, or the candidate is safe. Do not mount secrets or the Docker socket into the container. If cleanup cannot be confirmed, the controller blocks rather than assuming the sandbox stopped.

## Failure and unknown outcomes

FOUNDRY distinguishes known failure from an outcome that became uncertain after dispatch. Unknown model/MCP outcomes are not replayed automatically and their reserved budget is not refunded. Restart recovery performs local reconciliation and owner-scoped cleanup only; it does not contact providers, repeat MCP calls, start verification, resolve an image, or deliver artifacts.

Do not manually edit the SQLite database, controller workspace, sealed evidence, or exported artifact metadata to bypass a blocked state. Preserve the data directory for diagnosis and create a new task when frozen inputs legitimately change.

## Alpha operating rules

- Use a new disposable data directory.
- Use synthetic profiles and local fixtures only.
- Use a disposable Git repository with no secrets.
- Do not reuse a personal provider configuration or existing workbench state.
- Do not expose the loopback service through a proxy or port forward.
- Do not weaken host/origin/session checks, approval gates, scope rules, network isolation, or `--pull=never` to make a demo pass.
- Review every exported patch outside the product before applying it manually.

See [alpha status](alpha-status.md) for unverified areas. Report suspected vulnerabilities through the GitHub private reporting channel described in [SECURITY.md](../../SECURITY.md), which is monitored by repository owner `tsumon`. Do not post secrets or suspected vulnerabilities in public issues.
