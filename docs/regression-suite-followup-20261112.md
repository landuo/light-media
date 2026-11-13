# Regression follow-up, 2026-11-12

## Scope and baseline

The first complete, source-frozen macOS run used `18b237f08a4f4f4c06fd6130588cd8fa12f07996`, which contains the performance changes and deferred initial subscription checks. All 209 shards completed across 163 packages: 2,782 expected roots were observed, with 2,675 passed, 77 failed and 30 skipped. There were no timeouts, missing/unexpected roots or source changes during that run. Every failed shard was in `internal/app`.

Those numbers describe the original run, not a passing result for this follow-up. Full-suite, focused, race, source-oracle and browser evidence are separate. macOS did not execute the 51 Linux-specific test files. Optional browser fixture branches and production/NAS integration remain separate validation requirements.

## Production corrections

### Valid cross-device fallback jobs

The strict auto-copy decoder accepted only `id` and `file_ids`, although real import submissions also carry `original_scrape_batch`, `_parent_job_id` and `_request_id`. The decoder now explicitly recognizes those three fields. Unknown fields remain rejected, and actual job payload, generation, plan, cancellation, reservation and grant checks remain mandatory.

### Partial publication and safe overwrite cleanup

The import loop previously tried to finish every overwrite, including failed tasks that had intentionally not been registered. That masked the original filesystem/EXDEV error with an overwrite-registration error and could prevent recovery when only a sidecar failed.

Each transaction now persists its exact successful cleanup keys alongside its task results, ownership and plan. Cleanup only uses those keys, validates that each belongs to the plan, and retains the original registered-state, sealed receipt, path, policy and inode checks. An empty list means no successful publication; absent keys in legacy records retain the old conservative recovery behavior. This is not a migration that invents success for old mixed-failure records.

When an automatic main transfer changes from hardlink to copy, a sidecar already successfully registered as a hardlink retains its sealed actual method. This exception is restricted to that transition and registered/complete operations. It does not authorize an unregistered task or bypass the existing publication checks.

Regression coverage includes real EXDEV, explicit-hardlink rejection, fabricated public errors, cancellation/lost acknowledgement, cold recovery, main/sidecar mixed success, precise per-file syscall counts, and foreign/unregistered cleanup-key and unknown-payload rejection. The new mixed-publication test fails on the unmodified baseline with the masked-error symptom. An independent code review found no actionable defect in the production patch; that review did not count as another test run.

## Test and fixture corrections

- CLI readiness: login/auth status is deliberately available before plugin initialization completes. Fourteen startup probes now wait for `/healthz`; subsequent business assertions are unchanged.
- Notification failures: source dispatch consumes ordinary module exceptions. Tests now retain the expected queue result while requiring actual error logs and the original caller attribution, rather than inventing a failed queue delivery.
- Auth getter isolation: a dedicated immutable-config SDK variant separates getter tests from host config reads. A positive control verifies getters after removal of display metadata, while a live-provider negative control still requires that metadata.
- Event-capable workers: transfer and catalog source-oracle tests explicitly select real workers that declare their required handlers. The default worker remains unchanged for unrelated tests.
- Cold callback recovery: unfinished work reclaims the durable job after restart and checks a newer execution generation. Already succeeded jobs must remain rejected, with no repeated provider calls and unchanged successful history; tests do not force completed jobs back into the queue.
- Site HTTP parallelism: dedicated parallel worker binding preserves the physical overlap, RPC, SQLite and scope-revocation assertions. It does not globally substitute the ordinary worker.
- Raw JSON state: a dedicated worker variant retains raw numeric representation instead of prematurely converting it to float64.
- Automatic season packs: synthetic source-shaped metadata is served through the normal metadata fixture; no real credentials or provider requests are needed.
- Directory tests: queued files run the actual asynchronous recognition stage before assertions. Conflicts must persist a failed plan/job without assets, reservations or a target.
- File numbering: explicit numbering remains authoritative when auxiliary episode metadata is absent, matching the existing source contract. Preview still cannot publish or change source files.
- Quality imports: every durable single-file job is checked for plan membership and uniqueness and executed before checking all pack assets. The prior test ran only the newest file job.
- Site refresh: the fixture explicitly models absent homepage/favicon routes, joins save-triggered work and uses an atomic failure switch. Last-success, unknown-value and history assertions remain intact.
- Worker isolation: real previewed/submitted import plans occupy organizing workers. Missing plan IDs are no longer used to bypass queue validation.

## Reproducible prerequisites

The runner now discovers indirect source fixture variables and requires explicit prerequisites before starting tests. Actual PluginUnInstall, MPServerStatus and AutoSignIn integration tests use independently built business plugins and their real SDK from the pinned `landuo/light-media-plugins` checkout. Small retained test entrypoints do not replace that SDK. A caller can provide a pinned `PLUGINS_ROOT`, or explicitly supply precompiled binaries with SHA-256 values; the latter is recorded as supplied binary provenance, not a reproduced source build.

Retained source-view cases and response evidence are immutable synthetic source-oracle inputs, not newly generated expected results. See the associated testdata READMEs and origin manifests. No machine-specific absolute path is required by the committed runner.

The Go toolchain used here ignores a linked worktree's `.git` file while finding VCS metadata and can stamp the outer checkout's revision. Build provenance therefore records actual package directories, GOMOD, selected HEAD and source hashes, and warns when the embedded revision differs. The stale stamp is neither evidence of an old source build nor silently presented as the tested commit.

## Verification boundary

Before integration, focused corrections passed on real test processes and synthetic owned data, including six real CLI roots, the external plugin/source matrices, worker-isolation repeats, and EXDEV/overwrite normal and race groups. The combined transfer-fixture and production overlay passed 56 roots in 132.8 seconds without skips. These focused results do not replace the final whole-repository run. Complete rerun results and remaining skips must be reported separately after the final source snapshot is frozen.

### Recovery verification, 2026-11-13

The original 77 failed roots, with the single explicit test-name rename, plus four added roots were enumerated and rerun: **81/81 passed, zero failed/skipped/missing/unexpected roots**, with every shard exiting 0. The completed-job cold-retry tests also passed three repetitions each under the race detector. All 109 fixture bindings were rebuilt (89 unique builds), including the pinned external sources and identity/hash checks. The only unresolved build recipe is the explicitly optional browser Render fixture.

The ordinary execution environment rejected a local child-HTTP scenario. The same unchanged test passed through the executor's approved local-test execution path, followed by the complete 81-root run through that path. No proxy or system network configuration was changed to obtain this result. These are still local synthetic/protocol results, not browser or production acceptance.

At this checkpoint the follow-up has not yet completed a second whole-repository run. The 81-root result must not be presented as a whole-repository pass. The changes have no file-path overlap with the intervening refresh commit `a4396ed176b34977e0b14d4afafb1e553dd6ac5a`; final main-branch integration is a separate step.
