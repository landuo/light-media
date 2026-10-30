# S07.CustomIdentifiers — scoped shared configuration

## Selected boundary

This is the **RemoteIdentifiers shared get/set + native editing + selected WordsMatcher consumer** slice, not all S07 or S11 and not the complete RemoteIdentifiers plugin (remote HTTP fetch/scheduling belong to its own migration). Final independent review and browser acceptance are separate gates. The exposed one-key SDK also accepts broader JSON inputs, whose named serial/failure/cold fixtures are tested; this is **not a claim of arbitrary SystemConfig DAO concurrency equivalence**. In particular, a generic synchronous delete/recreate racing a pending UI UPDATE can depend on other source keys' row IDs; that counterexample remains an open parent-S07 dependency outside the fixed RemoteIdentifiers producer contract. No global Settings identity index was invented, and historical source row IDs were not guessed. Fixed MoviePilot backend: `8a83efeacd3e26b82be495e17d50146e31c96a60`; frontend: `2829e3b08214348f05ff2f5beae644882eccad58`. Source entry points are `app/db/systemconfig_oper.py`, `app/db/models/systemconfig.py`, `app/db/models/__init__.py`, `app/core/meta/words.py`, `app/api/endpoints/system.py` and frontend `WordsView.vue`. The source observations execute normally imported default DAO/SQLAlchemy SQLite and WordsMatcher in new owned processes. They are not original Manager/full authenticated daemon tests. UI response-schema observations are independent owned ASGI routes using the original Response schema, not the original authenticated route.

77 installed Python files were inspected. Enum callers use CustomIdentifiers, IndexerSites, RssSites, UserInstalledPlugins and Notifications; two ordinary direct-SQL consumers also refer to Downloaders/plugin.BrushFlowLanduo. Only CustomIdentifiers is granted here. Other ordinary keys remain separate work; credential, account and access-control values are not exposed.

## SDK and host

`plugins.SharedSystemConfig{Host: currentCaller}.Get(ctx, key)` returns `json.RawMessage`. `Set(ctx, key, value)` returns `*bool`: true for a changed DB operation, nil for source None. Both methods require the exact declared capability (`system.config.get` or `system.config.set`). Only key `CustomIdentifiers` is accepted; unknown/empty keys never mean all settings. Each operation authenticates current identity/config generation and lifecycle scope before effects. Only the trusted already-authenticated close closure can finish against a replaced installation; ordinary stale callbacks cannot.

Use `json.RawMessage` for ordered objects. A Go map has no caller insertion order. Do not decode a returned ordered object into a map and then claim its reserialization is lossless. Object values and all JSON scalar/container types are preserved, even when unusable as rules. Decimal/exponent numbers follow Python binary64 decoding, integer literals retain arbitrary precision before the source SQLite JSON affinity on cold reload. Nonfinite floats remain internal sealed facts under the separately documented numeric boundary. Lone UTF-16 codepoints are retained as standard JSON escapes in internal Get/Set transport; no public tagged-value API or WTF-8 string is introduced.

The singleton shares the existing encrypted `recognition_words` fact with native readers/writers, not BaseKV or a second persistent table. A temporary SQLite JSON-affinity projection is connection/transaction-local. Source sync and async writers use independent locks: async takes a real SELECT snapshot without retaining a transaction or sync lock, then mutates and publishes under a short cache lock. SDK Get is pure hot-cache access; native revision metadata is read separately from the DB. SQLAlchemy unchanged-attribute commits do not issue a physical UPDATE. Source sync cache-before-DB behavior is intentional: failed SQL leaves the new hot value visible; equal repeated set on an existing row returns nil without repairing its DB; cold reload restores persisted data. An absent row still inserts an equal falsey value. Existing-row falsey changes delete the row. Object equality ignores key order, but equal reordered input changes hot iteration order without changing stored order. Native typed edits invalidate/reload the same cache. Source duplicate-key states are represented in the same sealed fact with a distinct authenticated internal envelope, not a public JSON tag or another KV table. Ordinary legacy single-row ciphertext format remains unchanged. Async reads with multiple rows fail; source snapshot updates of deleted rows fail, rather than silently upserting. Native typed list writes cannot silently discard a duplicate row.

## UI and actual consumer

The authenticated Settings → Rules card uses `/api/settings/recognition-words`. Its text adapter matches source split-newline/filter-empty normalization; it does not trim spaces. Explicit save uses source async DB-first semantics, updates the shared cache only on success/equality, and broadcasts `config.updated` only for a successful changed DB operation. SDK sync set does not invent that event. A truthy non-list cannot be joined for display: the card shows the error and keeps manual editing/explicit repair available; it never automatically overwrites raw JSON.

