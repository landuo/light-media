# S01 qB completed TLS — ordinary CLI preparation 01

**Static candidate only. No execution / build / SSL loading has occurred in this preparation.** The driver initially names HEAD `808148abfad0b7fe230c426387a3b7b49eeedb18`. S13 may publish before this gate: the coordinator must explicitly rebind the final HEAD, clean compile view and real server/worker programs before any execution. Missing `execution-bindings.json` intentionally blocks the driver; placeholder program hashes are not supplied.

## Exact affected chain

Normal server bootstrap/start → installed unchanged `OwnedQBittorrentCompleted` standard SDK worker → existing App scope/config guard → qB-only `NewQBittorrentCompletedClient` → private transport clone, same login and completed-info query. Uses the approved qB certificate-verification exception, not a new general TLS permission. Transmission, general NewIYUUClient, qB command client, proxy/environment/CA/redirect rules remain unchanged.

The original two S01 completed-query operations and prior UI/raw-field/selector behavior are inherited with the parent matrix. This gate tests the one newly affected qB HTTPS path, not a real downloader daemon or a new full raw-field matrix.

## Fixed routes and processes

- Controller: only `http://127.0.0.1:39123`, with seven exact method/path pairs in `ROUTES`. Verify actual held server PID/starttime/executable and owned listener before each request. Never forward redirects. Login bodies/headers and credentials are not evidence.
- Peer: IPv4 `127.0.0.1` dynamic owned HTTPS port only. GET `/artifact/index`, `/artifact/worker.zip`; POST `/api/v2/auth/login`, `/api/v2/torrents/info`. Fixed synthetic replies, owned worker artifact; no upstream, generic URL handling, proxy forwarding or CONNECT implementation. Any unexpected request fails the gate.
- Go public testcert constants are the same previously approved fixture. Certificate and public test-only key are materialized only under fresh `private-data/owned-test-tls`, mode 0600; no key content is exported. No system CA or SSL settings change.
- Inherited environment is copied unchanged, including proxies. Existing exact `NO_PROXY` for 127.0.0.1 is required; absence fails, without rewrite/fallback. The controller uses its existing narrowly authorized fixed local client, not a generic direct-fallback client.
- Exact fresh data directory, two sequential normal hosts, four business queries. Success → one info HTTP 503 / API 502 → same worker recovery → same DB, new host/worker cold query. Four authenticated observe-only calls bind PID/parent/starttime/installed binary SHA but cause no qB request. Two unauthenticated calls must be 401 without qB request. Final peer totals: one index + one artifact + four login + four info. No refetch or query replay.
- Graceful host Wait(0), observed worker disappearance and only identity-matched owned cleanup. Failure remains failure, including forced cleanup.

## Intentional inherited test isolation

Bootstrap disables the old public plugin statistic center and empties PluginServerURL/markets/local repo roots. This is inherited owned-fixture isolation, **not** a claim all production settings are unchanged and not a TLS grant for B/statistics. No Meta/report field is modified; the fixed completed API never calls recognition/report. The already delivered A installation client merely installs the owned HTTPS artifact and is not newly accepted by this S01 gate.

## Binding / limitations

Final runtime inputs must include actual normal-server and unchanged-worker binaries, their complete compiler input closures and generated/mapping metadata, reviewed clean-view proof, this driver and preparation files, candidate02, old source/current protected mappings, and public testcert provenance. Before/terminal checks enforce all immutable bytes; ledger is not an immutable build input. Current executable paths are planned only: `runtime/s01-qb-query-tls-server01`, `runtime/s01-qb-query-tls-worker01`.

Native01 original whole-root FAIL remains. Candidate02 test-only lazy-initialization baseline correction and native02 normal/race are separately reviewed; they are not retroactive native01 success. This preparation is not execution READY and author cannot accept their own implementation.
