# Repository-task orchestration (local simulation)

## Design and boundary

This increment removes the Double-specific goal, filename and arithmetic from
the **local simulation path**, not the restrictions on real execution. It reuses
the existing C2/C3/C4 controller: frozen repository import, baseline approval,
context approval, editable plan, execution approval, immutable candidate
generations, bounded repair, evidence-bound QA/Auditor and delivery approval.
The existing workbench remains the UI. No new account, provider, tool, script,
Docker or Git-write capability is enabled.

The simulation accepts a bounded, protected `foundry.task.json` in a committed
Go repository. It describes a goal, proposed plan, full-file Worker edits, up to
two proposed repair rounds, and exact-content assertions. These are **scripted
local proposals and content comparisons**, not model reasoning, compilation or
Go test results. The controller, not the descriptor, owns permissions and state.
Repository text and failure output never become executable instructions.

The descriptor and protected tests/config are part of the immutable imported
context. A changed descriptor, changed scope, unknown schema, malformed JSON,
duplicate keys, missing content, unsafe path or unsupported proposal blocks the
task. No fallback to Double, a model router or a real runner is permitted.
Only `mode=synthetic`, `control_version=0`, accountless role configurations may
use this path. The separately gated real C4 controller is unchanged.

## Task contract

Schema 1 carries `schema_version: 1`, `goal`, `summary`, `scope`, `protected`, `steps`, `repairs` and
`checks`. Each step names an ID, title, description, dependencies and full-file
edits (`path`, `content`). Plan file lists derive from edits. Each repair is a
list of edits; checks are exact expected file contents. Bound the descriptor to
128 KiB, at most six acyclic steps, 30 distinct editable files, two repair rounds
and 30 assertions. Every edited file must have an assertion, every assertion
must name an editable file, and every edit must pass the existing path policy.
Required tests and module/build inputs remain protected. The descriptor itself
must be explicitly protected; the importer adds it to readable scope when needed.
The existing importer also enforces its 30-context-file/200-KiB context limits,
which include the descriptor and any protected files under the selected scope.
There are no command,
credential, permission, execution-success or arbitrary tool fields.

The same data format works for nested paths and multiple files without editing
the gateway. Worker proposals select only files in the current approved step;
revisions may regroup/rename steps or narrow the proposed files. A new scoped
source file is supported only when declared by the frozen descriptor with a
Worker edit and an assertion. Undeclared plan paths stop rather than silently
borrowing an old proposal. File deletion is unsupported. A repair must
reference the current failed verification/candidate and only the approved plan
files. Equal-content or out-of-scope repairs cannot trigger a repeat dispatch.

## Failure, review and recovery

The baseline compares the immutable imported contents. Candidate verification
compares the candidate against the frozen assertions, never assertions reread
from a mutable candidate. A baseline pass is not a candidate pass. Verification
counts describe simulated content checks and retain `synthetic_stub` provenance;
no check is represented as an actual compiler or Docker test.

Only a recorded, current `test_failed` result with confirmed simulated cleanup
may enter the existing maximum two repair rounds. Missing/stale/cross-task
evidence, unknown outcomes, infrastructure or persistence failure stops.
An exhausted repair sequence stays blocked. Explicit replanning freezes and
references the prior failure bundle; unknown outcomes remain unresolved. Old
evidence and candidate generations remain unchanged and cannot authorize a new
epoch. QA and Auditor independently return the existing controller-bound review
contract; passing simulated review is never proof of execution.

Startup recovery does not replay unfinished work. Existing cancellation fencing,
local-operation recovery, exact candidate digests and separate export approval
remain authoritative. A delivered artifact is a local patch/report only; the
source repository is not modified, committed, pushed or deployed by a task.

## Acceptance and historical status

Use a separate accountless disposable acceptance mode with two non-Double
repositories: a nested-path formatting task and a dependent, multi-file pricing
task. Exercise pending approvals, plan revision, Worker failure, repair and
reverification, failure exhaustion, explicit replan, stale authority, protected
files and recovery. An optional explicit repository path may be imported
read-only into this same simulation; its descriptor is not permission to execute.
The pricing fixture modifies `internal/pricing/price.go` and creates the declared
`internal/pricing/format.go`; the labels fixture modifies a different nested path.

## Running the local demo

With the documented Go toolchain, cached Go dependencies and an already built
`web/dist`, run from the source root:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go run ./cmd/foundry-acceptance --repository-tasks-fixture --web-dir web/dist
```

Open the printed loopback URL. Each launch creates a new disposable store and
two committed fixture repositories. Both tasks start with pending, unapproved
baseline material. Approve baseline, explicitly execute it, approve context and
planning, inspect/edit the proposed plan, separately approve execution, inspect
the failed check/repair/new generation and two reviews, then explicitly deliver.
The exported patch/report stays under the disposable runtime root. No source
repository is changed by execution or delivery; fixture commits occur only during
seeding newly allocated demo repositories.

To import an existing local repository's committed HEAD instead:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go run ./cmd/foundry-acceptance --repository-tasks-fixture --repository-task-path /absolute/path/to/repository --web-dir web/dist
```

This is a **committed-snapshot-only** import, not a clean-worktree certification.
Dirty and untracked working files are ignored and preserved. Commit intended
inputs first, or prepare a separate disposable repository. The bounded regular
working `foundry.task.json` must exactly match the inspected committed descriptor;
a mismatch blocks import. No `git status`/`git diff`, conversion filter, hook,
network Git operation, or source index write is used. Existing committed-object
admission and import-race checks remain authoritative. The optional path flag is
invalid without the repository mode; repository mode and `--c4-controls-fixture`
are mutually exclusive. No account service, model router, Keychain or MCP service
is constructed by this mode.

Minimal descriptor (requires committed `go.mod`, `go.sum` and protected tests;
source contents and assertions below are illustrative data, not commands):

```json
{
  "schema_version": 1,
  "goal": "Normalize labels",
  "summary": "Scripted nested-path normalization",
  "scope": ["internal/label"],
  "protected": ["foundry.task.json", "go.mod", "go.sum", "internal/label/label_test.go"],
  "steps": [{
    "id": "normalize", "title": "Normalize labels",
    "description": "Replace the source with the proposed text",
    "depends_on": [],
    "edits": [{"path": "internal/label/label.go", "content": "package label\n\nfunc Normalize(s string) string { return s }\n"}]
  }],
  "repairs": [],
  "checks": [{"path": "internal/label/label.go", "content": "package label\n\nfunc Normalize(s string) string { return s }\n"}]
}
```

The gateway proposes the declared bytes; it does not infer how to solve arbitrary
goals. Replanning maps the sealed failure IDs to the scripted steps but does not
invent a new solution. A descriptor with insufficient or ineffective repairs
stops; unknown outcomes stay explicitly unresolved. A narrowed human plan can
fail unchanged frozen assertions and does not silently weaken them.

## Historical acceptance boundary

Earlier bounded C4-C acceptance did execute the disposable Double project's Go
tests in the explicitly approved local Docker image: baseline passed, initial
candidate failed, repaired candidate passed, and exact-owned cleanup was
confirmed. Those agents were local simulations. Original evidence and sealed
exports are retained unchanged. This is not proof of general repositories,
real-model coding, every Docker host, or any future execution authority.

This increment uses local simulation only. Subscription integration remains
paused. Real Docker, real accounts, refresh, retries and paid API fallback remain
outside this increment; existing default-closed production gates are unchanged.
