# S11 ordinary CLI — self-plugin path only, prepared / not executed

Fixed new ordinary server + installed `OwnedMediaRecognition` worker, two normal
hosts using one new private data directory. No safe/dev mode, native testlauncher
or alternative controller. Source entry/router hooks must be integrated and
independently accepted first; merely preparing this driver is not readiness.

## Four fixed business calls

1. Three `module_hit` operations: cache omitted, false, null. Complete synthetic
   66-field model and existing live descriptors must return; raw large integer
   is checked without float conversion. Module trace must expose true/false/None
   respectively. This checks forwarding/defaults, **not native cache/freshness**.
2. `module_false` returns source None through the original falsey terminal path.
   This is not a null module return and does not fall through to native.
3. `unsupported_type`, `pre_canceled`, then `module_hit` with explicit continue.
   Real worker error is `host_operation_failed` (existing wire sanitization), not
   the test-only fake HostCaller's original SOURCE_* error. Pre-cancel has zero
   callbacks; same-worker explicit recovery succeeds. This is not in-flight
   cancellation evidence.
4. Stop first normal host naturally, start a new one over the same private DB,
   no installation/refetch, then one `module_hit`. New PID, init_count=1,
   invocation=1 and new module trace are required.

Expected totals: 4 business POSTs, 8 typed attempts (6 successful values, 2
errors), 7 actual SDK HostCaller callbacks (6 envelopes, 1 host error), 6 actual
self module handlers (5 models + 1 false). Registration GET405 is readiness,
not a business request. Current ordinary API does not measure physical pipe/frame
counts; the result explicitly does not claim those.

The original full response is retained. Expected model fields are the actual
owned fixture constant, not a source/native oracle. Existing live descriptors
come from `originalFilterSourceMediaJSON` / `rsscontext.MediaInfo.ToDict`; the
independent source/target algorithm evidence remains separate.

## Exact safety and process boundary

* Controller remains **http://127.0.0.1:39123** with only health/login/market-read/
  install and this registered plugin route. It verifies held child executable,
  PID/starttime and its exact IPv4 listener before each call. No socket scans,
  IPv6, netlink, arbitrary PID probes, alternative ports or route retries.
* One own artifact peer only: GET `/index` and `/worker`, exact Host, no auth,
  cookies, body, forwarding, redirects or CONNECT. Every POST/PUT/DELETE/HEAD/
  OPTIONS/PATCH/TRACE is refused. It has no provider/downloader endpoint.
* The driver contains neither the worker's `native` operation nor ID 900045.
  Only IDs 900043 and 900044 reach module handlers; their returned model/false
  stops core dispatch. No real or synthetic TMDB key is installed: both TMDB
  credential fields are empty; downloader/site/root lists are empty.
* New owned login/API secrets are generated in memory and passed through stdin;
  login responses/headers are not saved. Private instance DB/logs are not public
  evidence. Public structured observations contain no supplied credentials.
* Environment, proxies, CA and startup modes are inherited unchanged. Conflicts
  cause refusal, not environment clearing. The preflight grant marker is to be
  supplied only by the coordinator after the execution window is approved.
* Same existing owned-process stop/identity/cleanup logic: only held hosts and
  previously bound direct-child workers. Forced cleanup makes the gate FAIL.
* No arbitrary file reading, real media/downloader/NAS or UI operations.

## Frozen preparation versus future execution

`normal-cli-driver.py` is a complete draft; it has only been parsed with stdlib
`ast.parse`, never imported or run. `inherited-helpers.json` proves 15 unchanged
function ASTs and 10 unchanged peer helper ASTs from the actual previous TR
normal driver. `start` changes only the owned login username; preflight binds
the new exact programs. The TR RPC handler and credentials were removed.

`prepared-inputs.json` records observed source/audit hashes, not a compiled
closure or runtime PASS. Server/worker binary SHA values are deliberately null.
The coordinator must build the exact normal server and worker, record actual
program/compile-input receipts, and create `execution-bindings.json` with status
`COORDINATOR_FROZEN_READY_FOR_REVIEWED_EXECUTION`, actual hashes and all inputs.
The script refuses absent/inconsistent bindings or pre-existing output paths.
No placeholder can be treated as a binary hash or permission to execute.

Expected program paths: `runtime/s11-id-server01`, `runtime/s11-id-fixture01`.
Expected fresh output: `test-instances/s11-id-normal01`. Another reviewer must
review this author's driver before coordinator execution. Full native TMDB,
fresh-cache and remaining S11 inputs remain outside this ordinary CLI gate.
