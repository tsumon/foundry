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
container, cloud model, subscription, account, or credential flow. Later work
may attach trusted runner-source metadata without changing the runner method
signatures introduced here.

## Implementation and acceptance plan

1. Add the content-driven fixture described above, expose its draft mission in
   the acceptance manifest, and prove the failure/repair/review/export path plus
   its negative and restart cases with Go tests.
2. Add versioned verification-source metadata to new records, preserve old
   records as unknown, bind the source to delivery authority, and show it in the
   workbench and exported report. This is future work until its implementation
   and tests land; C1 content-check output alone is not trusted provenance.
3. Make `generations_v1` the default only for newly created ordinary UI tasks,
   retaining historical and specialized mode behavior. This is also future
   work until its UI tests and acceptance checks land.

Final acceptance requires focused Go tests for each boundary, the repository's
full Go and frontend suites, a clean public export, and a fresh local browser
walkthrough. Any Docker, native Keychain, real-provider, or paid-subscription
check remains explicitly skipped rather than inferred from this fixture.
