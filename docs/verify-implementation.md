# v0.1 implementation plan: verify, verify-deploy, doctor

Status: 2026-09-12, authoritative for v0.1. Supersedes the module rows in docs/plan.md where
they differ. Written so that each task can be dispatched to a driver lane with no parent
context and read back independently.

## 0. Frozen contracts

- Exit codes: 0 every claim verified and every required assertion satisfied; 1 at least one
  claim contradicted or a required assertion missing; 2 nothing contradicted but at least
  one claim indeterminate, or no usable input; 64 usage. Precedence: 1 beats 2 beats 0.
- Claim statuses: `verified`, `contradicted`, `indeterminate`. Nothing else, ever.
- Prose is never parsed. Only a JSON file or a fenced ```readback-claims block is input.
- A claims document can never name a command, a credential, or a token file: schema error,
  exit 64, no network calls made. A host outside the operator's `allow_hosts` is refused per
  claim as `contradicted` / `host_not_allowed` with no network call for that claim, so the
  run exits 1 (amended 2026-09-12 to match sections 2 and 3; the per-claim record is what an
  operator wants to see).
- No model calls. No telemetry. Network only to GitHub, Cloudflare, and URLs named in claims.

## 1. Claims document

Path for the schema: `schemas/claims.v1.json` (JSON Schema draft 2020-12). Embedded into the
binary as a string constant in `internal/verify/schema.go` (a `go:embed` cannot reach a parent
directory; `TestClaimsSchemaFileMatchesEmbedded` fails the build on drift) and printed by
`readback schema verify`.

```json
{
  "version": 1,
  "claims": [ { "type": "...", ...fields } ]
}
```

Closed set, v0.1. Unknown `type` is `indeterminate` with reason `unsupported_claim_type`.

| type | required fields | optional | provider |
|---|---|---|---|
| `pr_merged` | `repo` (owner/name), `pr` (int) | `into` (branch), `sha` (merge commit) | github |
| `checks_passed` | `repo`, `sha` (40 hex) | `require` (list of check names) | github |
| `commit_on_branch` | `repo`, `sha`, `branch` | | github |
| `file_exists` | `path` (repo-relative or absolute) | `contains` (literal string) | local |
| `url_serving` | `url` (https) | `expect_status` (default 200), `marker` (literal string in body), `header` (name: value) | http |
| `deployment_serving` | `provider` ("cloudflare-workers"), `sha`, `url` | `account`, `worker`, `marker`, `health` (URL returning JSON with `commit_sha`), `smoke` (list of url_serving claims) | cloudflare + http |

Field rules: `sha` must be 40 hex, short SHAs are a schema error. `url` must be https. `path`
may not contain `..`. Each claim may carry an optional `id` (string) used in the result.

Markdown extraction (`internal/verify/extract.go`): find every fenced block whose info string
is exactly `readback-claims`; concatenate their `claims` arrays; version must match across
blocks. Zero blocks: exit 2, reason `no_claims_block`, message tells the agent the block
grammar. Text outside blocks is ignored. This is the answer to the fabricated-handoff fixture:
an agent that writes prose only gets exit 2 and its report must say unverified.

## 2. Assertions file (operator-owned)

`readback.assertions.yaml` in the working directory (`--cwd`, default the process cwd), or
`--assertions <path>`. Discovery never walks parent directories, so run from the repo root or
pass the path. Never read from the claims document.

```yaml
version: 1
require:
  - type: pr_merged
    into: main
  - type: deployment_serving
    provider: cloudflare-workers
allow_hosts:            # optional; when present, url and health hosts must match
  - example.com
  - "*.example.com"
