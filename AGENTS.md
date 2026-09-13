# readback

Verify coding agents' completion claims against GitHub and live deployments. Single Go
binary, JSON everywhere, exit codes agents branch on. `CLAUDE.md` is a symlink to this file
so Codex, Kimi, and Claude Code read the same instructions.

## Commands

- `make check`: go vet, go test ./..., go build. Run before every commit.
- `make attack`: incident-derived adversarial fixtures (must reject what they must reject).
- `make build`: produces `./readback` with the git version embedded.
- `./readback capabilities --json`, `./readback schema <module>`, `./readback search <term>`.
- `./readback verify <claims.json|handoff.md> [--assertions path] [--cwd dir] [--timeout dur]`.
- `./readback doctor --json` (never writes; exit 0 only when gh auth works).

## Layout

- `cmd/readback/main.go`: entrypoint only.
- `internal/cli/`: cobra root, `--json` flag, discovery commands, exit-code plumbing.
- `internal/output/`: the JSON envelope `{command, ok, exit, data, error}` and table writer.
- `internal/registry/`: module registry that drives capabilities, schema, search.
- `internal/verify/`: claims (typed, closed set), extract (fenced blocks), assertions (operator file), result (statuses, reason codes, Checker interface), run (runner, deadlines, exit precedence), verify (command).
- `internal/providers/<name>/`: one Checker per system of record (github via gh, http, local, cloudflare).
- `internal/deploy/`, `internal/doctor/`: verify-deploy and doctor commands.
- `schemas/`: JSON Schema for the claims document and the result payload (documentation; Go validation is authoritative).
- `policies/`, `skills/`: shipped rule packs and the agent skill installed by `install-skills` (v0.2).
- `testdata/`: fixtures; `testdata/verify/fabricated-handoff.md` is the 2026-08-17 incident shape.
- `docs/plan.md` (product plan, revision 2 authoritative), `docs/verify-implementation.md` (v0.1 spec: contracts and providers).

## Frozen contracts (never change without a decision record)

- Exit codes: 0 every claim verified and every required assertion met; 1 any claim contradicted or a required assertion unmet; 2 anything indeterminate, zero claims, or unusable input; 64 usage. Precedence 1 > 2 > 0.
- Claim statuses are exactly `verified`, `contradicted`, `indeterminate`. Reason codes are the closed set in `internal/verify/result.go`; a Checker-supplied reason outside it is replaced.
- Claims are typed. Prose is never parsed. Input is a JSON document or fenced ```readback-claims blocks; no block means exit 2 with reason `no_claims_block`.
- A claims document can never carry a command, credential, token file, or host outside the operator allowlist. Required assertions come from `readback.assertions.yaml`, never from the agent.
- A stub command exits 2 with "not implemented". Never let a stub return 0.
- No model calls, no telemetry, no network except the systems of record named by claims. Never print a token, a token prefix, or a token length.

## Adding a module or provider

Package under `internal/<name>` calls `registry.Register` in `init` and exposes
`Command(func() *output.Writer)`; wire it in `internal/cli/root.go` and update the stub list in
`internal/cli/root_test.go`. Providers implement `verify.Checker` and are wired in the default
registry in `internal/verify/run.go`. Every contract line gets a named test; every provider ships
recorded fixtures under `testdata/providers/<name>/`.

## How work is done here

- Any coding agent may contribute. Every PR is reviewed by a different model vendor than the one that wrote it, and every review finding is reproduced by a person before it counts.
- Branching: `feature/<task>` -> PR -> squash to main. Commit messages are `type(scope): description`, ASCII only, no trailers of any kind, no Co-Authored-By, no session links, no generated-with footer.
- CI runs `make check` and `make attack` on every PR and on main. Run both locally before pushing.
- Public under MIT since v0.1.0.
