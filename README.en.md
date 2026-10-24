<div align="center">

<img src="docs/media/foundry-icon.png" width="88" alt="Foundry">

# FOUNDRY

### A clear delivery process—even when the team is just you.

Plans · Isolated candidates · Built-in sandbox · Repairs · Approvals · Local patches

[![Verify](https://github.com/tsumon/foundry/actions/workflows/verify.yml/badge.svg?branch=main)](https://github.com/tsumon/foundry/actions/workflows/verify.yml)
[![Desktop packages](https://github.com/tsumon/foundry/actions/workflows/desktop.yml/badge.svg?branch=main)](https://github.com/tsumon/foundry/actions/workflows/desktop.yml)
[![Alpha](https://img.shields.io/badge/stage-desktop%20alpha-F0A44B)](docs/public/alpha-status.md)
[![Apache 2.0](https://img.shields.io/badge/license-Apache--2.0-2C56F5)](LICENSE)

[简体中文](README.md) · [English](README.en.md) · [Install & use](docs/public/desktop-guide.md) · [Architecture](docs/public/architecture.md) · [Safety](docs/public/safety.md)

</div>

<p align="center">
  <img src="docs/media/desktop-delivery.jpg" alt="Installed Foundry desktop: independently approved synthetic delivery with patch and report export" width="100%">
</p>
<p align="center"><sub>Actual workbench, shown in Chinese with disposable synthetic data. Visible plans and states do not certify real model or Docker execution.</sub></p>

Foundry is a software-delivery workbench for a one-person team. It reads a committed Git snapshot and keeps plans, candidate changes, failures, rechecks and delivery in one inspectable workflow. Your source repository stays read-only. The output is a **patch and Markdown report**, not an unreviewed automatic push.

> [!IMPORTANT]
> **This is an approval-bound coding Alpha, not an unattended autonomous developer.** Coding tasks default to a bundled lightweight Linux VM; Docker is an explicit alternative. Docker Desktop is not required, and repository code never silently runs on the host. Model access and test execution have separate approvals; installation grants neither. Native sandbox acceptance uses local model-protocol responses—**real-account end-to-end acceptance is not claimed**. The original fully synthetic workflow remains labeled `synthetic_stub`.

## Inspect the outcome before you deliver

| Understand the plan | Isolate the changes | Keep the evidence |
| :--- | :--- | :--- |
| Approve context and execution separately. Plans are editable; saving is not approval. | Create candidates from a fixed commit. Protect tests and leave the source repository untouched. | Retain failures, Debugger repairs, rechecks and QA/Auditor reviews. Delivery has its own approval. |

## Install the desktop app

Download the artifact only from a platform job that passed all its gates in [Desktop packages](https://github.com/tsumon/foundry/actions/workflows/desktop.yml); one platform's success does not certify another. Check its source revision and `SHA256SUMS`; retain both runtime sources and `sandbox-sources`. **Distribution uses CI artifacts, not a GitHub Release. Older artifacts do not represent the current version.**

### Verified builds · 2026-10-24

| Platform / revision | Download | Acceptance actually performed |
| :--- | :--- | :--- |
| Apple Silicon Mac · `46e1247` | [DMG + checksums + runtime sources](https://github.com/tsumon/foundry/actions/runs/39964561670/artifacts/12506990609) | Physical-Mac installation, native UI, restart; real Apple Virtualization failure → repair → recheck |
| Windows x64 · `0c87188` | [EXE + checksums + reports and screenshots](https://github.com/tsumon/foundry/actions/runs/39983328865/artifacts/12515976175) | Unicode-path installation, cross-drive user data, real WHPX loop; native UI automation, restart, uninstall with evidence retained |

Check [Mac Verify](https://github.com/tsumon/foundry/actions/runs/39964561514), [Windows Verify](https://github.com/tsumon/foundry/actions/runs/39983328874), and [native Windows installation acceptance](https://github.com/tsumon/foundry/actions/runs/39983328865/job/120516374476). Evidence is bound to the revisions above—**it does not automatically certify later builds**. Both platforms execute tests in real VMs; role responses are local protocol fixtures. No real model accounts were called.

The Mac package has an ad-hoc integrity signature, not Developer ID signing or notarization. Windows uses an unsigned per-user installer. Hardware virtualization and Windows Hypervisor Platform must already be enabled; Foundry does not install or enable those system features.

The app bundles its frontend, controller, Linux guest and Go toolchain. No separate Go, Node.js, npm, MSYS2 or Docker installation is needed. Repository operations still require supported local Git. Missing virtualization blocks execution: no automatic OS feature enablement, reboot, host execution, software emulation or Docker fallback. GitHub artifacts may require sign-in and expire; this is not a permanent download channel. Do not disable OS security protections to bypass unsigned-app warnings. See the [desktop guide](docs/public/desktop-guide.md) for prerequisites, storage and uninstall behavior.

## From a request to a reviewable patch

Choose **Controlled Coding Workbench** in the application menu. It has its own persistent data, separate from the ordinary workbench and demonstrations:

1. Explicitly select an OpenAI account, route and role models. API-key and subscription routes are distinct, never fallbacks for each other. Both Mac and Windows coding account cards can explicitly prepare connection, followed by independent approval/execution and a manual browser step; separately approve catalog discovery, select a recorded model and assign the resulting profile to future-task roles. The ordinary workbench does not expose this preparation entry.
2. Provide a goal, committed Go repository and editable/protected file scope. The built-in sandbox is selected by default and its identity is frozen with the task. Creation neither calls a model nor boots a VM.
3. Separately approve and execute sandbox readiness and baseline verification. After a passing baseline, approve context sharing for Researcher and Planner to produce the plan.
4. Review or revise the plan, then approve execution. Workers propose isolated changes. A confirmed test failure can trigger at most two Debugger repairs, each followed by verification.
5. QA and Auditor review the current candidate and test evidence. Independently approve delivery to export the patch/report.

**The current scope is bounded, standard-library Go projects—not arbitrary stacks.** Both hosts test inside Linux; this does not validate native DMG/EXE, browser or mobile applications. There is no package installation, guest networking, host filesystem share, source-checkout write or protected-test edit. Credential expiry, quota exhaustion, ambiguous outcomes and unconfirmed shutdown stop the task: no automatic retry, refresh or paid-API substitution. Call-count and time limits are not monetary caps. See the [sandbox design](docs/public/embedded-sandbox.md) and [desktop guide](docs/public/desktop-guide.md).

An existing Docker environment can be explicitly selected for a **new task** on the supported Mac path. Its immutable image, readiness and approval material are separate; a VM approval never authorizes Docker.

## Try the workflow without an account

Choose **Foundry → New Isolated Local Repository Simulation**, then open a task labeled `SYNTHETIC`:

1. Inspect the baseline material. **Approve it**, then separately **execute the simulated baseline**.
2. Check the context files and synthetic endpoint; authorize planning.
3. Review or revise the plan, then approve execution. Workers change only an isolated candidate.
4. In Verification, inspect **failure → Debugger repair → simulated recheck → QA/Auditor** records.
5. Approve delivery separately and save the patch/report. Review the patch outside Foundry before applying it.

Each simulation has fresh inputs and a retained separate database. **Persistent Workbench** is a different entrypoint for your persistent records; demonstration data is not mixed into it, and it does not silently connect a model.

Have a disposable repository? **Open Committed Descriptor Repository…** accepts one with a committed `foundry.task.json`. The bounded workflow supports multiple files, declared new files and dependent steps; uncommitted changes are excluded. A descriptor supplies preset proposals—it is not autonomous understanding of an arbitrary repository. Read the [contract and limits](docs/public/repository-task-orchestration.md).

## What works—and what stays closed

Both desktop platforms share the subscription approval flow. Mac uses Keychain;
Windows uses Credential Manager with bounded segmented storage for complete large
bundles, never plaintext files. Protocol acceptance uses local synthetic services;
native credential diagnostics use Foundry-only test items. **Neither certifies a
real-account model loop.** Catalog selections pin account, route, authorization
generation/expiry and receipt. A catalog receipt never approves inference.
See the [subscription workbench](docs/public/mac-subscription-workbench.md).

### Upgrade the app, preserve the task

| Retained state | Upgrade behavior |
| :--- | :--- |
| Original task, plan, baseline and evidence | Original records are not rewritten or relabeled as new-build results |
| Consumed model budget | Never reset; unknown-outcome calls remain consumed |
| Frozen sandbox | Retain and select by complete identity; new tasks use the current default |
| Independent approvals | Retention grants no authority; expired, missing or damaged inputs block, without refresh or fallback |

Tasks predating runtime retention can recover their exact sandbox from an explicitly
verified local previous installation, without downgrading the app or rewriting its
database. See the [upgrade compatibility design](docs/public/desktop-parity-upgrade.md).

| Capability | Actual boundary |
| :--- | :--- |
| Project and asset workbench | Projects, delivery targets, environments, shared-resource ledger and task associations |
| Model-driven repository tasks | Opt-in coding entry; frozen OpenAI account/model selection, committed Go inputs, isolated candidates, repairs and export |
| Default built-in sandbox | Mac Virtualization / Windows WHPX; independent Linux Go tests, no guest network or host shares; native and synthetic evidence recorded separately |
| Demonstration agents / verification | Scripted local responses and content assertions; **not** real model reasoning or compiler tests |
| Approvals and recovery | Controller-bound revisions/digests; restart never replays unknown outcomes or widens permission |
| MCP / Skills / hooks | Management, discovery and static descriptions; source CLI has an independently approved local MCP fixture. Desktop simulation enables no external tool execution |
| Optional Docker | Explicit Mac selection of an existing immutable image, separate readiness/baseline/candidate approvals; Windows Docker remains closed, never a fallback |
| Real models / subscriptions | Production protocol wired; real-account acceptance requires separate authorization. Tasks never implicitly log in, query catalogs, refresh, retry or switch to paid APIs |
| Automatic apply / Git writes / deployment / migrations | **Not provided**; exported patches are handled outside the product |

Roles assign responsibilities, not permissions. Model reviews are not test evidence, and a synthetic pass does not verify a real project. See [Alpha status](docs/public/alpha-status.md) for the full support boundary.

## Run from source

Mac development baseline: Go **1.27.3**, Node.js **22.23.2**, npm **10.9.8**, cgo/a C compiler. The system `/usr/bin/git --no-lazy-fetch --version` must succeed.

```sh
git clone https://github.com/tsumon/foundry.git
cd foundry
npm ci --prefix web
./scripts/build.sh
go run ./cmd/foundry-acceptance --repository-tasks-fixture --web-dir web/dist
```

Open the printed random loopback URL. This entry uses disposable data, no real credentials and no Docker execution. Ordinary CLI and dedicated operator modes have different capabilities: do not infer that every CLI configuration is offline. Do not reuse personal runtime data or enter real credentials during evaluation.

See [Contributing](CONTRIBUTING.md) and the [desktop design](docs/public/desktop-delivery.md) for development, verification and package builds.

## Explore further

| Using Foundry | Engineering & safety |
| :--- | :--- |
| [Install, demonstrate, troubleshoot](docs/public/desktop-guide.md) | [Architecture and approval boundaries](docs/public/architecture.md) |
| [Model-driven coding and authority](docs/public/live-coding.md) | [Safety model](docs/public/safety.md) |
| [Capabilities and acceptance scope](docs/public/alpha-status.md) | [Third-party notices and runtime source](THIRD_PARTY_NOTICES.md) |
| [Changelog](CHANGELOG.md) | [Model-gate design, not live-access certification](docs/public/model-acceptance.md) |

Report suspected vulnerabilities through [GitHub private vulnerability reporting](https://github.com/tsumon/foundry/security/advisories/new), never by publishing credentials or exploit details in an issue.

---

Copyright © 2026 [tsumon](https://github.com/tsumon). Foundry source is [Apache-2.0](LICENSE), permitting commercial use subject to its terms. Third-party components retain their own licenses. No production support or service-level commitment is offered.
