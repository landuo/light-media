# S01 finite qB completed query — standalone backend slice

Source: fixed MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`, complete qB leaf SHA256 `114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0`.

Scope is only `get_completed_torrents(ids,tags)`, not generic downloader RPC or all download methods. Existing Transmission acceptance and the 28 source completed oracle cases are inherited, not rerun. Existing `downloader.tasks.read` stays exact-selection only.

The standard SDK capability `downloader.qbittorrent.completed.query` accepts a configured enabled downloader ID and bounded JSON None/string/list selectors. IDs list members use existing source-shaped `str` with original JSON ordering; no hash normalization. Tags perform the complete source pass before completion predicates. Caught query/tag failures map to source None; successful empty result is a nonnil array. Completion-predicate source errors remain errors.

The new finite native adapter reuses the old guarded session/transport without modifying credentials, TLS, timeouts, retries, redirects or mutation behavior. Current 20s total timeout and authentication policies differ from source SDK and remain explicit adaptations. qB independent third-party TLS behavior is NOT covered by the approved RequestUtils categories or OCR choice. Keep verification.

No original Python or native daemon is executed by this candidate. Owned HTTP fixtures are protocol evidence only. The inherited AF_NETLINK restriction is not retried. The standalone candidate is exactly baseline `b4f3251ee4f5359659a66935a427502da20f59e1` plus thirteen new files and two narrow shared-dispatcher hooks; unrelated lifecycle, torrent, OCR, clone and trigger edits are excluded.

## Finite verification on 2026-10-31

- Fresh standalone candidate: seven normal roots (SDK two, qB host four, adjacent Transmission permission one), then four qB host race roots using freshly built race SDK workers. The fixed source28, pure value roots and unchanged native adapter evidence are inherited through the independently compared 189-path dependency set, not misreported as rerun.
- Two real normal server processes and two ordinary compiled SDK workers used the same new owned database: authenticated installation/configuration, unknown-hash completed query, an owned HTTP 503 exposed as HTTP 502, recovery, a new cold host/worker, persisted selection and 401 boundaries. Both hosts exited normally and workers were naturally reaped; the cold run did not fetch the artifact again.
- The first compilation run reached the reserved disk floor after all seven builds succeeded and before any test root ran. That failed run is retained. The next normal run reused the exact successful programs after source/binary readback instead of recompiling or changing the failed evidence.
- The controller used only the specifically authorized `http://127.0.0.1:39123` API origin, fixed method/path allowlist, no redirects and no transport fallback. Product proxy, TLS and guard settings were not changed. The race build used an explicitly approved isolated temporary Go cache; generated input bytes were captured.

This is backend capability evidence, not browser/UI, a real qB daemon, source full-runtime equivalence, media/download/NAS, the entire S01 category, CI, or remote delivery. Fixture UI methods are not browser acceptance. Detailed source/input hashes, raw results, failure history and independent reviews are retained outside worktrees; only the curated safe textual subset belongs in a release. No database, credential, downloaded artifact or binary belongs in that subset.

## Reproduction entrypoints

Product runtime is Go only. The saved Python controller is an isolated development/test harness, not a server dependency. The curated evidence lives in `migration/evidence/s01-qb-completed-minimal-20261031/`; saved commands/plans retain their original cloud paths and are execution records, not portable commands to run unchanged.

From this repository on Linux with the Go version required by `go.mod`, build both qB fixture variants before App tests. Use new temporary outputs, never an existing data directory:

```sh
OUT=$(mktemp -d)
go build -o "$OUT/qb-allowed" ./cmd/owned-qbittorrent-completed-fixture
go build -ldflags='-X main.fixtureDenied=true' -o "$OUT/qb-denied" ./cmd/owned-qbittorrent-completed-fixture
export OWNED_QBITTORRENT_COMPLETED_FIXTURE_BINARY="$OUT/qb-allowed"
export OWNED_QBITTORRENT_COMPLETED_DENIED_FIXTURE_BINARY="$OUT/qb-denied"
go test ./internal/plugins -count=1 -run '^TestQBittorrentCompletedSDK'
go test ./internal/app -count=1 -run '^TestSharedQBittorrentCompleted'
```

For App race verification, rebuild **both** fixture binaries with `go build -race` into separate paths, point both variables there, then run `go test -race` with the four exact App roots listed in the saved `race-plan.json`. The saved normal plan also includes one adjacent Transmission permission regression and its two separately built fixtures. The real-CLI record uses a newly bootstrapped owned database and only the fixed local controller origin; do not repoint its synthetic qB endpoint or reuse production credentials/data. The portable commands above are documented equivalents, not an additional claimed execution.
