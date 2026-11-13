# Regression follow-up integration into main

## Preserved history and scope

This merge combines current main `bcf63eaad29390645225bacaae938ea263e6994a` with regression follow-up `cc273310b83ec42935be4d6090f56c617fa0f02a`, whose parent is `18b237f08a4f4f4c06fd6130588cd8fa12f07996`.

The two lines of development have no changed-file overlap. Before adding this report, the merge's patch against current main was byte-identical to the follow-up patch against its parent (SHA-256 `d49e64a251907bd17029739089114af9ecec2c2bad5771c480f00c0c7dbcc363`). No conflict resolution or further product-code change was required. The tested candidate tree was `53648296013cd7012874d97c3859f64780bba08d`.

Later refresh/performance, proxy and season metadata, subscription, and bounded Cookie-save changes are retained. The existing 500-subscription enumeration limit is unchanged. Creating a subscription still defers its first check to the normal scheduler; explicit checks remain available. No production deployment, user worktree reset, database migration, or live media operation was performed.

## Verification on the integrated candidate

- Rebuilt all 109 fixture bindings (89 unique builds), including pinned independent plugin sources with source, hash and identity checks.
- Related host regressions: **131/131 roots passed**, no failed/skipped/missing/unexpected roots or subtest skips. This includes the original 81-root repaired selection plus subsequent-main, transfer, site-save, proxy and subscription-cadence coverage.
- Host race selection: **62/62 roots passed**, no failed/skipped/missing/unexpected roots or subtest skips (108.223 seconds). Host code is race-instrumented; separately spawned fixture binaries use the ordinary rebuilt artifacts, not an additional fixture-wide race build.
- Full `-race` package runs: `internal/networkproxy` 26 passed; `internal/catalog` 61 passed and 1 skipped; embedded `web` 2 passed. The skipped `TestExploreLivePublicProviders` is an existing opt-in external-provider probe (`LIGHT_MEDIA_EXPLORE_LIVE` unset), not a successful live check.
- Frontend: **603/603 passed** with `TZ=Asia/Shanghai`; Vue type check passed. Frontend source and published assets are unchanged from current main; embedded-asset checks passed.
- Root-module current-platform compile check: all **163 packages** completed `go test -run '^$' ./...`. This is explicitly a compile check, not execution of their test suites.
- Regression audit helper: 3/3 tests passed. Tracked source remained unchanged during validation.

## Remaining validation boundaries

These are isolated macOS, synthetic/protocol and source-fixture results, not a second complete whole-repository test run. Linux-only files, optional browser Render fixture, actual browser workflows, live providers and production/NAS acceptance are not claimed passed. The optional Render fixture remains without a supported build recipe, so preflight retains its documented limitation status. See `regression-suite-followup-20261112.md` for the original full-run failures and their fixes.
