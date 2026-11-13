# Performance follow-up

Baseline: main `a4396ed176b34977e0b14d4afafb1e553dd6ac5a`.
The owner excluded the 500-subscription truncation/scheduler coverage finding.
No 500 limit, scheduler enumeration, first-check scheduling, search concurrency,
selection policy or download side effect is changed.

## Implemented

- Downloader ownership and calendar coverage pure reads use the reader pool and
  committed data, without waiting for the sole writer connection.
- Notification-only partial indexes support latest ordering and unread count;
  unrelated encrypted records do not enter JSON expressions. Startup creates the
  indexes, and dedicated notification queries use them explicitly.
- Identical cache key/TTL calls share one in-flight read. Waiters cancel independently,
  each caller decodes its own model, canceled leaders allow live callers to retry.
  An upstream timeout with a live caller is shared instead of multiplied. Stale
  fallback, separate TTLs/keys and panic cleanup have regression coverage.
- Disabled DEBUG logging checks its level/scoped overrides before copying Settings;
  emitted records retain full sanitization and the second level check.
- Download annotations are indexed once and each card's view model derived once,
  retaining first non-cancelled intent before native-record matching.
- Subscription cards use client-side pages of 24 with lazy/async images. Every returned
  record remains available for filtering, sorting and editing. The backend cap is
  unchanged and counts describe the loaded batch.
- Directory navigation reuses ancestor caches; explicit refresh invalidates current
  and parent nodes. Navigation/unmount cancel obsolete requests.
- Log first-page and pagination reads share cancellation/latest-query ownership.
  Pages are bound to the submitted query, snapshot and cursor, so late old responses
  cannot overwrite or append to a newer query.

Additional subscription optimization reuses a Shanghai clock/location per coverage
calculation and per list snapshot. Exclusion reads use an exact indexed prefix range
instead of LIKE scans. No site requests are parallelized by this change.

## Evidence and boundaries

Isolated synthetic measurements, not production/browser or full subscription-run timings:
- Same-key cold reads: 20 concurrent requests share one fetch instead of 20.
- Disabled DEBUG logging: previous 20/100-site fixtures allocated about 141/681KB per
  ignored log; optimized focused benchmark has zero bytes/allocations.
- Recovery-run 100k notifications: original query about 546ms, index creation 263ms,
  indexed full poll about 12.2ms. Production migration duration is not established.
- At the actual 400 annotation-record bound, 5000 tasks require about 9.97 million
  record comparisons previously; the new index reads 400 record hashes once.
- 500 returned subscriptions display 24 cards/48 lazy images on page one instead of
  all 500 cards/1000 eager images.
- Recovered 50-season × 30-episode list benchmark: about 7.65ms / 2.80MB, compared to
  earlier same-function baseline 18.84–25.52ms / 4.93MB. CPU contention varies.

Changes were reconstructed after an interrupted publish and revalidated; earlier
results are not treated as verification of reconstructed files. No production data,
media, site or download client is used in tests. Independent cross-review caught and
fixed timeout amplification and stale log pagination before publishing. Unrelated
broad Go fixture failures are not claimed repaired.