```

A `require` entry matches a claim when every listed field equals the claim's field. Each
`require` entry must be matched by at least one `verified` claim, else the run is 1 with
reason `required_assertion_unmet`. `allow_hosts` absent means any https host is allowed.

## 3. Result record

Schema: `schemas/result.v1.json`. Shape of `data` inside the standard envelope:

```json
{
  "input": {"path": "handoff.md", "claims": 4, "assertions": "readback.assertions.yaml"},
  "summary": {"verified": 3, "contradicted": 0, "indeterminate": 1, "required_unmet": []},
  "claims": [
    {
      "id": "c1", "type": "pr_merged", "status": "verified",
      "checked_at": "2026-09-12T18:02:11Z",
      "evidence": [
        {"source": "github", "call": "GET repos/o/r/pulls/412", "observed": {"merged": true, "merged_at": "...", "merge_commit_sha": "...", "base_ref": "main"}}
      ],
      "reason": null
    }
  ]
}
```

`reason` is a stable snake_case code when status is not verified:
`unsupported_claim_type`, `not_merged`, `merged_into_other_branch`, `checks_failing`,
`checks_pending`, `check_missing`, `sha_not_on_branch`, `file_missing`, `content_missing`,
`status_mismatch`, `marker_missing`, `header_mismatch`, `version_sha_mismatch`,
`health_sha_mismatch`, `deployment_not_found`, `auth_missing`, `provider_unreachable`,
`host_not_allowed`, `no_claims_block`, `required_assertion_unmet`.

`evidence[].observed` is the raw fields used for the decision, never the whole response.

## 4. Provider contracts

All providers implement the `verify.Checker` interface declared in `internal/verify/run.go`:

```go
type Check func(ctx context.Context, c verify.Claim) verify.Outcome  // Outcome{Status, Reason, Evidence}
```

Every network error, timeout, 401, 403, or 5xx maps to `indeterminate` with
`provider_unreachable` or `auth_missing`. A 404 on the object itself is `contradicted`
(`deployment_not_found`, `not_merged` when the PR does not exist) because the system of
record answered. Per-call timeout 20 s, overall `--timeout` default 120 s.

### github (`internal/providers/github`)

Shell out to `gh api` so auth is gh's. Missing `gh`, or a `gh api` failure that names
authentication (exit 4, or an auth message on stderr), is `auth_missing`; `readback doctor`
runs the explicit `gh auth status` preflight.

- `pr_merged`: `gh api repos/{repo}/pulls/{pr}`. Verified when `merged == true` and, if
  `into` given, `base.ref == into`, and, if `sha` given, `merge_commit_sha == sha`.
- `checks_passed`: `gh api repos/{repo}/commits/{sha}/check-runs --paginate` plus
  `gh api repos/{repo}/commits/{sha}/status`. Verified when every check-run has
  `conclusion` in {success, neutral, skipped} and combined `state == success`; any
  `queued`/`in_progress` is `checks_pending`; if `require` lists a name not present,
  `check_missing`. Zero check-runs and no statuses is `indeterminate` with `check_missing`,
  never verified.
- `commit_on_branch`: `gh api repos/{repo}/compare/{branch}...{sha}`; verified when
  `status` is `identical` or `behind` (sha is an ancestor of branch).

### local (`internal/providers/local`)

- `file_exists`: `os.Stat`; with `contains`, read and search. Paths resolve against `--cwd`
  (default: current directory). Absolute paths allowed; `..` rejected at schema time.

### http (`internal/providers/http`)

- `url_serving`: GET with `User-Agent: readback/<version>`, `Cache-Control: no-cache`, and a
  `?readback_bust=<unix-ns>-<n>` cache-bust query that never clobbers an existing parameter
  (configurable off with `--no-cache-bust` on `verify` and `verify-deploy`). Follow
  up to 5 redirects. Verified when status equals `expect_status`, `marker` (if given) is in
  the first 2 MB of the body, and every `header` matches. Evidence records final URL,
  status, and the byte offset of the marker.

### cloudflare-workers (`internal/providers/cloudflare`)

Auth: `CLOUDFLARE_API_TOKEN` env, else `~/.cloudflare/api-token`, else `auth_missing`.
Account: claim `account`, else `CLOUDFLARE_ACCOUNT_ID`, else the first account the token
lists. `deployment_serving` is verified only when ALL of the following hold, and the
evidence lists each as its own entry so a reader can see which rung was reached:

1. `version_sha` (confirmed by live probe, 2026-09-12): `GET /accounts/{acct}/workers/scripts/{worker}/deployments`
   returns `result.deployments[]` newest first, each with `versions[]{version_id, percentage}`;
   take every version with percentage > 0 in `deployments[0]`. Then
   `GET /accounts/{acct}/workers/scripts/{worker}/versions/{id}` returns `result.annotations`,
   a flat map. On a Workers Builds deploy the worker carries `"workers/message": "<40-hex sha>"`
   (the deploy workflow passes the commit as the message) and `"workers/triggered_by":
   "version_upload"`; wrangler-only workers carry no sha at all. Rule:
   verified when any active version's `workers/message` or `workers/git-commit` contains the
   claim `sha` as a delimited full 40-hex id (annotations are free text, so a shorter hex run
   never counts; the 12-char prefix rule applies only to the structured health `commit_sha`);
   a match on any active version wins over a fetch failure on another; an empty deployments
   list is `indeterminate version_sha_unavailable`;
   `indeterminate version_sha_unavailable` when no active version carries a sha-shaped
   annotation; `contradicted version_sha_mismatch` when an annotation carries a different
   40-hex sha. Auth: `CLOUDFLARE_API_TOKEN` (account-scoped `cfat_` works; the Workers
   Builds `builds` API needs a user `cfut_` token and returned no rows, so it is not used).
   Recorded fixtures: `testdata/providers/cloudflare/`.
2. `health_sha` (when `health` given): GET the health URL, parse JSON, verified when
   `commit_sha` equals the claim `sha` or is a prefix of it with at least 12 chars. This is
   the proven pattern from the existing per-repo verify-deploy scripts.
3. `marker` (when given): the `url_serving` check against `url` with the marker.
4. `smoke` (when given): every listed `url_serving` claim verified.

When neither `health` nor `marker` is given the claim cannot be verified: `indeterminate`,
reason `marker_missing`, with the message that at least one observable rung is required.

## 5. verify-deploy

`readback verify-deploy <sha> --url <https://...> [--marker <s>] [--health <url>] [--worker <name>] [--account <id>] [--provider cloudflare-workers]`

