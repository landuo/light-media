# Responsiveness and loading improvements

Base: `42d54bf7f58ac32e587419cbe1676842e6d07d04` (main, verified again during implementation).

This change is a performance repair, not a deployment or a claim of complete production acceptance. Plugin ordering, side effects, encrypted configuration, ownership/path checks, stable history identity, recovery records and the existing UI layout remain authoritative.

## Changes

1. Download polling is single-flight. A response slower than the 3-second polling interval can finish and update the UI. Changing downloaders or unmounting aborts the active request and ignores stale completions.
2. Route components and plugin-heavy filters are loaded on demand. Ace and plugin utility CSS no longer belong to the initial HTML dependency graph. The complete Vuetify plugin component registry and search KeepAlive state remain available.
3. Build output includes gzip representations. Static serving negotiates gzip, retains HEAD/cache/MIME behavior, and serves byte ranges from the identity representation. Previous hashed assets are retained for compatibility with already-open clients; no source or user records are removed.
4. Media decoration uses scoped composite expression indexes and one reader snapshot. Subscription display batches coverage, exclusions, metadata and encrypted custom words. Directory display computes pending counts once. Queue and subscription ordering have matching indexes.
5. Transfer history maintains a current-visible projection per source, updates dirty source groups only, and pages through ordered indexes. Hydration starts from the bounded page instead of scanning all plans. Refresh commits before opening the consistent read snapshot. Stable IDs, hidden markers, all attempt records and cancellation identity are preserved.
6. File listing snapshots configuration and precomputes protection mappings once per request. Mutations still revalidate current safety state. The frontend uses a variable-height virtual list with selection and keyboard support.
7. Dashboard independent startup reads run concurrently; disabled widgets retain their settings entries but do not fetch widget data. Polling stops on deactivation and stale responses are discarded.
8. Image requests have a deadline that includes queue time, per-provider and global limits, and same-key miss coalescing. Network slots are released before fsync. Pruning is coalesced in the background and joined at shutdown. Existing cache limits are retained with an approximately one-second eviction delay.
9. Exploration/recommendation clients reuse transports for the same proxy configuration; changes replace the transport and close idle connections.
10. The server binds and serves the shell/login/diagnostics before ordered downloader/plugin initialization completes. Business endpoints return `503 SERVICE_STARTING` until registration finishes; the UI automatically resumes. Shutdown joins initialization before closing stores. Selected native display GET requests carry a 30-second context budget; this is cooperative cancellation, not forced termination of synchronous plugin side effects or a universal hard HTTP timeout.

## Measured local evidence

Measurements below are local synthetic/build measurements on macOS/M1 Pro, not production or browser first-paint timings.

- Initial HTML direct JS/CSS references: 3,123,710 → 741,646 bytes, a 76.3% reduction. Gzip total: 227,786 bytes. Route assets subsequently loaded by navigation are not included.
- Transfer history, 100,000 sources, count plus 50 hydrated entries, minimal synthetic JSON and no filesystem checks: old full-ranking query 2.702 s; current projection 0.757 ms. Refresh one changed source: 1.153 ms. These numbers should not be treated as a production speedup guarantee.
- Media decoration, 20 cards against 300,000 asset/subscription/intent records: 0.450 ms/op after setup and teardown are excluded.
- Per-entry file protection check: 32,502 → 799 ns and 54 → 4 allocations in the synthetic comparison.
- Loopback HTTP test: three independently acquired exploration clients reuse one TCP connection.
- The actual Vuetify virtualizer returns at most 15 window items for 10,000 rows in a 620px viewport, including middle/end positions and variable heights.

## Regression validation

- `npm run build` passed.
- Frontend tests: 572/572 passed.
- `go test ./web` passed, including gzip/HEAD/Range/cache behavior.
- Focused history, reorganize, display reads, subscription coverage/words, directory counts and query-plan tests passed.
- Focused image/file safety and concurrency tests passed with `-race`.
- Startup/authentication tests passed with `-race`, including unavailable business endpoints, ready-state recovery, auth/origin and proxy-cookie scope, blocked plugin registration locks, and cooperative deadline scope. A deadline during the session query returns 504 rather than a false 401; the session remains usable.
- `go vet -p 2 ./...`, `go mod verify`, and the dateparser normal/concurrent-locale checks passed.
- An independent backend review found no actionable regression in projection invalidation/snapshots, subscription batch semantics, image lifecycle or startup joining.

## Verification limits

- Actual Chrome rendered the isolated safe-mode setup/login shell. An open extension UI blocked further browser control. Authenticated lazy routes, real scroll geometry, wrapped filenames, keyboard/mobile menus and dashboard layout remain unverified in a browser.
- ApexCharts remains an on-demand chunk above the bundler's 500 KB warning threshold.
- No production traffic benchmark, installed-plugin startup acceptance, NAS/download-client test or deployment was performed.
- The full repository suite is not green. Existing fixture/provenance failures include missing pinned native implementations, stale source digests and SDK `invalid_manifest` errors.
- A fixture-bound candidate/baseline comparison covered 1,346/1,377 candidate root tests (1,010 passed, 300 failed, 36 skipped) and 1,329/1,356 baseline roots (995 passed, 298 failed, 36 skipped). The initial runs reached their 12-minute watchdogs; segmented continuation hit the same existing panics on both versions. The broad comparison left 31 candidate and 27 baseline roots unfinished. Some of these history/word tests passed independently in the focused runs described above.
- The two initially candidate-only failures were reproduced on the baseline: a `site_connection` uniqueness race and duplicate `site.refreshed` events from an unjoined asynchronous test consumer. No unresolved candidate-only failure was identified; this is not a claim that all tests passed.
- Local evidence remains under `work/`, including `regression-checks/final-summary.json`, raw test JSONL, the compared root sets, and the limited baseline Go overlay. The 45 generated fixture binaries and temporary test directories were cleaned after testing; no test server was left running.
