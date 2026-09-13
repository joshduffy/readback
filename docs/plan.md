# readback: product plan

Status: draft v1, 2026-09-12. Pre-review by Kimi K3 and Codex; see docs/reviews/.

## One line

Agent ops for your own machine. Agents claim; readback verifies.

## Why this name

A readback is the aviation and radio discipline where the receiver repeats the
instruction back so the sender can confirm it was heard correctly. Every module in
this product is the same move: an agent claims a side effect, a state, or a policy,
and readback reads it back from the system of record. The claim verifier is the
literal readback; the hook policy, deploy verifier, fleet scanner, and memory linter
are readbacks against git, deploy platforms, the filesystem, and the agent's own
memory.

## Thesis (from the 2026-09-12 landscape research)

Twelve candidate tools were researched. None is moot, none is bundled, and no
product occupies "governance for a developer's own coding agents" as opposed to
governance for production LLM apps. Coding agent CLIs (Claude Code, Codex, Cursor,
Gemini CLI, OpenCode, Kimi Code) each grew a private hook system in 2026;
Microsoft's AGENT-HOOKS-0.1 spec (2026-08-27) standardizes SDK-level policy but no
terminal CLI adopts it. The gap is one binary that (a) verifies what agents claim
from the actual system of record, (b) compiles one policy file into every CLI's
hook format, and (c) keeps a machine full of parallel agent sessions from rotting.

## Non-goals

- Not an LLM gateway, proxy, or observability platform (LiteLLM, Helicone, Langfuse own that).
- Not a sandbox or container runtime (e2b, Daytona, Agyn own that).
- Not a code review model. `readback review` routes to existing CLIs and owns only the preflight.
- Not an agent framework or orchestrator.
- No hosted service, no telemetry, no accounts. Local-first, network only to the systems of record the user names.

## Language: single Go binary

Yes. Reasons, in order of weight:

1. Hooks fire on every agent tool call. Go cold start is ~5 ms; Node is 40 to 80 ms and
   Python worse. A hook that adds 50 ms to every Bash call is felt; 5 ms is not.
2. Zero runtime dependency. Codex, Cursor, and Gemini CLI users may not have Node.
   `curl | bash` and `brew install` must produce a working hook with nothing else installed.
3. Cross-platform is one `goreleaser` config: darwin, linux, windows on amd64 and arm64.
4. Precedent: gh CLI and asc CLI (asccli.sh) both chose Go for exactly this shape and
   both are agent-adopted.
5. Static binary means the policy engine cannot be altered by a compromised
   `node_modules` in the repo being governed, which matters for a guard.

Cost: the existing prototypes are Node hooks and shell scripts. They are
ported, not wrapped. Each port carries its incident-derived test cases with it.

Library: cobra for commands, no other heavy deps. YAML via gopkg.in/yaml.v3, JSON via
stdlib. Shell out to `gh`, `git`, `wrangler`, and vendor CLIs rather than re-implementing
their auth.

## Module map

Every command supports `--json`. Every module exposes its contract through
`readback schema <cmd>` and `readback capabilities`, mirroring asc.

