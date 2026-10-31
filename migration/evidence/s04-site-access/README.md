# S04 finite site-access statistics bridge

Extends 7b9b1d0. This is fresh ordinary SiteOper.success/fail support, not sign-in, full SiteOper/S04, historical-data migration, UI acceptance or a remote delivery claim.

## Implemented scope

An installed Go worker calls SharedSiteAccess through fixed site.access.success / site.access.fail capabilities into a real private SQLite table. Exact domain strings are not normalized or checked against configured sites. A single local wall-clock timestamp precedes the query. New/old counters, None/zero/negative finite integer timings, same-second note overwrite, descending ten-key trim, pre-trim averaging denominator, Python-style negative floor and zero-average fallback retain the selected source behavior. SQLite assigns the row ID. No site traffic is required or performed.

Fail preserves existing success/seconds/note values, including valid raw JSON shapes; malformed JSON is rejected before mutation to preserve the source ORM hydration prerequisite. Candidate01 had the wrong malformed-note expectation but was never compiled/executed. The original draft is retained externally and candidate02's correction is recorded, not fabricated as a failed run.

The finite Go producer uses int64 counters/timings and an int64-valued timing map. Arbitrary legacy nullable/loosely typed columns, non-finite JSON and other note shapes are not migrated or certified. In particular source success(None) can preserve {"x":null}, whereas this finite adapter rejects it. Existing rows are not fabricated/backfilled. Post-commit cancellation or revocation may report an error after an effect; no automatic mutation replay or rollback guarantee is claimed. The current invocation, transaction and App-close admission boundaries remain enforced. Generic request/response budgets remain explicit.

## Actual evidence, not just compilation

Fixed original MoviePilot 8a83efe selected complete ORM/decorator/component bodies ran against owned SQLite with the existing pinned SQLAlchemy/greenlet/typing dependencies: 26 nodes, 391 bound inputs, eleven scenarios/nineteen normal calls, five real database errors and eleven readonly cold queries. A controlled clock is explicit. Full original app, site traffic and unrestricted Python runtime are not certified. The entire warm raw appears once as internal/plugins/siteaccess/testdata/source-access-results.json; full cold raw/input maps remain in the externally saved source checkpoint. No new full app/requests/urllib3 imports or network probes were used.

Target gates actually passed: pure two roots (1468 inputs), recording-HostCaller SDK two (1928), real App/SQLite three (6218), installed-worker two (6219), and host-race three (6228). The earlier App three-root gate remains a separate input closure; it is not relabelled as a later all-roots run. Host-race used the separately hashed normal worker, not a race-built worker. Input maps are not claims that all dependency tests ran.

The normal CLI actually started hosts26 then51 on the same isolated DB, both natural exit0, with installed workers44 then66. Four business calls covered None/fail/nonzero timing, fixed malformed-frame rejection then same-worker zero-timing recovery, one expired prior HostCaller rejection without extra stored increment, and cold fresh-worker persistence. Readonly restricted observations compare all eight columns of two owned domains. The two GET unsupported-method checks are not additional business calls. The fixed peer supplied only one index and one worker artifact, with no forwarding/CONNECT/real service. The original environment proxy/CA was unchanged. Ordinary API counters are logical HostCaller attempts, not physical worker-pipe instrumentation.

Normal server3974-input and fixture1086-input builds bind actual program hashes. The server child build/result reports exit0 and current full-input/binary readback is equal; its original outer tool terminal output was truncated and unavailable. No outer exit or chunk is reconstructed. The later service gate has its own actual terminal receipt. Compiled existing UI assets are not new browser acceptance.

## Retention and remaining scope

The index binds exact product bytes and compact exact original records. Large full compile/dependency/generated-source maps, source cold raw and draft history remain in acknowledged external Space checkpoints; binaries, databases, keys, credential responses, host logs and unrelated Browse/S06 work are excluded. The owned private DB is retained locally, not exported. Current CLI raw HTTP bodies remain locally retained and will be checkpointed separately; the structured business responses and all eight-column observations are in the included original result.

S04's five other SiteOper reads and SiteChain.refresh_userdata remain open; existing history/identity/reverse-refresh credit is preserved. This does not expand the agreed seven-DAO-plus-one-refresh scope. The global ledger still has inherited unresolved structural issues, so neither full migration nor a global final/CI-green result is claimed. Remote remains the last confirmed11f922 pending the Mac delivery channel.
