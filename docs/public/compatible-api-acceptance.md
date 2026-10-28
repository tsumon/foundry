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
