# Compatible API acceptance — 2026-10-28

Maintainer-observed acceptance of source `e3f31446802c1fc0cd46dd8dddeaf5cd7b18906c`,
using the copied Mac DMG resources on physical Apple Silicon. The account,
credential, task and evidence directory were isolated from existing workspaces.
Private credentials, transcripts and local paths are not included in this summary.

| Check | Observed result |
| --- | --- |
| Receiver | User-selected local CPA Responses entry configured in Magpie; fixed loopback receiver, no route fallback |
| Requested model | `grok-4.6` |
| Returned model label | `grok-4.6-build`, as reported by the gateway; not independent upstream identity verification |
| Task | Replace a quadratic duplicate scan in `NormalizeLabels` with a set; only `labels.go` editable |
| Protected inputs | `go.mod` and `labels_test.go` unchanged |
| Sandbox | Bundled Linux arm64 VM on macOS; readiness passed, no guest network or host execution fallback |
| Baseline | 14 verifier-counted tests passed, exit 0, instance shutdown confirmed |
| Model roles | Researcher, Planner, Worker, QA, Auditor: five successful real calls |
| Candidate | 14 tests passed, exit 0, instance shutdown confirmed |
| Delivery | Patch and report sealed after independent delivery approval; source checkout unchanged |
| Provider-reported usage | 21,087 input tokens and 11,841 output tokens; not a monetary charge calculation |

All five successful responses satisfied the existing strict JSON media type and
completed Responses envelope checks. No retry, login, subscription refresh or
alternate model was used. One earlier request against a different, explicitly
selected remote entry was rejected with HTTP 401 and retained as a failed
invocation. The user then selected the local entry; the remaining allowance was
reduced accordingly. Combined acceptance consumption was six of seven calls.

The first candidate passed, so **this real run did not exercise Debugger**.
Failed-candidate → Debugger → passing recheck → sealed delivery is covered by
local protocol fixtures through the same production router and engine, for both
official OpenAI and compatible Responses routes. It must not be reported as a
real-provider repair run.

This result establishes one real compatible-gateway Go delivery on Mac. It does
not establish successful OpenAI subscription inference, Windows real-provider
acceptance, arbitrary-language support, provider availability, publisher signing
or notarization. Native Windows installation/VM acceptance remains separately
reported by the exact-source Desktop packages CI.

Retained private artifact digests:

- Patch: `499e895024ede6da68a0b6494d160cdcd3378e4c307d49664e737ecfce89c546`
- Report: `ec091946ae6119114a33debfb74d2087b3b18947b7250e4e02bb77af7d77eefa`

## Real repair and delivery — stable candidate

A separate bounded run used the installed resources built from
`5b5cd706c9a2010e29b95a899484b304d3492617` on physical Apple Silicon. It used
independent task/account data and a seven-call ceiling; historical tasks and their
consumed budgets were unchanged.

This was a **deliberate fault-injection exercise**, not a spontaneous model bug.
The approved plan told Worker to initially implement `Double(x)` as
`(x << 1) + 1`, then required Debugger to repair it to `x << 1` using the actual
verifier failures. The protected test file and `go.mod` were never edited.

| Check | Observed result |
| --- | --- |
| Fixed receiver/model | User-selected local CPA Responses entry; requested `grok-4.6`, gateway reported `grok-4.6-build` |
| Model calls | Researcher, Planner, Worker, Debugger, QA and Auditor each completed once; six of seven calls used, no retry/fallback |
| Baseline | 9 real VM tests passed; shutdown confirmed |
| Injected candidate | 9 verifier-counted tests, confirmed `test_failed`; shutdown confirmed |
| Repaired candidate | 9 real VM tests passed; shutdown confirmed |
| Reviews | QA and Auditor passed against the corrected candidate and matching evidence |
| Delivery | Independently approved and sealed patch/report; source checkout and protected bytes unchanged |
| Provider-reported usage | 26,844 input and 11,929 output tokens; not a monetary charge calculation |

All six responses passed strict media and completed-envelope validation.
No guest network, host execution fallback, login or subscription renewal occurred.
The produced patch changes only `double.go`, from `x + x` to `x << 1`.
This adds real Debugger/repair evidence to the earlier first-pass delivery; it does
not establish arbitrary-project reliability or Windows real-account acceptance.

Retained private artifact digests:

- Patch: `4b17a0836dd60640829ef5628c3b8873268915de84813c133965ec23bac5420d`
- Report: `c5de06e4974b910ad1538f33d740339b05d3b2c1cd99a824b3d996cf6ef4c026`

Later candidates must verify unchanged executable/resource bytes before carrying
this run forward. Release receipts identify the final installer and that binding;
a version or source-tree resemblance alone is insufficient.

## Production path validation — 2026-10-29

Installed Mac 0.2.0, carrying source `e53ece42ff9f26e99a7d39a62b0fab4b790dc30a`,
completed a real repair of Foundry's `web/src/repositoryPath.ts`. The UI admitted
control characters that native repository inspection rejects. The original
production test reproduced the defect before any implementation change.

The production module was copied byte-for-byte into a bounded TypeScript package.
Existing behavior tests were adapted to `node:test`, with a protected native
isolation check and an empty frozen dependency bundle. This establishes a
production-module delivery, **not support for the whole React/Go monorepo**.

| Check | Observed result |
| --- | --- |
| Receiver/model | User-selected local CPA Responses gateway; requested `grok-4.6`, upstream identity not independently verified |
| Model calls | Researcher, Planner, Worker, QA and Auditor each completed once; five of seven calls, no retries or switching |
| Change | One early rejection of U+0000–001F and U+007F–009F; no other path rules changed |
| Baseline and candidate | Three real VM checks passed in each; protected inputs unchanged |
| Independent regression | Five real VM checks passed, including every Cc character on both path styles and legitimate Unicode/POSIX names |
| Lifecycle | Two readiness checks and three test executions; all five native instances confirmed closed |
| Delivery | Separate delivery approval after independent regression; sealed patch/report, then exact reviewed module bytes applied to Foundry source |
| Source integration | All 975 frontend tests passed; typecheck and production web build passed |
| Native UI | Installed Mac displayed the delivered task, five-call budget and patch/report links after reopening the isolated profile |

The product requires a passing baseline and immutable imported tests. The new
regressions therefore ran in a second, separately approved verification task
against the returned candidate, before delivery. That task made zero model calls;
the original task's protected tests were never replaced. No deliberate failure was
injected, and the first candidate passed, so this run did **not** call Debugger.

The installed controller produced the patch; the fixed 0.2.0 release assets have
not been replaced with it. Existing task data and consumed budgets were preserved.
No private credentials, raw transcripts or local evidence paths are published.

Retained private artifact digests:

- Patch: `d8b97aef66350c1496347c2cc8f2138c07e55b63a005d230d8d9ed4209009e9a`
- Report: `92502c6675d10950c8bd5441f6931a47706b47e60a7f66c1a5bdc04f2e878e73`
