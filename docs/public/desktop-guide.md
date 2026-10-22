# Desktop Alpha guide

Foundry Desktop packages the existing workbench and local Go controller. It does
not require a separately installed Node.js, npm, Go toolchain or compiler to run.
Git is a separate prerequisite for repository operations. This is an experimental
local-workflow Alpha, not a production-ready autonomous coding service.

## Get the right artifact

Use the **Desktop packages** workflow in
[GitHub Actions](https://github.com/tsumon/foundry/actions/workflows/desktop.yml).
Select a successful run for the source revision you intend to install. Download
the matching platform artifact, its `SHA256SUMS` and the accompanying runtime
source material. GitHub may require sign-in to download workflow artifacts;
these artifacts expire and are not a GitHub Release or an update channel.

| Target | Package | Trust status |
| --- | --- | --- |
| macOS Apple Silicon | `Foundry-0.1.0-alpha.1-arm64.dmg` | Ad-hoc integrity signature; not Developer ID signed or notarized |
| Windows x64 | `Foundry-0.1.0-alpha.1-x64.exe` | Unsigned per-user installer; no verified publisher |

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

## First run: two deliberately different modes

**Persistent Workbench** opens an empty, persistent local workbench. It supports
project, delivery-target, environment and shared-resource records, task history
and the existing settings UI. It does not invent a configured model or silently
turn on account access. Creating account metadata is not a successful connection.

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
- `workbench` holds persistent state; `simulations` contains separate retained
  fixture runs. Use **Show Current Data / Evidence Folder** to open the active
  location. Windows also keeps its Electron profile in the private `electron`
  subdirectory.

Removing/replacing the Mac app or running the Windows uninstaller does not erase
these directories. No account credentials belong in these desktop fixtures.
Back up retained evidence explicitly; uninstall is not evidence cleanup.

## Boundaries that installation does not change

- Context, plan execution, delivery and one-operation integration approvals remain
  separate. Role names and model text never grant permission.
- Subscription/login/catalog/inference operator entrypoints are unavailable in
  the desktop launch. No real Keychain values are read by the desktop mode.
- No real Docker activation, host scripts, Git writes, deployment or migration.
- No automatic model/provider retry, token refresh, paid fallback or updates.
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

**Simulation versus real verification:** inspect `source_kind`, the attempt trace
and the exported report. A screenshot, exit code or model review alone does not
prove real execution, safety or production readiness.

For implementation/build details see [desktop design](desktop-delivery.md).
Report suspected vulnerabilities privately via [SECURITY.md](../../SECURITY.md).
