# Desktop Alpha guide

Foundry Desktop packages the existing workbench and local Go controller. It does
not require a separately installed Node.js, npm, Go toolchain or compiler to run.
Git is a separate prerequisite for repository operations. This is an experimental
local-workflow Alpha, not a production-ready autonomous coding service.

## Get the right artifact

Use the [v0.1.0-alpha.3 prerelease](https://github.com/tsumon/foundry/releases/tag/v0.1.0-alpha.3)
for versioned downloads. Both installers are unchanged CI outputs from public
commit `5237fc49bafe4c5d33d3d6fa117ac59c467ace16`, not rolling builds of `main`.
Download the matching installer and `SHA256SUMS`; retain its platform source
archive containing `runtime-sources` and `sandbox-sources` when redistributing.
The release includes the clean Foundry source and public CI receipts. Actions
artifacts remain build evidence, may require sign-in and expire. Neither channel
enables automatic updates or makes this Alpha a stable release.

The [release acceptance table](../../README.en.md#release-acceptance--2026-10-26)
links the exact successful Verify and Desktop runs. This Mac CI DMG passed
installed-backend contracts and persistence, not a physical-Mac VM rerun. Existing
same-source-tree hardware evidence is not acceptance of that exact installer.
This Windows EXE passed Unicode-path install, cross-drive data, native UI, restart,
evidence-preserving uninstall and a real WHPX failure/repair/recheck loop.
Model responses remain local protocol fixtures, not real-account calls. Historical
builds remain listed separately; compilation alone is not acceptance.

| Target | Package | Trust status |
| --- | --- | --- |
| macOS Apple Silicon | `Foundry-0.1.0-alpha.3-arm64.dmg` | Ad-hoc integrity signature; not Developer ID signed or notarized |
| Windows x64 | `Foundry-0.1.0-alpha.3-x64.exe` | Unsigned per-user installer; no verified publisher |

Do not treat a checksum as publisher authentication. Do not disable Gatekeeper,
SmartScreen, antivirus or other OS protections to install the Alpha. If your
machine rejects the unsigned application, stop and use the reviewed source
workflow instead. Intel Macs, Windows ARM and Linux desktop packages are not
supported in this milestone.

On macOS, open the DMG and copy Foundry to Applications (or your user Applications
folder). Run the installed copy, not the mounted image. On Windows, use the
per-user installer; administrator access, startup services and an automatic
updater are not needed. Interactive installation opens Foundry when finished;
silent installation does not. This Alpha creates no Start-menu or desktop
shortcuts. To launch it again, open `Foundry.exe` in the OS per-user Programs
folder, normally `%LOCALAPPDATA%\Programs\Foundry\Foundry.exe` (or the explicit
installation path supplied with `/D`). Uninstalling the app must not remove task data or
evidence. Keep a backup before a future upgrade; downgrading an existing data
directory is not a supported recovery method.

## First run: separate workspaces, separate authority

**Persistent Workbench** opens an empty, persistent local workbench. It supports
project, delivery-target, environment and shared-resource records, task history
and the existing settings UI. It does not invent a configured model or silently
turn on account access. Creating account metadata is not a successful connection.

**Controlled Coding Workbench** is an explicit native-menu opt-in with its own
retained data. Opening it enables configuration, not a provider call or a VM boot.
New tasks default to a bundled Linux VM: Apple's Virtualization.framework on
Apple Silicon and bundled, headless QEMU with WHPX on Windows x64. Windows needs
hardware virtualization and Windows Hypervisor Platform already available;
Foundry does not enable features, reboot or substitute software emulation.
Docker remains an explicit Mac-only alternative for a new task. Installed
metadata is not evidence of successful readiness or permission to execute.
Normal launch and the simulation retain their original closed behavior.

**New Isolated Local Repository Simulation**, in the Foundry menu, creates fresh
disposable Git inputs and a separate retained database. This is the recommended
first experience. Two examples exercise nested-path and dependent multi-file
changes. The scripted role responses and exact-content checks are labeled
`SYNTHETIC` / `synthetic_stub`; they do not execute a Go compiler, Docker, cloud
model or a real MCP service.

### Complete the local demonstration

1. Open either synthetic task. Inspect the baseline material, **approve it**, then
   separately **execute the approved baseline (simulation)**.
2. Review the context file list and synthetic endpoint. Authorize context to
   generate the plan. This does not authorize execution.
3. Inspect or revise the plan. Saving a revision is not approval. Approve the
   selected plan to produce an isolated candidate.
4. Open **Verification**. Inspect the retained failed result, Debugger change,
   passing simulated recheck, and separate QA/Auditor records. A green simulated
   result is not proof of a real test run.
5. Approve delivery. Save the patch and Markdown report, or open **Show Current
   Data / Evidence Folder**. The source repository is never modified; apply the
   patch only after an independent review outside Foundry.

Closing the app stops its own controller. It does not erase retained evidence,
restart failed work, or grant any pending approval. Starting a new simulation
creates a new run; it does not resume the previous simulation automatically.

## Model-driven Go tasks (opt-in)

The new coding mode does not require `foundry.task.json` or preset patches. It
uses a committed repository snapshot, a goal and protected/scoped files. The
bundled verifier runs standard-library Go 1.27.3 tests in Linux, not native Mac or
Windows application tests. No guest NIC, host share, dependency installation or
host-script fallback is provided. The source repository and protected tests remain
untouched. If you explicitly choose Docker on Mac, supply an existing immutable
local image matching the verifier contract; Foundry does not pull or build it.

1. Open **Controlled Coding Workbench**. In the latest source, the recommended
   path is **API quick setup**: create an OpenAI or compatible gateway API Key
   account, save the key in native storage, enter its model ID, acknowledge billing,
   and save the model connection. Choose it in role settings for new tasks.
   Neither login nor catalog discovery is required. Saving metadata does not
   send inference. API and experimental subscription routes are never automatic
   fallbacks for one another. See [API setup](api-key-coding.md); pinned Release
   binaries reflect their recorded source revision, not the latest source UI.
2. In task creation, inspect the repository and freeze its committed base, file
   scope, model selections, call budget and immutable local verification profile.
   Creation grants neither network nor execution permission.
3. Prepare, approve and explicitly execute the separate sandbox readiness check.
   Prepare, approve and execute baseline verification. A passing baseline is
   required; this mode does not ask a model to repair an already-failing baseline.
4. Approve sending the bounded repository context to the selected role models.
   Researcher and Planner produce the proposal; inspect or revise it before the
   separate execution approval.
5. Workers propose complete file edits in isolated generations. Confirmed test
   failures can enter two bounded Debugger rounds; infrastructure failures,
   uncertain execution or unconfirmed cleanup stop instead of prompting a repair.
6. Inspect real/synthetic evidence labels and QA/Auditor records. Independently
   approve delivery to export a patch/report. There is no automatic apply or Git
   write.

Native credentials use Foundry-scoped macOS Keychain or Windows Credential
Manager. Only explicit account operations, separately approved standing renewal,
or approved claimed task calls may access them. Existing accounts have renewal
disabled; opening settings or reading status never reads credentials or calls a
provider. In the **Mac and Windows coding workbenches**, the Models account card can
explicitly prepare a subscription connection. It first explains and runs a
Foundry-only synthetic native-store write/read/exact-delete check; confirmed cleanup
grants ten minutes of process-local readiness, not login approval. Propose,
approve and execute connection separately, then manually open the current local
handoff in your normal browser. The desktop validates that exact waiting session
and launches it at most once; arbitrary localhost URLs remain forbidden.

After explicitly reading a successful connection, independently prepare,
propose, approve and execute one catalog request. Read the recorded evidence,
choose a model and save a new account-bound profile, then explicitly assign it
to roles. Catalog provenance pins account, route, authorization generation and
expiry; account switches or reconnections cannot silently retarget older tasks.
No catalog receipt approves model use. A failed diagnostic retains its private
exact-item journal; unconfirmed cleanup blocks new preparation after restart.
Ordinary desktop mode remains closed. Protocol acceptance uses local synthetic
services; native-store diagnostics use isolated Foundry-owned test items. Neither
certifies real sign-in, catalog or model-task acceptance. See the
[desktop subscription workbench](mac-subscription-workbench.md).

For **Stay signed in**, prepare renewal material, review the exact registration,
resource, endpoint, scopes, native store and failure behavior, then separately
approve enabling it. Material expires after ten minutes. The desktop checks every
30 seconds while running and renews at expiry minus five minutes, respecting the
provider's earliest time. Disable stops future dispatch and joins local work;
already-sent work in another process may remain in progress until its bounded
deadline. Unknown outcomes require explicit recovery, never automatic replay.
Renewal does not refresh catalog evidence, readiness, task continuation approvals
or call budgets. A new sign-in needs fresh consent. See [session renewal](session-renewal.md).
Only synthetic local renewal has been tested; real renewal remains separately approved.

Windows Credential Manager keeps its native 2,560-byte per-record ceiling. A
bounded segmented representation supports subscription bundles up to 256 KiB,
with an exact-reference commit envelope and verified cleanup. Missing or altered
segments fail closed; there is no truncation or plaintext fallback.

Upgrades retain verified runtimes in the private coding data archive. Existing
tasks resolve only their complete frozen runtime identity; new tasks use the
new installed default. Original task, baseline, evidence and consumed budget
remain unchanged. An older installation that predates retention needs an exact
verified local bundle import; missing or damaged assets block, never silently
select the new runtime. Archive retention grants no execution permission and
does not refresh expired credentials or approvals. See
[upgrade compatibility](desktop-parity-upgrade.md).

The current task defaults are a finite 16-call budget and at most two repairs.
Usage is recorded, not a promise of available subscription quota. A transport
timeout or response-size bound is not an output-token or spending hard cap.
Expiry, allowance exhaustion and unknown outcomes stop without automatic retry,
refresh or paid fallback. The [Mac CPA/Grok acceptance](compatible-api-acceptance.md)
records a real five-role code delivery with passing VM tests. It is distinct from
local protocol fixtures and Windows installation/VM tests; subscription inference
is not certified by that result.
See [coding design](live-coding.md) for the exact authority and failure model.

After delivery, that model-backed task is a read-only historical result: download
its artifacts or inspect evidence, but start a new task for additional model chat
or execution. New readiness/cleanup preparation cannot rewrite a delivered record.

## Bring a disposable repository

The **Open Committed Descriptor Repository…** menu is a native folder picker.
Choose a disposable local Git repository containing a committed
`foundry.task.json`. This imports the committed HEAD read-only; the working
descriptor must match the commit. Uncommitted and untracked content are not part
of the candidate. This is a descriptor-driven workflow, not free-form AI coding.

Read the [descriptor contract](repository-task-orchestration.md) for proposals,
protected tests, file scope, content assertions and repair limits. Do not add
secrets to the repository or descriptor. Never use a production checkout for a
first evaluation.

On macOS, `/usr/bin/git --no-lazy-fetch --version` must work. A different Git on
PATH does not replace the trusted system path. On Windows, install official Git
for Windows in the OS **Program Files** directory: normally
`C:\Program Files\Git\cmd\git.exe`. Foundry does not search PATH, install Git or
use a portable copy automatically. For this Alpha, Windows repositories must be
on the **same drive as Foundry data**; cross-drive/aliased-drive imports fail
closed. Only regular committed `100644` files are supported; executable-bit
entries, symlinks, junctions, case aliases and alternate streams are rejected.

## Data and removal

- macOS controller data: `~/Library/Application Support/foundry-desktop/controller-data/`.
- Windows controller data: the OS Local Application Data folder's `Foundry\`
  directory, normally `%LOCALAPPDATA%\Foundry\`.
- `workbench` holds ordinary persistent state; `coding` holds the opt-in coding
  workbench; `simulations` contains separate retained
  fixture runs. Use **Show Current Data / Evidence Folder** to open the active
  location. Windows also keeps its Electron profile in the private `electron`
  subdirectory.

Removing/replacing the Mac app or running the Windows uninstaller does not erase
these directories. No account credentials belong in desktop simulation fixtures.
Back up retained evidence explicitly; uninstall is not evidence cleanup.

## Boundaries that installation does not change

- Context, plan execution, delivery and one-operation integration approvals remain
  separate. Role names and model text never grant permission.
- Subscription/login/catalog/single-inference operator entrypoints are unavailable
  in desktop launch. Coding-mode task inference has its own claimed-call authority;
  it is not permission to query a catalog or begin OAuth.
- Real Docker remains closed in normal/simulation mode and on Windows. Mac coding
  mode still needs separate readiness, baseline and task execution approvals.
- Built-in VM execution also requires independent readiness, baseline and task
  approvals. Changing its bundle/profile requires new material. No automatic
  backend switch, host execution or TCG fallback is allowed.
- No host scripts, Git writes, deployment or migration.
- No automatic model/provider retry, paid fallback or updates. Credential renewal
  requires its own explicit standing approval and is disabled for existing accounts.
- The renderer has no Node.js or filesystem bridge. A random per-launch capability
  protects the local server, including its bootstrap and static content. Other
  browser tabs cannot simply open the desktop server URL.
- The underlying source CLI has separate operator modes and legacy networking
  behavior. Do not generalize the desktop simulation's isolation to every CLI
  configuration; see [safety](safety.md).

## Troubleshooting without deleting evidence

**Startup failure:** verify the artifact checksum and installation, then check
the platform's Git prerequisite if a simulation was requested. Failure is not
automatically retried. Reopen only after addressing the reported condition.

**Data directory already owned:** close the owning Foundry window normally. Do
not delete lock files or kill processes by name. A second desktop launch should
focus the existing window rather than start a second controller on the same data.

**Privacy/integrity rejection:** do not relax permissions or remove records to
bypass the check. Retain the failing inputs for diagnosis. Broken or altered
installed resources require a clean verified application copy, not editing its
manifest.

**Built-in sandbox unavailable:** missing or changed bundled assets require a
verified installation. Unavailable hardware virtualization or WHPX blocks the
task; it is not a simulated pass. On managed Windows machines, ask the device
administrator about the prerequisite. Foundry never changes it automatically.
Unconfirmed shutdown preserves evidence and blocks further work instead of
killing processes by name. See [sandbox design](embedded-sandbox.md).

**Simulation versus real verification:** inspect `source_kind`, the attempt trace
and the exported report. A screenshot, exit code or model review alone does not
prove real execution, safety or production readiness.

For implementation/build details see [desktop design](desktop-delivery.md).
Report suspected vulnerabilities privately via [SECURITY.md](../../SECURITY.md).
