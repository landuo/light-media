# S11 cache/response source01 — finite actual ACCEPT

The coordinator executed the frozen collector once (`c1fed5`, exit 0), 2026-11-01 01:26:24.034330–01:26:24.460008 UTC, with the exact pinned Python `-I -S -B` argv. Raw stdout is 80,668 bytes, SHA-256 `9be629e95537437fd8f60dc3dcf5b092af95f64e9f4fe8b90d0d8d386b825d80`; stderr empty. All 16 inputs and 9 preparation entries still match. The actual output contains the same 32 complete source nodes and 10 fixed input scenarios as the preexec review.

## Actual observations

23 request actions produced **15 returns and 8 recorded uncaught exceptions**, plus one fresh-context state sequence. This is not 23 successful requests. The recording dependencies saw 19 GET reads, 11 cache sets, 20 cache gets and two owned-session rebuilds.

- Same-day repetition hits the snapshot; the original deep-copy return isolates nested caller mutation. Forced fresh replaces the entry and the next ordinary call sees the replacement.
- A fresh read exception leaves the prior cached snapshot usable. Ordinary read exceptions and malformed/scalar JSON are not cached.
- A structurally valid TMDB error JSON is cached **before** business-error interpretation. Both calls raise the same TMDbException with only one read.
- GET None rebuilds and retries once only for the owned session: read → DEBUG → recording close → recording init → second read. Two None results then raise; an external session performs no rebuild/retry.
- Original signature binding excludes self and includes ordered method/URL/data/JSON/kwargs with local-day `_ts`; the supplied next day changes the recording key. This does not establish cachetools exact encoding.
- Outer fresh survives a false inner request scope; nested/exception exits restore `[false,true,true,true,false,false,false]`.

## Limits and downstream use

The original decorator/request/snapshot/error/context logic actually ran, but Cache/hashkey, response/read/session-init and calendar remain explicit recording inputs. No real cachetools backend, TTL/LRU, Redis, precise key token, cross-instance cache, concurrency, HTTP, requests Session, rate-limit wait, POST, provider, Go or full App was exercised. Retain the complete raw trace for target comparison; any normalization must label these ports and must not turn the eight source exceptions into successful returns. The initial command receipt's RUNNING marker is historical; result.json and exit.txt establish the terminal state. Reviewer only read and hashed artifacts; no rerun.
