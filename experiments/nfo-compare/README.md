# Offline synthetic NFO field comparator

This local-only CLI compares the actual XML fixtures with a saved, sanitized `observe` JSON result. It never contacts fnOS, changes fixtures, renames files or overwrites reports. A successful API read is not a successful comparison.

```sh
go run ./experiments/nfo-compare \
  --experiment-id <the-same-synthetic-UUID> \
  --fixture-dir work/<fixture-directory> \
  --observation work/<observe-result>.json
```

Exit 0 and `status=passed` mean only `contract=synthetic_nfo_fields_v1` passed. Provider IDs and image bytes remain outside this comparator and `provider_ids_verified` is always false. A field mismatch exits 1 while preserving `api_read_success=true` if the read itself succeeded. Output exposes fixed field names, booleans, expected/actual season/episode numbers and constrained dates; it does not dump metadata or response bodies.

The comparator reads five namespace-matching NFO documents and expects seven unique observed nodes. It checks Movie/TV/Episode type and exact XML title, real ancestor and parent relationships, root explicit empty parents, expected seasons/episodes, `year` as the observed fnOS `YYYY-01-01` release-date representation, episode aired/release dates, and actor names/roles/relative ordering. Actor `order=0/1` → `1/2` is accepted, but reversing/duplicating/missing orders is not. The known fnOS TV-root empty cast semantics are checked separately; Season and Episode cast are compared. Custom synthetic source IDs must not silently become nonempty real source IDs.

Only `lightmedia-m0` IDs in the explicit UUID namespace are accepted. This tool does not validate or manufacture real TMDB/TVDB/IMDb identities. Input file sizes, directory entries, XML count and actor count are bounded; symlinks are refused.

A same-name NFO may use a clean video basename such as `Synthetic Series S01E02.nfo` within the UUID-namespaced work directory. The metadata expectation is read from its XML and cannot be changed by renaming the file. The proposed filename experiment should change only the synthetic video and its matching NFO basename; preserve the NFO season=1/episode=2, metadata title, actors and isolated directory. Re-observe and compare; do not apply a manual rescrape episode override to hide the mismatch. Whether a UUID containing `e01` affects the server filename parser remains a hypothesis, not a demonstrated root cause.

The observed regression was saved separately in `work/nfo-distinct-compare-before-rename.json`: API reading succeeded, but ordinary-series episode expected 2 and actual 1 failed. The original report was not altered.

Validation: `go test -race ./experiments/nfo-compare`, `go vet ./experiments/nfo-compare`. Tests cover the E02→E01 regression, wrong title/year/role/order/source/ownership, missing order, normalized actor ordering and clean-basename XML parsing.

Both explicit publishing layouts are supported: historical UUID-prefixed Movie/Series/Animation directories, or canonical `合成电影 (2024)`, `合成普通剧/Season 1`, and `合成国漫/Season 2`. The loader detects one complete set; mixed layouts, additional root directories and other nested directories are refused. XML titles/actors/custom IDs must remain UUID-namespaced. Neither layout nor a basename rename changes the XML season/episode expectation: an observed E01 still fails against S01E02 in canonical layout. Canonical publishing has only synthetic local test coverage until a separate new isolated live experiment is performed.
