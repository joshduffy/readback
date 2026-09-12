# readback

Verify coding agents' completion claims against GitHub and live deployments.
Checks the exact commit and target environment, with timestamped evidence for every assertion.
Local CLI, no model calls; an incomplete or unsupported check never returns success.

```
readback verify handoff.md                 # every typed claim: verified, contradicted, or indeterminate
readback verify-deploy <sha> --url … --marker …   # build, activation, marker observed at the edge
readback doctor                            # which agent CLIs, which provider auth, are hooks firing
```

Agents emit claims in a fenced block; prose is commentary and is never parsed:

````markdown
```readback-claims
{"version":1,"claims":[{"type":"pr_merged","repo":"org/app","pr":412,"into":"main"}]}
```
````

Exit codes are the contract: `0` all claims verified, `1` a claim contradicted, `2` could not check. Pass `--json` for the full evidence record. Discover locally with `readback capabilities`, `readback schema <module>`, `readback search <term>`.

Status: scaffold, v0.1 in progress. Plan and cross-model critiques in `docs/`.
