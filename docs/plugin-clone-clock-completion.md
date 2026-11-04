# Plugin clone and scheduler whole-parent completion

This delivery composes the accepted whole scopes of parent 15 (independent clone instances) and parent 38 (plugin scheduled services) on the previously delivered S08 commit `c553f698`. Earlier accepted source, SDK, CLI, cold-start and browser evidence is preserved. The final composition does not replay or relabel those historical matrices.

## Product composition

- Clone name and description retain approved literal display text. Display version and icon remain distinct from executable identity and safe resource loading. Folder/cache/SQL failure behavior and independent clone lifecycle are retained.
- Scheduler support includes the accepted CalendarInterval, AND/OR and nested trigger construction, bounded/cancellable evaluation, fault visibility, independent task execution, manual-run error retention and resave recovery. The previous interval/cron/date and source ordering contracts remain intact.
- The actual HTTP server now roots requests in the generation context. Existing cancel, HTTP drain/close, App.Close, Store lifetime and failure reporting order are retained.

## Final-composition verification

- Actual frontend typecheck, all 22 clone UI tests and Vite build passed. All 8,364 bound inputs were unchanged. Old tracked hashed assets were retained; the new index references the freshly built assets.
- Five server roots, six existing SDK/clock/SSE boundary roots and five clone/builtin-clock/S08 roots passed both normal and race execution. The finite checks use ordinary worker binaries; they are not claims of race-instrumented workers or an all-package test pass.
- `go vet` passed for the affected server, App and schedule packages. Changed Go files passed gofmt checking; the source diff passed whitespace checking. The full staged diff flags eight trailing-whitespace lines inside generated Ace/Vuetify CSS template strings; the exact built assets are retained rather than edited after build.
- The actual ordinary server was built from the combined delivery source and new embedded frontend. Its SHA256 is `81f3288fce4dbc8b2a01bb1bfabe8d3ea10b7684ef3f529bc5e0f1769d7ccbd5`.
- Existing owned data and the existing signed-in browser session were reused in normal mode. All three installed fixtures remained present. The clone card rendered literal markup as text with its fallback icon, and the saved clock task showed waiting with a future execution time.
- Owned stop returned the original subprocess Wait exit 0, generation `close_reason=stop`, `wait_success=true`, empty stderr, and unchanged installed inventory. The browser page remained open during stop.

## Evidence and boundaries

`migration/evidence/plugin-clone-clock-completion/` contains the explicit safe evidence index, acceptance summary and compressed selected receipts. No credentials, accounts, private databases/logs, dependency trees or executable binaries are included.

Historical nonzero shutdown receipts and the detached-context negative control remain intact. The TCP positive/negative pair establishes the verified cancellation mechanism, without retroactively identifying the exact request in each historical failure. Browser Developer Tools were not used, and active browser SSE was not independently observed. No actual supervisor subprocess restart cycle was added here.

This closes these two agreed parent scopes after final independent review. It does not claim whole-project final acceptance, live NAS/vendor acceptance, deployment, or a main-branch merge.

The Vite build emitted its existing future config-loader and chunk-size advisory warnings (700 bytes of stderr). These are retained in the archive; the build exited 0. Other recorded positive Go commands have empty stderr.
