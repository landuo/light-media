# Performance and subscription integration

Integrates the recovered second-round performance changes (`ae0db4f`) with the proxy/subscription source branch (`07e8df75`). No production deployment or NAS timing claim is made.

## Preserved boundaries
- The existing 500-subscription enumeration limit is unchanged.
- Creating a subscription still does not immediately check/download its contents.
- Plugin invocation and native fallback contracts remain live and compatible.
- Native season identity and episode validation occurs before its existing single-layer cache is written. The proposed shared raw season cache was removed after independent regression review.
- Reused provider details are reprojected using current category rules and image domain. Missing newly-required fields cause a fresh provider read.
- Lightweight season summaries are validated before cache insertion; a new cache namespace avoids reusing invalid earlier candidate data.

## Verification
- Frontend: 600/600 tests; Vue type check and production build pass.
- Embedded web assets test passes; existing hashed assets retained for in-flight clients.
- Integrated backend race selection passes (130.278s): cache coalescing/cancellation, disabled logging, notification indexes/readers, subscription display/coverage/create semantics, live plugin fallback and recognition, proxy routing and configuration.
- Full networkproxy/catalog race packages pass.
- Separate final summary-cache race selection covers malformed responses, retry recovery, missing/null summary fields and real plugin SDK season contracts.

Tests use isolated local fixtures and temporary databases. This is not a full-repository green claim or a measurement of real external services, NAS hardware, or site latency.

## Site credential-save follow-up

- Same-identity Cookie/APIKey/Token-only edits no longer wait on download/import locks. All enabled sites are normalized before the final equality check; effects and any structural/other-setting changes retain the original exclusion boundary.
- Settings/plugin serialization, transactional identity reconciliation, encrypted persistence and commit-before-publication/event ordering are retained. Admission waits are cancelable; the site-save request uses a 15-second deadline.
- The UI explicitly requests `POST /api/sites/manage?response=configuration` to acknowledge the committed redacted configuration without a full account-history read. The default POST, unknown response modes and GET preserve their existing full response contract.
- The UI no longer waits on a second global settings fetch after success. Existing statistics remain visible; the edited site's old statistics are marked stale. Duplicate submission is blocked; unmount cancels the request; a 20-second client timeout clearly reports that the outcome is unconfirmed.
- Final frontend suite: 603/603 using `TZ=Asia/Shanghai`; type-check/build and embedded asset checks pass. The earlier unrelated log-oracle failure was caused by its timezone-dependent date literal, not a site-save regression.
- Independent review reproduced and closed the legacy-other-site normalization bypass, and checked explicit/default/unknown HTTP response compatibility. No real site credentials or NAS data were used.

The fast path does not remove necessary database/settings/plugin serialization. Structural edits can still wait, now with cancellation. This is not a guarantee that every deployment saves instantly.

Final combined backend `-race` selection passed in 152.713s after the normalization and opt-in amendments, covering the site-save, identity/import/export, cache/log/notification, subscription/season/plugin and proxy regressions together.