Internal SDK Get is not a UTF-8 HTTP renderer: it returns source lone codepoints as JSON escapes in `json.RawMessage`, so an ordinary producer can retain and process them. Keep raw elements or use `DecodeSystemConfigJSONString` / `EncodeSystemConfigJSONString` for explicit codepoint processing; standard `json.Unmarshal` into Go strings is lossy here. The owned producer fixture keeps `[]json.RawMessage`, compares the ASCII marker over decoded codepoints, and sends the unchanged raw elements back. `ValidateSystemConfigUTF8JSON` is an explicit caller-side UTF-8 rendering check, not a global SDK JSON replacement. Its outer raw HTTP response still fails on lone codepoints. UI GET separately mirrors the original Response-schema projection: nonfinite values become null recursively **only in the response**, while lone codepoints remain an encoding error. The previously selected nonfinite internal-Get rejection remains a distinct numeric adapter; this slice does not claim a complete arbitrary Python-object DAO transport. Go error codes/HTTP envelopes are explicit adapters, not Python exception class/status identity.

The selected native consumer uses `prepareRecognitionTitleChecked` → `words.NewCodepointRules` → `PrepareChecked`. Normal UTF-8 uses the prior matcher. A private rune/WTF-8 branch preserves source surrogate codepoints through matching, captures and intermediate replacements; only a final non-UTF-8 title errors. It never equates a surrogate with real U+FFFD. The checked interface returns an error; existing string-only native helpers retain their prior empty-string fail-closed adapter. Python's full `(title, applied_rules)` JSON rendering is not the same as this title-only interface. Existing regex budgets/timeouts remain; source-ignored Python-strip-empty/raw-leading-# lines do not consume executable-rule budgets. The existing native subscription text editor retains its separate raw 64 KiB per-line / 8 MiB summed-line / 100000-line limits before invoking this matcher; these host form limits are not attributed to the original global source DAO.

**Open dependency:** full S11 Rust/default parser Options/cache and every MetaInfo entry are not migrated by this slice. The ordinary `[]string` Options projection explicitly refuses unrepresentable surrogate rules instead of changing them. This is not a claim that all recognition consumers accept all Python strings, nor a general regex-engine rewrite.

## Reproduction and acceptance gates

Run from the repository with Go 1.26 / current `go.mod`:

```sh
go test ./internal/plugins -count=1
go test ./internal/media/words -count=1
go test ./internal/app -count=1 -run '^TestSharedSystemConfig' -v
OWNED_SYSTEM_CONFIG_RACE=1 go test -race ./internal/app -count=1 -run '^TestSharedSystemConfig' -v
# Optional retained actual cmd/server stdout:
# SHARED_SYSTEM_CONFIG_PROCESS_EVIDENCE_DIR=/owned/evidence
cd web
node --experimental-transform-types --disable-warning=ExperimentalWarning --test tests/shared-system-config-words.test.mjs tests/settings-app-domain.test.mjs tests/settings-connection.test.mjs tests/plugin-market-settings.test.mjs
npm run build -- --outDir ../work/system-config-web
```

The tests compile actual standard workers and verified artifacts, install through normal HTTP, call typed SDK, use real encrypted SQLite, native consumer/UI routes, clones and lifecycle. Actual cmd/server tests perform three independent process starts with graceful stops and new worker PIDs. SQL triggers exercise failed update/delete/insert; hot/cold and no-repair are checked against original outputs. Source-owned expectations are in `source/oracle.json`, independent supplements and Unicode title-only observations. Test inputs are synthetic, offline/loopback only; no site/downloader/production account is used. Browser acceptance and final independent hash-bound review are separate gates owned by the coordinator, not implied by this document.

Source reproduction uses a fresh CONFIG_DIR and the pinned packages in `source/python-freeze.txt` constrained by `source/source-constraints.txt`:

```sh
mkdir -p "$OWNED_CONFIG"
env -i PATH=/usr/bin:/bin HOME="$OWNED_CONFIG" CONFIG_DIR="$OWNED_CONFIG" \
 PYTHONNOUSERSITE=1 PYTHONDONTWRITEBYTECODE=1 LANG=C.UTF-8 TZ=Asia/Shanghai DB_TYPE=sqlite \
 PYTHONPATH="$FIXED_SOURCE" "$VENV/bin/python" source/source_driver.py source/ordinary.json "$OUTPUT"
# Same CONFIG_DIR, new process for source/cold.json. Unicode consumer uses
# source/source_unicode_consumer.py and source/unicode-consumer-input.json.
```

