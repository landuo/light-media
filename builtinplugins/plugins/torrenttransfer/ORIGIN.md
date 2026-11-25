# TorrentTransfer 1.10.3

This package is a standalone Go implementation of the installed MoviePilot
`TorrentTransfer` class, source SHA-256
`e5f513a57cf42ee8778744ba30cf669f93a5a3e6c7589ca0b2e3b9c483651bd5`.
The installed `TorrentTransfermusic` differs only in class/presentation identity
and `is_clone`; the host supplies separate configuration, data, files and service
namespaces to this same executable. No production source configuration is bundled.

The source configuration form and defaults are literal AST values. Its two
option lists are populated from the finite ordered
`downloader.configurations.read` projection (public references only, no sites or
credentials). Live DownloaderHelper/ServiceBaseHelper were captured and
byte-matched against the inspected fixed reference (SHA-256
`932b1718c3a47f8fc848036f4fe38ba59793475c97d24f7e2065bda0310782f5` /
`fa1c4bd99c5196bb2d8170a0fd942f4386e12c5f7e230e43cb0778c22193d6b9`).
Configuration remains name based. The form retains each eligible name's first
insertion slot. Service metadata uses the last eligible global configuration;
runtime identity uses the last eligible configuration of its own module kind,
with actual QB module iteration before TR. Inactive QB does not fall back to TR.
Runtime existence/inactivity comes from the host's shared source-wrapper
constructor facts, rather than a new connection probe on each plugin call. A
previously connected wrapper remains active when a later task query fails, as in
the source; plugin initialization and clones do not rebuild that shared cache.
Metadata Type and RuntimeKind remain separate, including the original mismatches.
A metadata-TR/runtime-QB add passes its labels as finite repeated QB `labels`
fields, then fails at the original tuple.hashString access; it does not invent
successful history, recheck, source deletion or a completion notification.


`config.go` is shared by product initialization and source-oracle tests. `core.go`
preserves selection order, raw prefix mapping, ALL include/ANY exclude tags,
untagged bypass, backup-before-duplicate lookup, source-only task deletion,
paused adds, qB tuple truthiness, source history shape/upsert and in-memory pending
queue behavior. Running jobs read current object configuration after reinitialization.
There is no whole-transfer lock, verification-before-source-delete policy,
transaction journal, history-based recovery, added API, page, command, or Python
runtime. The checker deliberately retains the original raised-error gate behavior.

Public cron is declared through standard `get_service`. The source's private
scheduler uses admitted SDK callbacks with fresh HostCallers. Each timer tick
reserves its successor before dispatching jobs; the source anchor, once-consumed
flag and per-job instance state remain on the same object. `stop_service` seals
new timer admission, joins already reserved/active private work, and preserves the
queue and checker flag. Public cron is removed by the host.

Transfer uses the original in-memory loop locals as a cursor. Each candidate
selection and each selected torrent receives a fresh admitted callback, including
filtered candidates and failure branches. The original `transfer` callback waits
for the entire pass, summary or raised failure; public running/manual completion
therefore covers all continuations. There is no persistent cursor, recovery,
workflow or new executor. The synchronous source-oracle runner uses exactly these
same preparation/selection/transfer/summary functions. Recheck retains the original
single bulk query/start (fixed RPC count), including false/empty/native-error
semantics. Existing SDK limits remain unchanged: default 64/max 256 host calls per
phase and the existing single-request size limits. Business logs use LoggerRPC;
putting them in cumulative diagnostic stderr would incorrectly retire a long-lived
worker after its diagnostic byte limit.

Pure Go path/bencode helpers and original-source fixture captures were reused
from the existing host package after checking them against the installed source.
The complete `cron.go` calendar implementation is vendored from
`landuo/light-media@75e13686a0e3df05ac251e359303ef3fd5971cf6`,
`internal/plugins/schedule/cron.go`, SHA-256
`43d48819383fadd42e45743519095832b5ca3d579ba4c80ae8ae0ba5a7eddb3f`.
The package declaration and crontab whitespace lexer changed: tokenization uses
the captured Python whitespace predicate, including U+001C through U+001F.
Cron execution is still the host scheduler's
responsibility; the worker validates source cron construction on the getter.

Recorded platform adaptations and outstanding integration boundaries:

- Canonical downloader IDs/hashes, bounded typed IPC, canonical credentials and
  current-call revocation remain host boundaries. SourceAdd has a finite
  `skip_checking_wire` text field; the plugin reproduces the installed vendor and
  Requests multipart text conversion, preserving null omission, boolean case,
  raw strings, Python numeric/container repr and dictionary input order. Invalid native values cannot
  broaden task selection. Revocation, cancellation and explicit post-mutation or
  partial-failure fences end the chain. A plain unknown mutation receipt retains
  its fact and follows the original wrapper result without repeating the selected
  write: QB continues marker lookup, TR counts a failed add, and ignored control
  failures remain ignored in their original positions. The original vendor's
  automatic write retry is excluded by the current host mutation boundary.
- Repaired torrent material is retained at the host-resolved instance data path
  `tmp/<hash>.torrent`, instead of the source's shared global TEMP_PATH. Source
  torrent/fastresume files remain unchanged; different clones cannot overwrite
  each other's repaired material. There is no automatic payload-file deletion.
- Public cron follows the captured tzlocal process-local timezone (explicit
  raw TZ, distribution files and localtime); it is not forced to Shanghai.
  Anonymous valid TZif data is declared with `local_timezone:true` and no
  `timezone` string. This requires the coordinator's finite source-local host
  trigger support, including empty-TZ system-file handling. No timezone bytes
  or paths cross the SDK. Dependency warnings use the existing scoped LoggerRPC
  once per worker resolution, rather than Python process-global warning stderr;
  this diagnostic channel/lifetime difference follows process isolation. Invalid data/configuration remains an error. Warnings
  and successful resolution are cached like tzlocal. Private scheduler
  construction separately validates stripped Settings.TZ before clearing a
  one-shot flag; its default is Shanghai only when TZ is absent. Elapsed private
  date/interval anchors remain unchanged.
