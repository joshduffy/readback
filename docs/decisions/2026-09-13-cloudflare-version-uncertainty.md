# Cloudflare mixed active version uncertainty

Date: 2026-09-13

During a rolling Workers deploy the newest deployment holds several active versions
with positive traffic. The version_sha rung fetches each active version's annotations.
Version identity is existential over those active versions: one active version whose
annotations contain the claimed commit as a delimited full 40-hex id verifies the rung,
and that known match still wins over a wrong version or a failed observation on another
active version.

Old behavior: any active version with a different full-SHA annotation contradicted the
claim with version_sha_mismatch, even when another active version could not be observed
at all or carried no usable annotation. A 404 or API10007 from a version-detail fetch
contradicted with deployment_not_found, as if the whole deployment were absent.

New behavior: without a match, uncertainty blocks contradiction. Only a completely
observed active set, every version carrying usable full-SHA annotations and none
matching, contradicts with version_sha_mismatch. Otherwise the rung is indeterminate,
resolved by a stable priority that does not depend on version order: auth_missing,
then provider_unreachable, then version_sha_unavailable.

- An active version with a missing version id or a failed detail fetch is
  provider_unreachable; a 401 or 403 retains auth_missing.
- A referenced version's 404 or API10007 means the version evidence is inconsistent or
  unavailable, not proof the deployment is absent. It is translated to
  provider_unreachable at the detail-call boundary. A 404 or API10007 on the
  deployments-list call itself still contradicts with deployment_not_found, because
  the system of record answered for the worker.
- An active version whose annotations carry no usable full-SHA id is
  version_sha_unavailable.
- Inactive versions and older deployments still cannot affect the result.

Structural and API inconsistency is provider_unreachable rather than
version_sha_unavailable so that a matching health response cannot conceal an
unavailable requested provider check: the aggregate Check waives
version_sha_unavailable when another rung proves the commit SHA, but never waives
auth_missing or provider_unreachable. Missing usable annotations remain
version_sha_unavailable, and health may still supply identity in that case, as before.

Compatibility: claim statuses, the closed reason-code set, the JSON schemas, and the
exit-code precedence are unchanged; no new fields were added. The only behavior change
is that some claims that were contradicted under mixed active versions are now
indeterminate until the uncertain versions can be observed, some claims that were
contradicted under the old rule are now verified when a matching health response
supplies the commit identity a previously contradicting mixed active set withheld, and
version-detail 404 or API10007 responses no longer contradict the deployment as a
whole. Known version or health contradictions still win in aggregate, and a matching
version annotation still needs health, marker, or smoke evidence at the edge.

Recorded fixtures: testdata/providers/cloudflare/mixed-versions.json covers the mixed
active version shapes, including the version-detail API10007 regression.
