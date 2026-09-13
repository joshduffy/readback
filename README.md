# readback

Your coding agent says the pull request is merged and the new version is live.
You open GitHub. The pull request is still open.

Readback is a command-line tool for this slightly tedious moment. Give it a list
of things your agent says happened. It checks files, GitHub, and web responses,
then reports what it found. Each result comes with the evidence behind it.

It is written in Go and ships as one binary. It makes no calls to language models
and sends no telemetry.

**Current status:** early software. Version 0.1.4 has known bugs in deployment
verification, host restrictions, and GitHub check reporting. Review the evidence
yourself before using a result to approve a release. See the limitations below.

## Install

On macOS, with Homebrew:

```sh
brew install --cask joshduffy/tap/readback
```

On macOS or Linux, you can also download and run the installer:

```sh
(
  installer=$(mktemp) || exit 1
  trap 'rm -f "$installer"' 0
  curl -fsSL --proto '=https' --proto-redir '=https' \
    https://readbackcli.dev/install -o "$installer" &&
    sh "$installer"
)
```

This downloads the script before running it. If the download fails, the command
fails too. The installer checks the release checksum and puts the binary in
`~/.local/bin`. Add that directory to your `PATH` if it is not already there.
It supports Intel/AMD and ARM64 machines. Set `READBACK_INSTALL_DIR` to choose a
different directory, or `READBACK_VERSION=v0.1.4` to install a specific release.