- The original C bencode extension has native crashes/undefined results on some
  malformed inputs. Go returns a parsing error, with finite depth/transport bounds.
  Valid observed byte/string/integer/duplicate-key behavior is retained.
- This artifact requires the finite `downloader.source.connection`,
  `downloader.source.find_by_tag`, `downloader.source.tasks`,
  `downloader.source.completed`, `downloader.source.add`, and
  `downloader.source.control` capabilities, finite SourceAdd wire/QBLabels fields, and explicit
  source-local cron trigger support and ordered configurations added after the
  original host SDK snapshot. It cannot run unchanged on host 75e13686. Their explicit native-error
  flag restores ordinary wrapper empty/false results; opaque host/guard errors,
  and post-mutation guard failures remain terminating. Source connection and
  marker lookup preserve the original cached wrapper/auth shape independently
  of the ordinary fresh-probe downloader interfaces.
  In particular, an ordinary query error clears the recheck queue, qB add false
  still performs marker lookup, and TR add false is a failed transfer. The
  coordinator records the exact new host commit and actual integration evidence.


Actual installed Python is 3.12.13. Its pathlib `exists` ignores only ENOENT,
ENOTDIR, EBADF and ELOOP; permission/I/O errors propagate, while non-encodable/NUL
paths are false. The Go adapter follows that captured implementation. Random
marker sampling retains the actual helper alphabet (G/g repeated, no J/j);
cryptographic entropy replaces Python's nondeterministic PRNG without changing
its index distribution.

Evidence in this package is deliberately scoped:

- `reference/source-oracle.json` is a pre-existing capture of the exact original
  class AST with synthetic file/downloader/scheduler/DB dependencies: 57
  scenarios plus 8 path cases. SHA-256
  `b77b1927fc5050a7de0fc7122c72534c70e190e1f947a1a7ede58455d16df2e0`.
  `source_oracle_test.go` compares Go calls/logs/state/history against this source
  capture; only dependency exception text is finitely normalized. It is not a
  real original host, real downloader or production timing test.
- `reference/bencode-oracle.json` captures actual fast-bencode 1.1.8 execution
  for 25 isolated inputs. SHA-256
  `cc57e9733870eb51f785511aea3e693a100ae8c74fd637fbbca059f1f84e0154`.
  Its crash/undefined cases are rejection adaptations, not fabricated round trips.
- `reference/vendor-skip-wire.json` is the independent 33-case observation of
  the actual installed qbittorrent-api 2026.6.0 AST and byte-matched Requests
  2.34.2/urllib3 2.7.0 serialization. It has no network/daemon execution. The
  ordered JSON/Python repr formatter is tested directly against these outputs.
- `reference/timezone-source-oracle.json` covers 19 source-first observations
  from actual installed APScheduler 3.11.3/tzlocal 5.4.4/Settings AST: absent,
  empty, named, colon and invalid environments; conflicts/aliases; warnings;
  Unicode/invalid UTF-8 clock files; anonymous and malformed TZif. The custom
  local marker preserves data through the finite host source-local declaration.
- `reference/module-service-order-oracle.json` contains six actual-source
  ordered configuration/module cases. `metadata-runtime-add-oracle.json` records
  original wrapper/vendor POST-to-owned-peer traces for the mismatched return
  shape; it is not a real daemon acceptance report.
- The SDK fixture tests exercise all four downloader combinations, flags, exact
  source data keys, source-only deletion, queue errors, repair files, defaults,
  lifecycle and overlap with synthetic HostCaller resources. The command test
  starts the actual executable/concurrent SDK protocol, waits the real one-shot
  delay and checks fresh callback scope and stop/join. These do not replace
  Linux App/browser/real qBittorrent and Transmission acceptance. Separate
  real SDK worker tests retain the original 64-call default while transferring
  120 synthetic torrents, filtering 400 candidates, bulk-checking 2,000 pending
  hashes, advancing 70 private ticks with a test-only clock, and stopping or
  reinitializing queued/active continuations. Parent/child/RPC timing observations
  are saved under ignored migration work; they use synthetic host services and
  do not replace the coordinator's real Linux daemon acceptance.

Run `go test -count=1 -race ./plugins/torrenttransfer ./cmd/torrent-transfer` from the
repository worktree. Command-test binaries are created only in ignored `work/`
and removed by the test. The migration coordinator owns the authoritative ledger,
registry, host compatibility extension and real-service acceptance results.

2026-11-06 source-checked declaration correction: the manifest omits `get_command`, `get_dashboard`, `get_dashboard_meta`, `get_module`. The independent actual-Python3.12 source inventory distinguishes inherited/explicit `pass` from an implemented `return None`; compatible null Handle branches remain. API/page/auth declarations retain their separate source order. See [interface source and host dependency](../../tests/sourceinterfaces/ORIGIN.md). This new executable requires the finite host optional-lifecycle/discovery-gate/default-dashboard correction; older published host75e13686 rejects its artifact acquisition. SDK98 transport remains unchanged. The coordinator binds the exact new host commit separately; this note does not invent a released minimum version.

Subsequent source-auth correction: this family also omits inherited-pass `get_auth_providers`, after independently executing the source state-first/check_method auth registry. This supplements the initial20 omission note; compatible null Handle branches remain. See `tests/sourceinterfaces/ORIGIN.md` and the current23 source-interface revalidation evidence.
