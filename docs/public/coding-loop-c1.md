# C1 content-driven coding loop

C1 adds a deterministic local acceptance scenario for Foundry's controlled
coding loop. The scenario starts from a disposable, committed repository and
uses the normal engine APIs for planning, human plan revision, execution
approval, candidate generation, verification, review, delivery approval, and
patch export.

The fixture deliberately does not execute a command. Its runner reads the
selected candidate's `src/main.txt` and reports success only when the file has
the expected repaired content and the frozen acceptance command matches. The
first Worker edit is intentionally wrong; the recorded failure is then given
to the Debugger, whose fixture response is refused unless its prompt contains
the latest matching candidate change and failed command result. The repaired
edit is then checked again. Repeating the unchanged wrong candidate continues
to fail, so neither call order nor model text can turn a failure into success.

The acceptance scenario uses the versioned `generations_v1` candidate mode and
stops before each human authority boundary. Creating the scenario leaves a
draft waiting for context approval; it neither generates nor approves a plan.
A test grants context, revises the generated plan through `RevisePlan`, approves
execution, waits for QA and Auditor review, then confirms the mission is
awaiting delivery. Only an explicit `Deliver` call exports a patch; the
committed source repository remains unchanged.

Acceptance coverage includes the successful repair path plus refusals for an
unchanged Debugger edit, QA rejection, Auditor rejection, protected acceptance
files, stale approvals, candidate tampering, persistence failure, and malformed
or unknown model output. Existing recovery tests continue to prove that an
interrupted saved result is not automatically replayed after restart.

This is a local simulation contract, not evidence of a real compiler, shell,
container, cloud model, subscription, account, or credential flow.

## Trusted verification provenance

New verification records carry `source_version: 1` and a controller-written
`source_kind`. The existing Runner signature is unchanged: an optional
process-side `VerificationSource(imageID)` provider declares the runner code
path. Docker declares `isolated_execution`; the acceptance wrapper declares
`synthetic_content_check` for its content-check branch and `synthetic_stub` for
its fallback. Missing or unsupported provider values become `unknown`. Image
names, model text, and browser JSON cannot declare isolated execution.

The workbench's existing cards, decision summaries, and new delivery reports
separate source from outcome using these labels:

| Source | Display |
| --- | --- |
| `synthetic_stub` | 流程模拟（未执行命令） |
| `synthetic_content_check` | 内容检查模拟（未执行命令） |
| `isolated_execution` | 隔离命令执行 |
| missing, unknown, or unsupported version/value | 来源未记录 |

Simulation success/failure is labeled 模拟验证通过 / 模拟验证未通过. Unknown
records say 验证记录通过 / 验证记录未通过, never that a command ran. Mixed
attempts retain each record's source; a later simulation cannot inherit an
earlier isolated-execution label. Source is evidence provenance, not a new
permission or an additional approval: explicit simulated deliveries remain
available through the same context, execution, and delivery approvals.

New controller reviews bind the complete evidence array, including raw source
version/kind, through `evidence_digest`. Source edits, removal, unknown versions,
and added mixed-source attempts invalidate those reviews. Export intents and
reports also bind this material, and committed generation exports reject a
changed evidence/report binding on download.

Compatibility deliberately has a small cost: absent/zero provenance and review
digest fields stay omitted, preserving historical JSON and seals. Historical
records remain unknown, with no automatic backfill. New exports always include
source summaries. An already prepared legacy export can resume only when all
source fields and review digests remain zero and its exact historical report,
intent, and prepared report input match. Its bytes are reused, not rewritten or
resealed. This exception cannot strip source from new records or accept a
modified old report. Previously committed legacy reports likewise retain their
original bytes; their lack of provenance is not evidence of command execution.

## Implementation and acceptance plan

1. Add the content-driven fixture described above, expose its draft mission in
   the acceptance manifest, and prove the failure/repair/review/export path plus
   its negative and restart cases with Go tests.
2. Add versioned verification-source metadata to new records, preserve old
   records as unknown, bind the source to delivery authority, and show it in the
   workbench and exported report. Implemented with source-trust, mixed/unknown
   records, model/browser spoofing, review/export binding, and legacy prepared
   export regressions. C1 content-check output alone is not trusted provenance.
3. Make `generations_v1` the default only for newly created ordinary UI tasks,
   retaining historical and specialized mode behavior. This is also future
   work until its UI tests and acceptance checks land.

Final acceptance requires focused Go tests for each boundary, the repository's
full Go and frontend suites, a clean public export, and a fresh local browser
walkthrough. Any Docker, native Keychain, real-provider, or paid-subscription
check remains explicitly skipped rather than inferred from this fixture.
