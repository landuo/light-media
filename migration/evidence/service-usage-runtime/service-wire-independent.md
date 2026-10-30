# Independent wire-v2 review — 2026-10-30 13:40 UTC

## Decision

**The finite service HTTP wire-v2 repair passes independent review.** The previous26 actual source→target differences are closed. This does **not** close entire features38/39: CalendarInterval/AND/OR implementation, final current-main/current-dist runtime and official browser remain separate open gates. No main/product/ledger writes, Go rebuilds or browser access occurred here; all owned processes are terminal.

## Fixed candidate and current integration

- Manifest: `evidence/services-wire-v2-owner/patch-manifest.json`, SHA `9d9e46c8e30b3ae32218e885561af58ce50e81b9e220a71c0936588d9471e8de`.
- Final receipt: SHA `1a67ab8d25480e9f1c3fb5fa458d50e304b44459a80101b51902b5f2246699b1`.
- All11 candidate paths and all6 reused/new executable hashes match the frozen material. Owner normal24/race24 roots, real CLI108 in each mode, frontend580/tsc all pass without skipped roots. The SDK126+schedule5 result is explicitly reused, not newly executed: all1422 dependency files remain identical.
- The13074 complete before/after input maps agree. Current rehash finds one missing generated Go-cache file after cache cleanup; all remaining13073 source/dependency/web inputs are unchanged. The absent cache entry and expected hash are preserved in `final-bindings.json`; it does not imply missing production source or a newly built binary.
- Current main already contains all9 non-shared files exactly. The two shared files must never be replaced wholesale: the main HTTP handler, after removing only its separately integrated usage entry, is byte-identical to candidate's handler; the service pre-mux block remains after existing session/origin guards. Main remote API retains its usage-method adaptation and the exact new service regex. Actual adapter execution on both current candidate/main confirms the service path behavior below. This is a static/source integration check, not a newly built main server acceptance.

## Independent actual execution

1. `run-reused.sh`: existing bound normal app.test + actual owned SDK worker, **7 roots PASS** (source66, query20, routing safety, raw query decoder, actual token, auth/origin and whole-source progress). No compile.
2. `cli_probe.py`: the reviewer's original independent108-case HTTP driver rerun against normal CLI `f1c33f72dc15a715189fd6e913da97821d85beaa7ec6ee7d5f0c630d47842878`; stdin-owned bootstrap, real loopback listener/session/token, process exits0. No browser, proxy interception, production data or external peer. Runtime data is scoped and cleaned normally.
3. A separate Python comparison checks the original immutable reviewer source56 against this fresh CLI output, not the author's comparison results. All56 now agree on status, Content-Type, Allow, exact non-JSON/HEAD bodies and canonical Location path/raw query. Only the generated test token/origin is explicitly normalized. In particular, all20 formerly dropped raw-query cases now return the source200 acknowledgement; all6 normal double-slash responses now have empty307 bodies, no Content-Type, absolute same-origin Location with all trailing slashes removed.
4. Independent comparison of fresh source66 target observations preserves all JSON fields, nulls and arbitrary-precision numbers; only fixture identity/provider and valid non-null execution times normalize. All66 agree. The owner's source142 embeds the reviewer's original56 observations unchanged;115 fixed source bindings and1104 complete imported-file hashes recheck without drift. Its source/auth/lifespan fixture boundaries remain explicit, not a source daemon/physical-loader/browser claim.
5. `generate-query.py` executes actual fixed Starlette1.3.1 `QueryParams(bytes)` / Python3.12.14 for **16383 bounded byte cases**: raw/escaped bytes, every selected UTF-8 lead/tail pair, incomplete/overlong/surrogate/out-of-range sequences, ASCII/non-ASCII mixing, malformed percent, delimiters and repeated keys. The unchanged compiled `TestOriginalServiceWireQueryDecoding` runs from an owned audit CWD against this source-generated oracle and passes in0.03s. No worktree testdata replacement or Go rebuild. This independently checks the newly handwritten UTF-8 replacement logic rather than relying on the author's38 examples alone.
6. `remote-check.mjs`: **48** actual candidate/main adapter cases (two/three trailing slashes across six routes and both versions) preserve v1/v2 and raw query; source-shaped payloads remain raw even with `data/request_id` keys; redirect policy remains `error`. This is actual Axios adapter execution without browser/network, not official UI validation.

## Source-first code findings

- Starlette QueryParams decodes raw ASGI query bytes as Latin-1, then Python `parse_qsl(keep_blank_values=True)`. New parser correctly splits only `&`, splits key/value once, preserves malformed percent escapes, distinguishes present-empty from absent, and takes the last matching decoded name. Literal Latin-1 boundaries and percent-decoded UTF-8 replacement match the actual source cases. It is used only for versioned service `jobid`; token authority/global URL parsing are unchanged.
- Known normal versioned service paths reach their writer after authentication/origin checks but before ServeMux's repeated-slash redirect. Native/unversioned and unrelated routes still use their existing path. No blanket mux replacement was introduced.
- The adapter's service-specific `/*` suffix matcher fixes version loss without altering unrelated APIs. Existing fetch `redirect:error` remains intact.
- Anonymous and invalid-last-token routes remain401; canonical cross-origin and cross-site manual requests remain403. Slash requests generate only a non-executing307; the same metadata on the canonical request is rejected. No execution bypass was demonstrated. Forwarded origin headers do not create an external redirect, and generated authority is absent from response bodies/evidence.
- Native/unversioned responses retain `data/request_id`; real daemon common scheduler row is retained, not removed to satisfy the earlier fixture's empty-list assumption. Native bad-body POST remains the host400 contract.

## Exact integration paths

Already exact on current main (9):

- `internal/app/plugin_module_process_services_test.go`
- `internal/app/plugin_services_wire.go`
- `internal/app/plugin_services_wire_boundaries_test.go`
- `internal/app/plugin_services_wire_test.go`
- `testdata/standard-services/README-wire.md`
- `testdata/standard-services/replay_wire_source.py`
- `testdata/standard-services/wire-boundary-source-oracle.json`
- `testdata/standard-services/wire-query-decoding-source-oracle.json`
- `web/tests/plugin-services-wire.test.mjs`

Manually merged/shared (2): `internal/app/http.go` and `web/src/features/plugin-remote-api.ts`. Preserve the separately integrated market/usage changes; integration-plan/final-bindings record current SHA values rather than assuming equality to the candidate's whole shared files.

## Reuse and remaining scope

All prior red runs/source oracles remain untouched. The original26-error report remains historical evidence; `final-bindings.json` is this candidate's current review. Reuse this finite gate only while the source, parser/router/adapter, tests and their dependency hashes remain unchanged. Any subsequent trigger or shared-handler changes require affected integration regression. Main source merge is present, but candidate binaries still embed their preexisting worktree dist; do not label them current-main runtime or official browser proof. The separate trigger-family39 source observations remain open implementation work and are not certified by this wire review.
