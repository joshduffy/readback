# readback

Verify coding agents' completion claims against GitHub and live deployments.

Install with one of these commands (requires a published release and tap):

```sh
brew install --cask joshduffy/tap/readback
curl -fsSL https://readbackcli.dev/install | bash
go install github.com/joshduffy/readback/cmd/readback@latest
```

The installer supports macOS/Linux on amd64/arm64, verifies the release SHA-256
checksum, and defaults to `~/.local/bin`. Set `READBACK_VERSION=v0.1.0` to pin a
release or `READBACK_INSTALL_DIR` to choose its destination. Add it to your PATH.
Run `readback doctor --json` to check provider access. GitHub checks use `gh` auth.

Install the bundled agent instructions:

```sh
readback install-skills --agent all
readback install-skills --agent codex
readback install-skills --dir ./my-skill
```

Default agent is `all`: Claude, Codex, and Cursor under their home skill folders.
`--dir` receives SKILL.md directly. A different first line requires `--force`.

Try this 30-second example, which needs no network. Run in a scratch directory:

```sh
mkdir readback-demo && cd readback-demo
printf 'release-ready\n' > proof.txt
cat > claims.json <<'JSON'
{"version":1,"claims":[
  {"id":"present","type":"file_exists","path":"proof.txt","contains":"release-ready"},
  {"id":"wrong-content","type":"file_exists","path":"proof.txt","contains":"never-written"}
]}
JSON
readback verify claims.json --json
echo "$?" # 1: one verified claim and one contradicted claim
```

Agents can put the same JSON in a Markdown fence with the exact info string
`readback-claims`. Only those blocks are read; prose is ignored:

````markdown
```readback-claims
{"version":1,"claims":[{"type":"pr_merged","repo":"org/app","pr":412,"into":"main"}]}
```
````

Claim types:

| Type | Required fields | Optional fields |
| --- | --- | --- |
| `pr_merged` | `repo`, `pr` | `into`, `sha` |
| `checks_passed` | `repo`, `sha` | `require` check names |
| `commit_on_branch` | `repo`, `sha`, `branch` | |
| `file_exists` | `path` | `contains` literal |
| `url_serving` | `url` | `expect_status` (200), `marker`, `header` map |
| `deployment_serving` | `provider`, `sha`, `url` | `account`, `worker`, `health`, `marker`, `smoke` |

All claims accept `id`. SHAs must be 40 hex characters, URLs HTTPS, and paths
cannot contain `..` segments. Deployment provider is `cloudflare-workers` and
requires an observable `marker` or `health` to verify, alongside active-version
evidence. Smoke entries are `url_serving` claims. Cloudflare uses the operator's
configured token; never put credentials or commands in a claims document.

```sh
readback verify handoff.md --cwd . --timeout 120s --json
readback verify-deploy <40-hex-sha> --url https://example.com/ --worker app --marker release-ready --json
readback doctor --json
```

The operator owns `readback.assertions.yaml` in the working directory, or selects
it with `verify --assertions <path>`. Every required entry must match at least one
verified claim on every listed field. Missing required claims fail the run:

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

`allow_hosts` is optional. The agent cannot supply assertions in its claims.

Verification exit codes:

| Exit | Meaning |
| --- | --- |
| 0 | Every claim verified and every required assertion met |
| 1 | Any claim contradicted or required assertion unmet |
| 2 | Indeterminate, zero claims, or unusable input; report unverified |
| 64 | Usage or invalid claims schema |

Precedence: 1 over 2 over 0. Statuses are exactly `verified`, `contradicted`, and
`indeterminate`. JSON includes timestamped evidence per claim. Doctor separately
exits 0 when GitHub authentication works and 1 otherwise; it never writes.

What it does not do: no model calls, no telemetry, no prose parsing. It does not
perform merges or deployments, or verify sending mail and sharing files. Never
report a side effect as done without a verified readback. Unsupported or
incomplete checks cannot prove completion.

Build and test: `make check`. Live adversarial fixtures: `make attack` (requires
`gh` authentication, `jq`, and network access). Discover commands with
`readback --help` and inspect modules with `readback capabilities --json`.
