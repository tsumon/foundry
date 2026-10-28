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
