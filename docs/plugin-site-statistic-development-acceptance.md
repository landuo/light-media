# SiteStatistic development acceptance

2026-10-22. Evaluated code: `4e50b3040c7cdb146097953447687f5aa355898e` on `dev/plugins-continuation`. This is a development assessment, not production migration or real-account acceptance. The shared plugin framework's new clone creation/reset/update UI is a separate workstream; this document does not claim that framework work is complete.

## Acceptance matrix

| Area | Evidence and outcome | Remaining boundary |
| --- | --- | --- |
| Source behavior, configuration, statistics/history and refresh | Frozen SiteStatistic 1.9.7 reference; existing account/history and native collection verification documents; real SDK and mounted API/job paths | Private source artifacts are not re-exported; shared core site observations are intentionally shared, not clone-specific copies |
| Notifications and deduplication | Original post-message/last-notify ordering, source-bound Router and durable queue; same-instance generation/config checks, distinct-instance isolation, rollback, unknown-send and cold-recovery tests | No real recipient/account delivery; unknown effects do not automatically resend |
| Error classification and system messages | Four supported native parser failures, provenance-bound SystemError, shared helper/FIFO, authenticated SSE, real API and scheduled-job paths | Go diagnostics deliberately are not fabricated Python tracebacks; fixed public Indexer display-name provenance remains explicit |
| Default links and application address | Existing default-link and AppDomain frontend/backend verification | No production domain guessed or configured |
| Affected backend verification | Ordinary, exhaustive race and modernconly each 166 root / 665 inclusive passed; native sites ordinary/race 64 root / 340 inclusive passed; vet/server/23 CLI builds passed | One private stdin case skipped; no whole-repository green claim |
| Frontend verification | All 200 tests, type check and build passed, including 22 system-message cases | These are automated module/render tests, not all browser actions |
| Actual browser | Same built server, owned synthetic data: login, authenticated SSE, bell layout, system/task tabs, three message role icons/order/safe literal HTML, local read marking, logout and re-login empty state; SiteStatistic install and disabled configuration dialog | Three visual message payloads are a display fixture; backend production has separate real SDK/API/job evidence |
| Background lifecycle | Production-feed tests assert 4999ms preserves stream, 5000ms closes it, 60 seconds hidden never reopens, foreground opens exactly once; hidden startup respects the original 3-second delay; logout/unmount/beforeunload clear timers/listeners; stale callbacks cannot enter the next login | Native tab changes and true window minimization did not emit hidden in this cloud browser. No synthetic event was injected into the real browser, and native background recovery is not claimed. Reload emitted pagehide/hidden, which is not equivalent |
| Instance isolation | Existing real SDK/API/config/KV/history/queue and recovery tests bind current instance, generation and source; request-provided instance cannot replace verified origin | Ordinary generic clone creation and lifecycle UI is tracked separately by the shared framework task, not duplicated inside this plugin |

The plugin's own implementation and necessary layered validation are complete within these stated development boundaries. The absence of a native hidden event is an environment observation limit, not a demonstrated code failure; it is covered by explicit production-module lifecycle tests rather than relabeled as a browser pass. Production handoff, real accounts and private stdin migration remain unperformed. The repository's pre-existing missing binary files were not fabricated, replaced or deleted; none was silently removed from these reported gates.

## Repeatable same-desktop browser route

1. Freeze the exact source and built server hash. Use an owned temporary application directory with synthetic accounts only and no external site, downloader or message-channel configuration.
2. Start the bounded test server from a terminal on the same assistant cloud desktop as the browser. Bind only to loopback. A sandbox-local listener on a different network namespace is not sufficient.
3. Open that loopback address in the assistant's cloud Chrome. Exercise actual controls and record screenshots plus narrowly scoped server/proxy events. Do not log authentication headers, cookies or credentials.
4. If a display fixture is needed, label it separately from backend message-production evidence. A passive observer may record native visibility events, but must not change visibility or dispatch fake events in browser acceptance.
5. Stop and reap every owned process; retain evidence. The primary browser run ended server exit 0, both proxies -15. A bounded visibility run required server -9 after shutdown grace; the subsequent minimize run ended server 0 and proxy -15. All children were reaped; these results are not hidden as universal graceful exits.

Server SHA-256: `30cd30136a8d454046f632110d8f9ea27e882bc7183c6788ecc61e93c868983e`.
See `plugin-site-system-message-chain-verification.json` for machine-readable results and the earlier input hashes. No security setting, public exposure, user computer or production service is required by this route.
