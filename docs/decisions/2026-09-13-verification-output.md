# Verification output contract additions

Date: 2026-09-13

Readback result records now include a typed claim summary. The summary records the
condition that was checked without serializing the claim decoder's raw input map. URL
userinfo, credential query values, fragments, and sensitive header values are keyed
HMAC pseudonyms. Their structure and equality remain useful without exposing the
original value, its prefix, or its length. Status names and exit codes do not change.

An output encoding or write failure cannot produce exit 0. A failed write preserves an
existing exit 1, exit 2, or exit 64. A result that otherwise had exit 0 becomes exit 2.
This keeps the frozen claim precedence while refusing to report success when the result
was not delivered.

An HTTP response that exceeds the configured body cap is read through one extra byte. If
the required marker is absent from the retained bytes, the result is indeterminate with
the new closed reason response_truncated because the unseen bytes may contain the marker.
An exact-cap body can still produce marker_missing, and a marker found before the cap can
still verify. A known status or header contradiction takes precedence over truncation.

A Cloudflare deployment needs both live edge evidence and evidence that names the claimed
commit SHA. A matching health response can satisfy both. A generic marker or smoke check
cannot supply commit identity. A matching version annotation still needs a health, marker,
or smoke observation at the edge.

Only a missing implicit readback.assertions.yaml is treated as absent. A nonregular,
unreadable, or otherwise unstatable assertions path fails with exit 2. Assertion syntax
errors remain usage failures with exit 64.

Local file content checks use the same indeterminate response_truncated result when
unread content could contain the requested text. They probe one extra byte to distinguish
an exact-cap file from a longer file.

Cloudflare API credentials are required only when a Worker version check is requested.
A health-only deployment check sends no credentials and can establish commit identity and
edge evidence from its JSON response. A specified Worker without credentials still returns
auth_missing. This narrows the unconditional auth requirement in the original v0.1 spec.

Discovery now reports working verification commands as beta and placeholders as planned.
The schema command returns a named input/result map instead of one ambiguous schema.
Capabilities omit schema bodies; search returns name, summary, status, and milestone,
with an empty array for no matches. Full schemas remain available through schema.
