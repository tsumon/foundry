# Workbench screenshot provenance

These are direct JPEG browser captures of the actual React workbench, not mockups or generated product images. They were captured on 2026-10-19 from application source commit `213fb10822df2903b3c1b217d63c67fc80426775` using `go run ./cmd/foundry-acceptance --web-dir web/dist` after a production frontend build.

| Image | Scenario and visible state | Capture |
| --- | --- | --- |
| [workbench-plan-approval.jpg](workbench-plan-approval.jpg) | `plan_success`: a local protocol response has produced a plan; plan execution still awaits approval | 1280×720 browser viewport, full-page capture at 1280×1187 |
| [model-acceptance.jpg](model-acceptance.jpg) | `SYNTHETIC Kimi interactive package`: explicit metadata-only acceptance check; live login/catalog/inference closed, quota unknown, remote billing unverified | 1280×720 browser viewport and capture; settings page scrolled to the report |

The UI is Chinese in both images. Scenario IDs, account IDs, repository commits and authorization IDs shown are generated disposable fixture identifiers, not real account credentials. `/workspace/src/main.txt` is the synthetic container-path example, not a user's host path.

## What these images do not prove

The acceptance process uses a fresh disposable Git repository and SQLite database, in-memory fake credentials, owned literal-loopback SIWC/model/MCP protocol fixtures and a synthetic runner. It never authenticates a real account or contacts a cloud model. The runner does not run Docker. Screenshots do not certify real subscription eligibility, inference quality, quota, billing, external MCP execution, Docker execution or production safety.

The plan card's ordinary Docker description expresses the product's execution boundary. In this explicitly synthetic harness, no Docker command was run and the plan remains unapproved. The local acceptance report is a point-in-time diagnostic, never an execution permit.

## Privacy and integrity review

Both images were visually inspected for private repository names, host paths, personal account data, tokens, authorization URLs and unlabelled claims of real success. None were observed. JPEG marker inspection found only a standard JFIF application marker; no EXIF/XMP, location, author or comment segment was present. No post-capture pixel edits were applied.

SHA-256:

```text
95ce04e772363084c3dd7c99dc620cacc291dfa891892b640d7f963eb3e52985  workbench-plan-approval.jpg
d2fb2ae3a522603b9c3e4e9276a991ca1241233217603ff92944e6878e6ddd5a  model-acceptance.jpg
```

## Desktop capture — 2026-10-23

`desktop-delivery.jpg` is a direct 2880×1920 native-window capture of the installed
Mac desktop Alpha, built from the reviewed desktop changes on source base
`ed6b0046f31277327ad3f1e044910d341d6f7e95`. The captured package's SPA build time is
`2026-10-22T18:41:21.788Z`. It shows the isolated `Normalize labels` descriptor
simulation after separate baseline, context, execution and delivery approvals.
The actual installed app saved both patch and report through native save dialogs.
The title, task and verification record explicitly identify synthetic execution.

Pixels were inspected: no personal account, credential, authorization URL or host
path appears. The original JPEG's EXIF dimension block, display ICC profile and
Photoshop metadata blocks were removed losslessly for privacy; the compressed
image scan is unchanged. No results, labels, UI elements or pixels were generated
or retouched. This screenshot does not certify cloud models or Docker.

`foundry-icon.png` is the 512×512 output of the checked-in `desktop/icon.swift`,
reusing the workbench's existing F/vertical-bar mark; it is not a third-party logo.
`desktop/icon.ico` wraps the same generator's 256×256 PNG in a standard Windows
ICO frame without changing the image pixels.

```text
b227d6cf86bc914b0084b923094841365de5642ab1619b960b0fcb5e39792fd3  desktop-delivery.jpg
7f629728393fb1a15bf10754bf3734e953849ae48f1af484857f3a6c54aa190d  foundry-icon.png
96a0b60e558c2162e800d4848dfe298abab267c6476e9ff8772b754b239f3ab4  desktop/icon.ico
```

Only these reviewed captures/assets belong in the public source manifest.
Runtime databases, fixture credentials, arbitrary screenshots and private
acceptance records do not.
