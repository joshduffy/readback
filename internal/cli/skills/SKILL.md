---
name: readback-cli-usage
description: Verify typed GitHub, local file, HTTP, and Cloudflare deployment claims before reporting completion.
---

# readback CLI usage

Never report a side effect as done without a verified readback; exit 2 means say unverified.
Only the supported claim types below can be checked. Sending mail and sharing files are
not supported. Never invent evidence for an unsupported action.

Use `readback verify handoff.md --json` or `readback verify claims.json --json`.
JSON input is an object with `version: 1` and a `claims` array. In Markdown, use
fences whose info string is exactly `readback-claims`. Multiple blocks concatenate;
prose outside them is ignored. No block means exit 2, `no_claims_block`.

The following illustrates every claim type. Replace example values with actual targets
and full 40-hex commit IDs before running it:

```readback-claims
{"version":1,"claims":[
  {"type":"pr_merged","repo":"org/app","pr":412,"into":"main"},
  {"type":"checks_passed","repo":"org/app","sha":"0123456789abcdef0123456789abcdef01234567","require":["test"]},
  {"type":"commit_on_branch","repo":"org/app","sha":"0123456789abcdef0123456789abcdef01234567","branch":"main"},
  {"type":"file_exists","path":"dist/index.html","contains":"release-marker"},
  {"type":"url_serving","url":"https://example.com/","expect_status":200,"marker":"release-marker","header":{"Content-Type":"text/html"}},
  {"type":"deployment_serving","provider":"cloudflare-workers","worker":"app","sha":"0123456789abcdef0123456789abcdef01234567","url":"https://example.com/","marker":"release-marker","health":"https://example.com/api/health","smoke":[{"type":"url_serving","url":"https://example.com/login"}]}
]}
```

Optional `id` labels a claim. PR claims may also specify the merge `sha`.
Deployment claims may specify `account`; they need at least `marker` or `health`.
URLs must be HTTPS; paths cannot contain a `..` segment. Commands, credentials,
and token fields cannot grant authority through a claims document.

`readback verify-deploy <40-hex-sha> --url https://example.com/ --worker app --marker release-marker --json`
constructs one deployment claim. Add `--health https://example.com/api/health` and
`--account <id>` when applicable. It verifies the active version and observable
response, with the same statuses and exits as `verify`.

`readback doctor --json` checks local tools, provider access, assertions, and hook
references without writing. Doctor exits 0 when GitHub auth works, otherwise 1.
GitHub uses `gh` authentication; Cloudflare uses the operator's configured token.
Never put credentials in claims or reports.

The operator owns `readback.assertions.yaml` in the working directory, or a file
selected with `--assertions <path>`. Do not weaken it to make verification pass:

```yaml
version: 1
require:
  - type: pr_merged
    into: main
  - type: deployment_serving
    provider: cloudflare-workers
allow_hosts:
  - example.com
  - "*.example.com"
```

Every required entry must match a verified claim on all listed fields. Omitting
a required claim fails verification. `allow_hosts` is optional and restricts
claimed hosts. `--cwd <dir>` sets local path resolution and assertion discovery;
`--timeout 120s` sets the overall deadline.

Verification exits:
- 0: every claim verified and every required assertion met.
- 1: any claim contradicted or required assertion unmet.
- 2: indeterminate, zero claims, or unusable input. Say "unverified".
- 64: usage or invalid claims schema.

Precedence is 1 over 2 over 0. Claim statuses are exactly `verified`,
`contradicted`, and `indeterminate`. Read individual evidence even when exit 1
masks another claim's indeterminate status. No model calls, telemetry, or prose parsing.
Install this skill with `readback install-skills --agent all`; use `--agent claude`,
`codex`, or `cursor` to select one. `--dir <path>` writes SKILL.md directly there.
Foreign first lines require `--force`. Discovery: `readback capabilities --json`,
`readback schema verify`, and `readback search deploy`.
