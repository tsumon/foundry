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

Production credentials use Foundry-scoped macOS Keychain or Windows Credential Manager and must never be stored in SQLite, browser storage, logs, reports, or committed fixtures. Local integration tests use fake stores, not real accounts. A real-provider task requires separate operator authorization; do not rely on account, entitlement, billing, balance, or reset-time displays as verified production facts.

Account routes marked `live` remain blocked in normal mode. The separately enabled [coding workbench](live-coding.md) accepts only an engine-claimed invocation bound to the current mission, epoch, role, frozen selection and sealed input. It checks authority before credential access and again before one dispatch. This does not enable catalog probes or general router access. Startup/read-only views do not read, refresh or clean native credentials. The application is not globally offline: legacy accountless profiles retain their existing network capability, and explicit MCP catalog discovery can contact a selected endpoint. Development/evaluation tests use owned loopback fixtures.

There is no automatic paid fallback, provider substitution, or retry of an uncertain provider outcome.

## MCP and Skills

Saving or viewing an MCP connection does not contact it. Catalog discovery requires a separate explicit confirmation and is bounded. A failed refresh does not erase the last known successful catalog, and that old catalog is not proof that the current endpoint is healthy.

Generic external MCP execution is closed. Task-time MCP execution accepts only the exact controller-owned synthetic fixture enabled with `--local-mcp-test`, an immutable selected tool, controller-reviewed argument policy, and a one-call approval. The fixture cannot read arbitrary files, execute commands, use credentials, or call upstream services. Starting it does not supply a model fixture.

Skill registration stores bounded static text. Skill content does not run scripts, fetch links, grant a tool, or authorize a state transition.

## Built-in VM and v2 inputs

The controlled desktop defaults to a bundled Linux VM, with separate Go v1 and Node/TypeScript/Python v2 identities. V2 permits only single-package `node:test`, fixed TS typecheck then `node:test`, and Python `unittest`; Docker remains a separately selected Mac Go option. No unavailable profile can switch language, use a host runner or bypass virtualization requirements. Installed catalog eligibility is metadata, not readiness or native attestation.

Dependency import is a separate explicit read/copy/storage consent over an already prepared local pure-JS tree or exact locked pure wheels. It grants no test/model/context permission. Admission checks committed metadata and complete bounded payload, stores immutable identities and rechecks them before use. No guest npm/pip, install script, arbitrary native extension/plugin or monorepo is supported. User-supplied lock consistency does not establish publisher authenticity. See the [input walkthrough](desktop-guide.md#candidate-020-language-and-dependency-inputs).

Selected source is read-only; entire tests/support directories, manifests, locks, TS config and dependency payload remain protected. Editable scope cannot broaden through a root selection or model response. The static guest supervisor owns privilege, read-only mounts, control/FD separation and fixed resource policy before project loading; native gates must prove these actual controls. Complete authenticated response, exact EOF and confirmed owned termination/Close are required for retained native facts. A report alone cannot authorize a session.

Selected test-file counts are not completed cases. Compiler, setup/discovery, truncated or zero-case infrastructure failures cannot become passing tests or Debugger repair evidence. Readiness, baseline, context, execution and delivery keep independent approvals. [Scoped Mac native and TypeScript gateway acceptance](alpha-status.md) is recorded; old Go or synthetic receipts never extend that proof. Platform CI and final-package verification remain mandatory, and docs-only repackaging requires measured equivalence with original evidence attribution. Windows fixture models do not establish real-provider access.

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

The separate `c4_go_model_v1` coding profile composes real model transport with
these same Docker controls. It is not the old fixture capability with its
restrictions removed. A native startup opt-in permits configuration; an HTTP
request cannot enable this mode in the normal workbench. Inspected source,
immutable local image, protected inputs and per-task approvals still bind every
operation. Windows real Docker remains unavailable rather than using a weaker
executable/socket trust check.

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

FOUNDRY distinguishes known failure from an outcome that became uncertain after dispatch. Unknown model/MCP outcomes are not replayed automatically and their reserved budget is not refunded. Controlled C4/coding recovery performs local reconciliation without automatic sandbox cleanup; cleanup needs its own current authority and exact-owned ID. The legacy path can perform owner-scoped cleanup. Neither recovery initiates provider/MCP calls, new verification, image resolution or delivery.

Do not manually edit the SQLite database, controller workspace, sealed evidence, or exported artifact metadata to bypass a blocked state. Preserve the data directory for diagnosis and create a new task when frozen inputs legitimately change.

## Alpha operating rules

- Use a new disposable data directory.
- Use synthetic profiles and local fixtures for development acceptance; authorize
  real credentials, source disclosure and effects separately for real tasks.
- Use a disposable Git repository with no secrets.
- Do not reuse a personal provider configuration or existing workbench state.
- Do not expose the loopback service through a proxy or port forward.
- Do not weaken host/origin/session checks, approval gates, scope rules, network isolation, or `--pull=never` to make a demo pass.
- Review every exported patch outside the product before applying it manually.

See [alpha status](alpha-status.md) for unverified areas. Report suspected vulnerabilities through the GitHub private reporting channel described in [SECURITY.md](../../SECURITY.md), which is monitored by repository owner `tsumon`. Do not post secrets or suspected vulnerabilities in public issues.
