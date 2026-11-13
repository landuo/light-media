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
