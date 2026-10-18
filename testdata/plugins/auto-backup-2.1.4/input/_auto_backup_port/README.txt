AutoBackup 2.1.4 — frozen all-Go candidate

Scope: complete installed AutoBackup source control flow, original full form and
metadata, API tuple-vs-Response behavior, falsey/reload/onlyonce state, date and
cron definitions, raw category/app.env/cookies/user.db* ZIP semantics, local
change-time retention, WebDAV consumed protocol and quirks, PostgreSQL pg_dump,
version and container/proxy source branches. It is not a formal App installation
or a user-data migration. No production backup/media/downloader/service is used.

The original unredacted source SHA is dedab53b1adf96dc59879ebdd67f77fe67ea773981d4befb04ca000889f607e4.
The original preexisting redacted plugin-source copy was not used as the oracle.
Pinned installed MoviePilot, webdavclient3 3.14.7 and relevant dependencies are in
reference/source-input-manifest.json. Python 3.12.13 ran the actual AST methods or
frozen actual dependency code in an independent fixed-image container with no
network, read-only root/source mount, ordinary UID, no capabilities and only an
owned tmpfs. It never imported or ran the original application/config/database.
Python is test input only; the Go product does not require Python.

All 16 candidate top-level tests passed normal and race without skips/failures;
vet and Linux/amd64 CGO_ENABLED=0 test binary build exited zero. See
verification.json and final-source-snapshot.json. The frozen Linux binary was then actually executed on NAS: 15 passed, one
real-PostgreSQL integration SKIP (missing optional initdb), zero failures/OOM.
The actual Linux SKIP is separate from macOS real-PG normal/race proof.
See nas-verification.json and the preserved nas-tests.log. Its SHA is e520dc74b4fea9749f9c66ec29267eeee81c74a82cb8a300a190ff7011cf9ed3.

199 original-source cases: 82 business/config/log cases, 55 webdavclient3 wire
calls, 9 actual backup_file archive cases, 10 actual SystemHelper mountinfo cases,
11 actual SHOW server_version projection cases and 32 original client-install
branch/proxy/container choices. Stable source logger text and levels are compared;
only the captured exception suffix in failed remote-retention logs is normalized,
because language/OS exception wording differs. Notification content and return
values are compared without this normalization.

Actual PostgreSQL 16.15 owned local cluster dump/restore verified schema, rows,
Unicode/quotes/newlines/bytea, enum, view, function, index, comment and sequences.
The cluster exposed only an owned Unix socket, no TCP, and was stopped/deleted.
The default SQLite path needs no external process. Optional PostgreSQL uses a
host-pinned packaged pg_dump binary with fixed argv; password is environment-only,
never argv/logs. The trusted resolver preserves the original version/proxy/runtime
branch metadata but replaces the original dynamic apt/shell installer. It never
modifies the host or container OS. Bundle a compatible pg_dump in the optional
image; no Python compatibility environment is involved.

Original quirks preserved include negative cleanup count / misleading remaining
count, negative retention partial deletion then IndexError, string "0" remote
retention deleting none, full-URL callback check URN, verification failure after
accepted upload still returning success, invalid remote filename aborting cleanup,
ignored webdav_digest_auth in the installed webdavclient3 version, and API success
returning a tuple while a wrong key returns a Response object.

Formal host must authorize each path/origin/action, encrypt config/private data,
redact logs, and bind occurrence/API identities before calling this package. The
worker forwards only opaque IDs and ignores worker-supplied config. Complete UI
rendering, durable owner/journal/outbox and catalog registration are subsequent
formal integration work. A private history_saved record is not external delivery.