Windows binaries are on the [releases page](https://github.com/joshduffy/readback/releases).
If you already have Go 1.25 or newer, you can build and install from source:

```sh
go install github.com/joshduffy/readback/cmd/readback@latest
```

That last route currently reports its version as `dev`, even when you install a
tagged release.

## Try it on a file

Start with something small. This example needs no GitHub account or network
connection. Run it in a new directory:

```sh
mkdir readback-demo && cd readback-demo
printf 'release-ready\n' > proof.txt

cat > claims.json <<'JSON'
{
  "version": 1,
  "claims": [
    {
      "id": "present",
      "type": "file_exists",
      "path": "proof.txt",
      "contains": "release-ready"
    },
    {
      "id": "wrong-content",
      "type": "file_exists",
      "path": "proof.txt",
      "contains": "never-written"
    }
  ]
}
JSON

readback verify claims.json --json
echo "$?"
```

The first claim is `verified`. The second is `contradicted`, because the file
does not contain `never-written`. The command exits with code `1`.

The file has passed one test and failed another. Readback does not average them
into a success.

## Give your agent something to fill in

A claim is a small JSON object describing a check. For example, this asks whether
pull request 42 was merged into `main`:

```json
{
  "version": 1,
  "claims": [
    {
      "type": "pr_merged",
      "repo": "your-org/your-repo",
      "pr": 42,
      "into": "main"
    }
  ]
}
```

Replace the repository and pull request number with your own. Save it as
`claims.json`, then run `readback verify claims.json --json`. GitHub checks use
the [GitHub CLI](https://cli.github.com/) and its existing login. Run
`gh auth login` if you have not signed in.

Your agent can also put the JSON in a Markdown code block labelled
`readback-claims`:

````markdown
```readback-claims
{"version":1,"claims":[{"type":"pr_merged","repo":"your-org/your-repo","pr":42,"into":"main"}]}
```
````

Then check the report itself:

```sh
readback verify handoff.md --json
```

Readback reads only those labelled blocks. The surrounding prose is ignored,
however confidently it was written. A report without a claims block exits `2`.

To install instructions that teach your agent this format:

```sh
readback install-skills --agent codex
```

Use `--agent claude`, `--agent cursor`, or `--agent all` for the other supported
destinations. This writes a `SKILL.md` into each selected agent's skills folder.
Use `--dir ./my-skill` instead to choose a directory. Existing edited or foreign
skills are preserved unless you pass `--force`.

## Decide what must be checked

An agent could submit a perfectly true claim about a file and forget to mention
the deployment you asked for. Every submitted claim might pass while your actual
task remains unfinished.

You can make particular checks compulsory. Put them in
`readback.assertions.yaml`, which you maintain separately from the agent's report:

```yaml
version: 1
require:
  - type: pr_merged
    repo: your-org/your-repo
    pr: 42
    into: main
```

This requires a verified claim about that specific pull request and branch.
Leaving it out fails the run. Readback looks for the assertions file in the
directory chosen by `--cwd`, or your shell's current directory if you omit that
flag. `--assertions path/to/file.yaml` selects a different file.

Assertions can also list `allow_hosts` to restrict requested URLs. In 0.1.4 that
restriction does not cover redirect destinations, so it is not a reliable
network boundary yet.

## Checks and results

The current claim types are:

- `file_exists`: check that a file exists, optionally with particular text.
- `pr_merged`: ask GitHub whether a pull request was merged, optionally into a
  particular branch or with a particular merge commit.
- `commit_on_branch`: ask GitHub whether a branch contains a commit.
- `checks_passed`: inspect a commit's GitHub check runs and commit statuses.
- `url_serving`: request an HTTPS URL and check its status, text, or response
  headers. A marker is simply text you expect to find in the response.
- `deployment_serving`: check a Cloudflare Workers deployment. This is
  experimental and has the verification gaps described below.

`verify-deploy` is a shortcut for submitting a single deployment claim. Run
`readback verify-deploy --help` for its options. The same deployment limitations
apply.

Every claim gets one of three results: `verified`, `contradicted`, or
`indeterminate`. The last means Readback could not establish an answer, for
example because credentials were missing or a service was unavailable.

For `verify` and `verify-deploy`, the process exit code is:

- `0`: all claims were verified and all required assertions were met.
- `1`: at least one claim was contradicted or a required assertion was not met.
- `2`: something could not be checked, or there were no usable claims.
- `64`: the command or claims document was invalid.

If a run has both a contradiction and an unanswered check, it exits `1`.
The JSON output includes the individual results, so you can see both.

Use `--cwd path/to/repo` to choose the directory for file checks and assertions,
and `--timeout 120s` to set the verification time limit. See the
[claims schema](schemas/claims.v1.json) for fields and the
[result schema](schemas/result.v1.json) for output structure. The Go validator
is authoritative.

## Limits worth knowing about

Version 0.1.4 can incorrectly verify a deployment without checking the claimed
commit. Its GitHub checks can also report pending when GitHub Actions has
finished successfully. These need fixing before Readback can be used as the
sole release approval check.

Keep passwords, tokens, and signed URLs out of claims. The current release can
echo credential-bearing URLs into its output. Cloudflare credentials belong in
your local provider configuration, such as `CLOUDFLARE_API_TOKEN`.

A successful request describes one response at one time. It says nothing about
what the website will serve tomorrow. Large response bodies are capped, and a
marker beyond that cap can currently be reported missing.

`policy`, `hook`, `fleet`, and `memory` are placeholders that exit `2`. They do
not yet perform the work their names suggest. Discovery also has rough edges:
`capabilities` currently labels the working verification commands as stubs, and
`schema verify` prints the input schema despite its help text promising output.

Readback does not merge pull requests, deploy code, send messages, or check
whether an email was delivered. It does not read ordinary prose and turn it
into checks.

## Troubleshooting and contributing

Start with:

```sh
readback doctor --json
readback --help
```

Doctor reports installed agent CLIs and provider access. Its exit code is `0`
when GitHub authentication works and `1` otherwise. It also looks for Readback
references in hook configuration; finding one does not prove the hook runs.

To work on Readback, install Go 1.25 or newer and run:

```sh
make check
make attack
```

`make check` runs vet, tests, and builds. `make attack` exercises claims that must
fail; it needs `gh` authentication, `jq`, and network access.

The [implementation notes](docs/verify-implementation.md) describe the providers
and contracts. The [plan](docs/plan.md) describes possible future work. Code in
the plan is not necessarily code in the binary.

Readback is released under the [MIT license](LICENSE).