| Command group | Solves | Landscape verdict |
|---|---|---|
| `readback verify <report>` | Claim verifier: parse a handoff or completion report for side-effect claims and read each back from GitHub, deploy platform, or URL. Non-zero exit on any unproven claim. | OPEN |
| `readback deploy <sha>` | Deploy verifier: poll CF Workers Builds, GH Actions, Vercel, Railway, Netlify, Fly for the build matching a SHA, then fetch a content marker from the live edge with cache-bust. | OPEN |
| `readback policy` | One `readback.policy.yaml` compiled to Claude Code settings hooks, Codex hooks.json, Cursor, Gemini CLI, and AGENT-HOOKS-0.1 JSON. `readback hook <event>` is the runtime entrypoint each compiled hook calls. | CROWDED, translator OPEN |
| `readback launch` | Clean-env launcher: `env -i`, named credential tiers, shell-snapshot off, rehydration self-test that fails closed. | CROWDED at isolation, OPEN at the ask |
| `readback fleet` | Orphan scanner: crawl a root for every repo, classify stashes, no-upstream branches, missing remotes, stale worktrees, dirty trees; emit dispositions. | CROWDED, seam OPEN |
| `readback memory lint` | Memory linter: stale file references, duplicates, verdicts-vs-facts, PII, credentials, missing index entries. | OPEN |
| `readback handoff` | Handoff spec: write, validate, claim-once ownership, stale detection. Vendor-neutral markdown with frontmatter. | CROWDED to OPEN |
| `readback review` | Cross-vendor review router with an inspectable hard-blocking secret and PII preflight, size caps, timeout, daily budget. Routes to codex, claude, gemini, kimi CLIs. | CROWDED, preflight OPEN |
| `readback provenance` | Trailer spec (`Readback-Agent`, `Readback-Model`, `Readback-Session`, `Readback-Reviewed`), `readback blame`, and a merge-gate check that fails when unreviewed-AI fraction exceeds a threshold. | CROWDED, standard OPEN |
| `readback budget` | Fan-out governor: `plan` estimates cost from a spawn manifest before launch; `check` validates an integrity report (spawned, succeeded, died, capHit); a policy rule blocks fan-out without explicit model. | CROWDED, agent-as-unit OPEN |
| `readback context` | Instruction-file analysis: static smells first (bloat, conflicts, dead references), behavioral ablation harness in v0.3. | OPEN |

## Agent-native surface

- `--json` on every command; human table output only when stdout is a TTY.
- `readback schema <cmd>` prints the JSON schema of that command's output.
- `readback capabilities` lists modules, providers, and which CLIs are detected on this machine.
- `readback search <term>` finds commands and flags locally, no docs fetch.
- `readback install-skills [--agent claude|codex|cursor|all]` writes SKILL.md files so the agent knows the tool on install.
- Exit codes are the contract: 0 verified, 1 unproven or violation, 2 could not check. Agents branch on these.

## Repository layout

```
readback/
  cmd/readback/main.go            entrypoint
  internal/cli/                   cobra root, global flags, json/tty output
  internal/verify/                claim extraction + readback runners
  internal/deploy/                platform pollers + edge fetch
  internal/policy/                policy schema, rule engine, compilers per CLI
  internal/hook/                  stdin event parsing for each CLI's hook protocol
  internal/launch/                env -i, tiers, self-test
  internal/fleet/                 repo crawl, classification, dispositions
  internal/memory/                memory file parsing + lints
  internal/handoff/               spec, validate, claim
  internal/review/                preflight + vendor routers
  internal/provenance/            trailers, blame, gate
  internal/budget/                spawn manifest, estimator, report validator
  internal/context/               instruction-file smells
  internal/providers/             github, cloudflare, vercel, railway, netlify, fly, http
  internal/output/                table + json writers, schema registry
  skills/                         SKILL.md per module, installed by install-skills
  policies/                       shipped rule packs (destructive-git, paid-api, secrets, fanout)
  docs/plan.md, docs/reviews/     this file and the cross-model critiques
  testdata/                       incident-derived fixtures (fabricated handoffs, runaway spawn manifests)
  .goreleaser.yaml, Makefile, .github/workflows/ci.yml
```

## Milestones

v0.1 (extraction release, target 4 weeks): verify, deploy, policy with three rule packs
compiled to Claude Code and Codex, fleet, memory lint. Homebrew tap, curl installer,
setup-readback action. Repo flips public.

v0.2: launch, handoff, review preflight, provenance trailers and blame.

v0.3: budget, context static smells, Cursor and Gemini CLI compilers, AGENT-HOOKS-0.1 export.

v0.4: context behavioral ablation harness, provenance merge gate action.

## Verification per module

Every module ships with testdata from a real incident and a `make attack` target that
runs the adversarial inputs. Claim verifier: the 2026-08-17 fabricated handoff must fail.
Deploy verifier: a stale green workflow run must not count as a deploy. Policy: the
paid-API rule must block `import openai` in a script and pass a pure-fs script. Fleet: a
stash must surface as at-risk WIP, never as a status glyph. Memory lint: "verified safe
in PR #221" must flag as a verdict.

## Risks

- Hook protocol drift across five CLIs. Mitigation: hook protocols are versioned fixtures in
  testdata, one per CLI per version; CI fails when a fixture no longer parses.
- AAIF publishes a handoff spec. Mitigation: ship ours first as markdown-plus-frontmatter and
  offer it as a reference implementation; adopt theirs if it lands.
