# Refresh startup isolation

Baseline: `18b237f08a4f4f4c06fd6130588cd8fa12f07996`.

## Change

Previously every full refresh awaited authentication, then both full settings and
six global database counts, before mounting the selected route. Startup business
readiness used the same statistics response and a 15-second refresh interval.

The authenticated `/api/ready` now reports startup state without global statistics.
Authentication and this lightweight readiness check remain mandatory. Full settings
and statistics load independently. Browsing, plugin pages, logs and history can
render without settings; pages that initialize forms/selections from settings keep
a visible, retryable local gate. Explore search and history reorganization obtain
settings when needed, without publishing stale responses after unmount/logout.

Startup reads have a 10-second client budget, a shared session cancellation scope,
and generation guards. Server auth/status, ready, settings and status reads carry
the existing 30-second cooperative context budget; admin and settings-policy queries
honor it. This is not a forced timeout for arbitrary synchronous Go mutex work.

While startup is pending, one readiness probe runs every 1.5 seconds after the prior
response. Probes stop on ready, failure, logout or unmount. Failures show a retry
control rather than silently opening business pages. A later statistics response
reporting a service restart closes the gate and resumes readiness probes. The
ordinary statistics interval remains 15 seconds, without overlapping requests.

## Validation and limits

- Frontend build and Vue/TypeScript checking passed in the isolated cloud worktree.
- Focused real-SFC tests cover slow settings/statistics, local configuration gates,
  startup recovery, failed/malformed readiness, anonymous users, late logout data,
  overlapping bootstraps, login/setup and service restart.
- Go bootstrap/startup/deadline focused tests passed using official Go 1.26.0.
- Independent review identified and repaired restart-state handling and a history
  dialog settings response that could outlive logout.
- Full frontend tests use `TZ=Asia/Shanghai`: an existing log-oracle fixture assumes
  that timezone and fails by one hour in this cloud computer's default JST.
- This is synthetic/local regression evidence, not an authenticated production
  browser measurement or a claim of a particular real-world speedup. No production
  configuration, media, download clients or database was touched.
- Unrelated broad Go fixture/provenance failures are not claimed fixed by this work.

Final validation: `TZ=Asia/Shanghai npm test` passed 586/586; `npm run build`
passed; `go test ./web -count=1` passed. The focused Go command was
`go test ./internal/app -run '^(TestReviewedSiteMessage|TestBootstrap|TestStartupHTTP|TestNativeDisplay)' -count=1 -timeout=90s`.
