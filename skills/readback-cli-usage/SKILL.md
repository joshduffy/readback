---
name: readback-cli-usage
description: How to call the readback CLI from an agent. Use whenever you claim a side effect (merged, deployed, sent, shared), before reporting completion, or when a hook denies a command.
---

# readback CLI usage

readback reads your claims back from the system of record. Exit codes are the contract:

| Exit | Meaning | What you do |
|---|---|---|
| 0 | verified | report the claim |
| 1 | unproven, or a rule fired | do not report it; fix or ask |
| 2 | could not check | say "unverified" in your report, never "done" |

Always pass `--json`. Discover locally: `readback capabilities`, `readback schema <module>`, `readback search <term>`.

Before you write a completion report: `readback verify --json <handoff.md>   # emit a ```readback-claims block; prose is not parsed`.
After a merge: `readback verify-deploy --json <sha> --url https://host/path --marker "<string that only the new build serves>"`.
