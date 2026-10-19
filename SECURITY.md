# Security policy

## Report a vulnerability privately

Use [GitHub private vulnerability reporting](https://github.com/tsumon/foundry/security/advisories/new) to report a suspected vulnerability to repository owner `tsumon`, who monitors that private channel.

Do not open a public issue for a suspected vulnerability. Do not include live credentials, private repository content, or unrelated personal data. Remove secrets from logs and attachments before submitting a report.

When possible, include:

- the affected source revision;
- macOS, Go, Node.js, npm, Git, and Docker versions relevant to the report;
- minimal reproduction steps and observed versus expected behavior;
- whether the behavior involved a network request, credential access, Docker execution, or a source-repository change; and
- a sanitized log or minimal disposable repository when it materially helps reproduction.

No response-time or remediation-time commitment is offered for this experimental source alpha.

## Supported versions

FOUNDRY does not yet publish an installer, signed application, package-manager formula, downloadable binary release, or production support commitment. The reviewed source-alpha target is macOS arm64 (Apple Silicon). Intel macOS and other OS/CPU targets are unverified and unsupported.

See [Alpha status](docs/public/alpha-status.md) for the full support boundary and [Safety model](docs/public/safety.md) before running the software.

## Relevant security boundaries

FOUNDRY treats model, MCP, Skill, repository, and browser content as untrusted data. The deterministic controller owns state transitions, budgets, immutable bindings, approvals, verification evidence, and artifact export.

Provider credentials belong in macOS Keychain, never SQLite, browser storage, logs, reports, or committed fixtures. Synthetic tests must never use real credentials. Legacy accountless profiles retain network capability, so the application must not be treated as globally offline.

The selected source repository is read-only to the workflow. Candidate changes remain in controller-owned storage, and delivery produces a local patch and Markdown report for human review. Docker restrictions reduce risk but do not prove the image, host, command, or candidate safe.