Transport remains bounded by the platform's 16 MiB value / 16 MiB + 4 KiB logical-message ceilings and negotiated physical chunks/session budgets; the envelope reduces maximum usable payload. This entry adds no generic 128 KiB cap. A source and target 700 KiB+ ignored-comment rule list is exercised through warm/cold storage and real matching. Existing native executable-regex budgets are retained and are not source-unbounded equivalence. The product does not require Python.

### Mixed-write source commands

Using the same isolated environment above, `source/independent/` contains exact independent drivers (no target-derived expectations). Use a fresh CONFIG_DIR for each main scenario, then the same directory in a new process for its `cold` mode:

- `source_mixed_set_interleaving.py OUTPUT` — completed async SELECT, then real sync set, then async publication.
- `source_mixed_set_cases.py delete-existing|delete-recreate|absent-insert OUTPUT`; follow with `cold OUTPUT`.
- `source_mixed_identity_probe.py other-high-id|two-queued OUTPUT`; follow with `cold OUTPUT`. The high-ID case remains an **unclosed target dependency**, not a passing test claim.
- `source_duplicate_delete_probe.py duplicate-delete OUTPUT`; follow with `cold OUTPUT`.
- `source_driver.py dirty-input.json OUTPUT` — actual SQLAlchemy dirty comparison under a rejecting UPDATE trigger.

`TestSharedSystemConfigMixedInterleavings` applies a context-local after-Scan observer via a temporary Go overlay and runs a separately compiled child test plus real typed workers. No timing hook is compiled into the product. The current target replay covers isolated-key equal/missing/duplicate/recreated-row cases and later native CAS/cold recovery, not the full-table high-ID counterexample outside the validated actual-producer contract.

### Installed producer reachability

In the 77 fixed installed Python files the sole CustomIdentifiers writer is `remoteidentifiers/__init__.py:149–163`. Its public `get_service` registers the original callback; `get_file_content` always returns a nonempty marker list on success, including empty-URL configuration. It therefore never sync-deletes this key on the tested ordinary path. The native source UI uses `SystemConfigOper.async_set` under its separate shared async lock. Actual reachable mixed cases include old-snapshot hot/DB disagreement and two same-key rows when both writers initially observe absence; both are represented and tested. The higher-row-ID delete/recreate counterexample requires an extra synchronous falsey/delete producer not found in this fixed installed chain. This is a scope boundary, not a claim that the generic DAO counterexample is impossible.

`source/remote_reachable_driver.py` normally imports the full original installed class. Its explicit component harness uses `object.__new__` to avoid unrelated Base/ModuleManager startup, then runs original `init_plugin(onlyonce=false, file_urls="")`, public `get_service`, its original callback, the real default DAO/SQLite, and original WordsMatcher. Six new processes include ordinary/UI, equal-snapshot, absent-insert and cold reads. No callback, query, matcher or DB result is stubbed. The after-SELECT observer only controls interleaving. UI source fixtures call original `async_set` with values normalized according to the fixed endpoint/frontend; the full endpoint cannot presently be normally imported without unrelated scheduler/AI globals, and **original HTTP authentication/event dispatch/Manager/Base startup were not executed**. Target tests use actual authenticated native HTTP and compiled SDK workers.

Run the source driver with the same isolated environment above plus `OWNED_INSTALLED_PLUGINS` pointing at the fixed installed package root and `OWNED_SOURCE_HELPERS` at the recorded official historical helper directory; execute `ordinary|equal|absent OUTPUT` on fresh CONFIG_DIRs, then `cold OUTPUT` in a new process for each. Extra import dependencies are pinned in `source/remote-python-freeze.txt`; helper commit/blob/SHA and raw-output bindings are in `remote-reachable-binding.json`. Original Dockerfile main-resource selection was unpinned; the selected public revision predates the fixed source, not a reconstructed NAS image.

The independent `source_reachable_extra.py` and six bound observations add actual empty-UI/Remote interleavings and UI-created lone-codepoint rules followed by the real original callback and cold reads. Candidate04's internal-Get Unicode rejection was a genuine reachable producer defect, not an approved difference. The corrected chain compares full codepoint lists and each persisted row as well as final titles; it retains separate failing outer-HTTP/UI render checks. Python/JSON decoding combines a valid escaped surrogate pair into its Unicode codepoint; these helpers preserve the JSON string domain, not every arbitrary Python in-memory string construction.

The independent `source_reachable_strip.py` and `source_reachable_ignored_whitespace.py` replay two real UI → original Remote callback → WordsMatcher → cold paths: Python strip separators in combined rule fields, and a 65537-space ignored line before a valid rule. Only source-ignored lines bypass executable-rule accounting; original list positions and native subscription raw-input bounds remain unchanged.