Constructs exactly one `deployment_serving` claim and runs it through `verify`. Same
output and exit codes. Exists so a human or a hook can prove a deploy without writing JSON.

## 6. doctor

`readback doctor` reports, in JSON and table: readback version; detected agent CLIs
(`claude`, `codex`, `cursor`, `gemini`, `kimi`) with version strings; `gh` present and
`gh auth status` result; Cloudflare token source found and whether
`GET /accounts` succeeds (never prints the token); whether `readback.assertions.yaml`
exists in cwd and parses; whether any hook in `~/.claude/settings.json` or
`~/.codex/hooks.json` references `readback` (informational in v0.1). Exit 0 when gh auth
works, 1 otherwise. Doctor never writes.

## 7. Definition of done for v0.1

- `readback verify testdata/verify/claims.json --json` against a real repository returns
  per-claim evidence and the expected exit.
- `readback verify testdata/verify/fabricated-handoff.md` exits 2 with `no_claims_block`.
- `readback verify testdata/verify/contradicted-claims.json` exits 1.
- `readback verify-deploy <real merge sha> --url https://<site> --health https://<site>/api/health` exits 0 after a real deploy and 1 against a stale SHA.
- `readback doctor` exits 0 on a machine with `gh` authenticated and 1 on a machine with no `gh`.
- goreleaser builds all six targets; `brew install joshduffy/tap/readback` works; the install
  script at readbackcli.dev/install works on a clean macOS shell.
- Repo flips public.