- Scope. Eleven modules is a suite, not a tool. Mitigation: v0.1 is five modules and the
  README leads with `verify` alone.

## Open questions for reviewers

1. Is the name right? Does "readback" read as verification to a non-aviation audience?
2. Is eleven modules under one binary the right shape, or should verify+deploy ship alone first under this name and the rest follow as `readback-*` companions?
3. Go: any reason to prefer Rust or TypeScript given the hook-latency argument?
4. Which of the v0.1 five would you cut, and which v0.2 module would you pull forward?
5. What is the single strongest existing tool this will be compared against, and what does the README need to say in its first three lines to survive that comparison?

---

# Revision 2 (2026-09-12, after Kimi K3 and Codex critiques)

Both reviews are in docs/reviews/. Their claims were checked before adoption; four
tools my landscape pass missed were confirmed on GitHub:

| Tool | Stars | Last push | What it changes |
|---|---|---|---|
| dyoshikawa/rulesync | 1,419 | 2026-09-12 | Generates cross-tool agent config including hooks. `policy` is CROWDED, not translator-OPEN. Differentiator left: enforcement semantics plus a `policy test` that proves compiled hooks fire. |
| agent-sh/agnix | 411 | 2026-09-11 | Lints CLAUDE.md, AGENTS.md, SKILL.md, hooks, MCP. `context` static smells are CROWDED. |
| eqtylab/cupcake | 290 | 2026-03-02 | OPA/Rego policy layer across coding-agent harnesses. Stale six months, but it disproves "nobody governs developer agents." |
| YawLabs/ctxlint | 10 | 2026-09-12 | Stale references, contradictions, credentials, Claude memory duplicates. `memory lint` is a rule pack (verdicts-vs-facts), not a module. |

git-ai (2,636 stars, OpenAI-steered) already carries agent, model, and session attribution with line-level blame. `provenance` is dropped from the roadmap; readback will emit git-ai-compatible attribution if it ever needs to, not a competing trailer spec.

## What changed

1. **Claims are typed, never extracted from prose.** Both reviewers named prose extraction as the way this product dies. `readback verify` takes a claims document: a JSON file or a fenced ```readback-claims block inside a markdown handoff. Prose outside the block is commentary. The installed skill teaches agents to emit the block. Closed claim set for v0.1: `pr_merged`, `checks_passed`, `deployment_serving`, `url_serving`, `file_exists`, `commit_on_branch`. Unknown claim types are `indeterminate`, never ignored.
2. **Required assertions come from the operator, not the agent.** An optional `readback.assertions.yaml` in the repo lists claims that must be present and verified for exit 0. An agent cannot pass by omitting the hard claim.
3. **Per-claim status is `verified`, `contradicted`, or `indeterminate`.** Exit 0 only when every claim and every required assertion is verified. Empty input, unsupported claim, missing credential, unreachable provider: all indeterminate, exit 2. Mixed contradicted and indeterminate: exit 1.
4. **Evidence strength is explicit.** `deployment_serving` separates build success, deployment activation, marker observation, and optional smoke check. A marker fetch proves one response at one time; the result says so.
5. **Reports cannot expand authority.** A claims document cannot name a command to run, a credential, or a network destination outside the provider allowlist in operator config.
6. **`deploy` renamed to `verify-deploy`.** A read-only command must not sound like a mutation.
7. **`doctor` added to v0.1.** Reports detected CLIs, provider auth, and hook installation state. Every support issue is "it is not running"; capabilities alone cannot answer that.
8. **v0.1 is verify, verify-deploy, doctor, and the discovery commands.** Providers: GitHub via gh, Cloudflare Workers Builds, plain HTTP. Nothing else. Policy moves to v0.2 with the `policy test` self-check as its entry condition. Fleet and memory move to v0.3 as rule packs where possible. npx shim dropped.
9. **Name kept provisionally.** README line one carries the meaning; the aviation explanation stays in this file and out of the README.
10. **Latency claim needs a benchmark.** The 5 ms figure is a cold-start number; a hook that loads a policy file and shells out is not 5 ms. `make bench` measures end to end before any latency claim appears in the README.

## Exit criterion before widening scope

Five people outside this machine use `readback verify` on a recurring workflow. Until then no v0.2 module lands on main.
