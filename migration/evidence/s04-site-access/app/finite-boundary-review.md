# S04 access-results App candidate02 — finite static review

**READY for the proposed three-root App compile/normal gate. No new execution or product write by this reviewer.** This is static admission for the fresh, ordinary int64 producer slice, not runtime acceptance, all S04, installed SDK or legacy-row migration.

Candidate manifest `audits/s04-app-implementation/candidate02.json`: `f9c216f7ddb4af6e82b0ec88286d62de3b9517a770019faa455b486f784312d9`. Its four current files independently match their frozen hashes. Candidate01 was never executed and remains historical; its incorrect claim that malformed note could be ignored by fail is superseded by `CANDIDATE02-CORRECTION.md`, not inherited.

## Source and algorithm correspondence

Fixed 8a83 `SiteOper.success/fail` (225–285), `SiteStatistic.get_by_domain` and eight-column schema, and original Base.create/update plus db_query/db_update were read. The real model query hydrates the whole row before the method body. Pinned SQLAlchemy `JSON.result_processor` (sqltypes.py:2812–2823) invokes json.loads for non-null storage. Thus malformed JSON can reject fail even though fail does not reference note explicitly. Candidate02 now reads/checks note before incrementing fail. Valid finite JSON of any shape is left byte-for-byte untouched, and only fail/state/time are updated.

The new table has a real SQLite INTEGER PRIMARY KEY, nullable source columns and a nonunique domain index. Lookup is exact `domain=? LIMIT 1`, with no normalization, site-existence test, upsert/unique key or additional ordering. A selected existing row is updated by its actual id. New failures write null seconds/note; successes use the separately tested pure transitions. Success updates only success/seconds/state/time/note and preserves fail; fail preserves unrelated fields. Transaction rollback is retained on errors and precommit cancellation.

The adapter's existing-success input is a nullable int64 timing and an int64-valued note map. This is an explicit **fresh producer invariant**, not support for arbitrary legacy ORM rows. In particular source success(None) can copy `{"x":null}`, whereas this adapter rejects that row; some other valid JSON shapes and loosely typed SQLite scalar storage also are not equivalent. No existing-row import/backfill or complete DAO migration is claimed or approved here. This known outside-slice difference must not disappear from the final scope wording. First-party rows created by these two methods retain the supported shape.

The pure implementation preserves same-second replacement, ordinary negative/zero/None behavior, Python floor division, the pre-truncation denominator and zero-average fallback. Counter/storage overflow is an explicit error, not wrapping. This review checks its use by the SQL adapter; previous pure/SDK runtime credit remains separate and is not re-executed here.

## Actual authority, lifetime and timestamp path

- The existing resource dispatcher retains its initial scope/context and exact identity checks. Only the two declared resource capabilities and their dispatch are added; the base contract is not expanded.
- Input token scanning rejects duplicate/unknown fields, trailing values and malformed objects. Domain must be a scalar Unicode string, without trimming/nonempty/site restrictions. Success allows absent/null/exact int64 seconds; fail rejects any seconds field. Successful reply is exactly null.
- Admission is acquired before taking pluginMu and is balanced by wg.Done. Close seals admission first and waits before closing Store. No new goroutine, timer, worker callback or network client is introduced.
- Under pluginMu, the real scope validator runs before BeginTx. The existing transaction-aware guard checks current object/installation generation using the held transaction both before and after the write. It does not call the ordinary DB-backed validator while holding the Store's single SQL connection. Commit is followed by scope/context checks after the transaction releases that connection.
- One `time.Now().Format(...)` is captured before the first DAO query and supplied as ordinary producer data to the mutation function. There is no mutable global test clock or client-supplied clock capability.
- Cancellation/revocation reported after commit is not a rollback guarantee; no retry is made. No actual lock/cancel race result is claimed from this static review.

The old-file patch is exactly schema concatenation in store.go and the two-capability predicate/dispatch in plugin_original_resources.go. No unrelated permission, environment, site configuration, source collector or ledger modification belongs to this candidate.

## Exact proposed tests / extra runtime input

1. `TestSharedSiteAccessActualSourceSQLiteAndCold`: 11 original scenarios / 19 source calls, same seeds/timestamps, all rows before and after through real owned SQLite, then a real Store close/reopen. Explicit producer timestamp input is not an invented global clock seam.
2. `TestSharedSiteAccessSQLiteFailureAndRawPreservation`: valid raw unrelated fields preserved by fail; malformed ORM JSON rejected; explicit unsupported note boundary; int64 overflow; owned trigger-raised SQL failure and recovery; cancelled transaction; selected-row-only duplicate-domain update. Trigger creation affects only the new owned test database; there is no OS permission/escape probe or original database operation.
3. `TestSharedSiteAccessResourceFiniteBoundary`: actual resource handler and real installation row with an explicitly supplied authority closure, malformed requests, exact domains, local timestamp window, stale generation, cancellation and closed admission. This is not an installed worker or proof of normal manifest-grant negotiation.

The first test reads `../plugins/siteaccess/testdata/source-access-results.json` at runtime. Independently compared: it is byte-exact to `evidence/s04-source-warm01/stdout.json`, SHA **9303332121f5265e8881f3330aa785d32d08aa4c2e00054111f41954fadc9fe3**. Bind it separately before/after execution: a go-list source closure does not include this os.ReadFile input. Run the App test program with cwd `release-light-media/internal/app`. The original full raw includes metadata/errors beyond the subset decoded by the test; no claim that all raw source error cases are replayed here.

These roots use OpenStore/newTestApp and owned fixtures; no hidden Go build, worker launch, source import, environment rewrite, HTTP endpoint or real credential is introduced. Existing newTestApp cleanup and statistics-disable helper were checked. No additional root or historical suite is needed for this static admission. Actual compile, three root results and their closure bindings remain required before runtime acceptance.
