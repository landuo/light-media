# SiteStatistic 1.9.7

Standalone Go implementation of the installed `SiteStatistic` class. The fixed
1122-line source has SHA-256
`763a88be85185a94082a2f131ed1c80d8306abe24b10ebe9ae95447cfe5da17d`,
from installed snapshot content commit
`270ae0bcbf1d270b916ca9b14f8bffff9b1938d0`. No source application, Python runtime,
production settings, site credentials or site parsers are bundled.

Notification dispatch uses the actual `NotificationType.SiteMessage.value`,
`站点`, from installed/fixed `app/schemas/types.py:313` (SHA256
`025060b9dd29be0fc811cb42ea357e7cd23ce1f450cb704f04f18012ed8be5c1`).
`reference/site-message-enum.json` binds the independent static AST capture.
The first ordinary-main run exposed an incorrect display-label wire value;
its failed run and the source-derived failing-before/fixed-after wire test are
retained. Notification-body-only oracles do not certify this transport enum.

| Source entry/algorithm | Go implementation |
|---|---|
| `init_plugin:63`, state `85`, literal form `116` | `config.go`, `plugin.go`, `form.json`, `defaults.json` |
| `send_msg:217`, body/fingerprint `350–359` | `notification.go`, ordinary `site.refreshed` handler |
| Approximation `322`, selection `362`, formatting `428` | `views.go`, `selection.go`, `characters.go`, `row.go` |
| Card/chart tree `447`, dashboard `937`, page `973` | `views.go`, normal generic Vuetify renderer |
| API declaration `94`, refresh `1100` | `refresh.go`, `host.go`, ordinary `http` method |
| No-op command `90`, service `112`, stop `1095` | `plugin.go`; all return null |

The literal four-field form and three-key defaults match the source AST. Raw
enabled/onlyonce values, missing keys, falsey initialization, unknown modes and
unrelated saved config keys retain their source behavior. `notify_type` is absent
from defaults. `statistic_sites`, written by another source plugin, is preserved
on config update but never read here. Source one-shot is independent of enabled,
mutates input `onlyonce=false` before constructing the scheduler, leaves the
object member true, starts before saving config, and runs after three seconds.
Source stop is literally a no-op: pending jobs survive stop and reinitialization.
The platform still revokes and joins work when retiring the worker/session.
One-shot config saving changes only `onlyonce`, retaining raw nested JSON values,
large integers, duplicate-key first slots and dictionary order across cold init.

History selection retains the original lazy date queries, invocation-local date
cache, active-domain filter, stable upload ordering, last same-name snapshot,
day-1 error retention and day-2 through day-7 fallback. The same production
selection cursor is used by synchronous source-oracle helpers and the worker.
Each missing date gets a fresh admitted SDK callback; the originating page,
dashboard or notification waits for the whole selection pass. Nothing is
persisted or replayed as a continuation. Owner cancellation seals pending work
and cancels/joins an active query. Ordinary source stop does not cancel the pass.

Traffic chart baselines are independent by direction. Notification baselines
retain the different both-zero rule, clamp each negative difference, preserve
floating sort-key collisions and total accumulation, and fingerprint the exact
complete body. Message computation happens before the module notification lock;
the lock covers read/dedup/post/save/log in the original order. Save failure may
therefore permit a later resend. Configuration is read again at every original
notification mode access, including each site branch/format, totals and receipt.
No journal, successful-send receipt, recovery task or background business
executor is introduced.

The pure host algorithm was used as a seed after source-first discovery:
`internal/plugins/statistic/statistic.go`, SHA-256
`b586f27afae970483d40699ad779ebe59c180faeb1a74000fb0c7220a4dbe699`;
the exact reusable source form was
`internal/plugins/builtin/site_statistic_form.json`, SHA-256
`d9c67818e08a82f0a198d04042e1036f9d51c3e811f91208a997e5f102eb32a0`.
Source execution exposed eager date validation, scientific float text and
Python 3.12 compensated-sum differences; these are corrected here. The old
`builtin.site.statistics.*` delegation and native operation/journal architecture
are not used. The raw configuration formatter reuses the frozen TT pure
Python-str/repr parser, input SHA-256
`7d0f187c5501fad2f5a7d678104f7c8dd36a5f12103f6c99cb366bbb27000102`,
with a Site logging entry point rather than a downloader operation.

`reference/source-python312.json` was produced by AST-isolated execution in the
exact installed image
`sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881`,
Python 3.12.13. Its 30 statistics cases and 14 format cases are source results,
not new-Go expectations. `reference/python312-character-classes.json` binds the
same runtime's numeric conversion, date parser, Unicode 15 digit/decimal/space
tables and numeric-metadata exceptions. JSON object field order is retained for
source `to_dict` traversal, including failures in user-name/id fields not shown
as traffic. DB Float history and arbitrary-size current DTO integers remain
distinct. The prior formatter fixture is explicitly a Python 3.14 source capture
of the same str/repr builtins; it is not claimed as a Site network test.

Finite host boundaries and data compatibility:

- `sites.source.account.read` reads stored source names and actual model column
  order. The host owns SQL latest/day selection and legacy/native history.
  Imported row IDs remain imported IDs; native rows map `id` to their stable
  existing host records.rowid. This is a database identity mapping, not the
  original MoviePilot autoincrement sequence. Its value is not displayed,
  sorted, counted as traffic or returned by refresh API; its presence preserves
  the source subtraction dictionary's truth value even when differences are zero.
- `sites.source.indexer` resolves the captured helper lookup to safe metadata
  plus nullable canonical current host identity. A known catalog template without
  current identity cannot authorize credentials or a network request. The
  original domain string is not rewritten or used as a guessed URL.
- `sites.source.refresh` returns this attempt's original 19-field DTO or null,
  including explicit nulls, error DTOs, large integers and raw nested lists.
  Old daily data never substitutes for the current result. The common host
  SiteChain owns collection, persistence, single-site events, unread and ratio
  notices. A source error DTO can produce API success because the DTO is truthy.
- `sites.source.refresh_all` is the common host SiteChain operation, used once
  per private one-shot. Its host owner preserves active helper order, per-site
  stopped checks, per-site effects, duplicate-name slots and the global event.
  This plugin does not copy the all-site loop or resend the chain's notices.
- Canonical identities, credentials, request/result sizes and call revocation
  remain host boundaries. API secrets never enter the worker: the compiled
  required `api_key` query becomes `CredentialMatches["apikey"]`. The source API
  summary/description remain in `get_api`; its callable becomes a method-name
  string. Clones use the host's independent identity/config/data/API namespaces.

Ordinary services and commands remain absent. The source inherits default
dashboard metadata, so `get_dashboard_meta` is deliberately not declared; the
host supplies its normal empty-key card. There is no file state or extra static
frontend. Only instance config and the `last_notify` source dictionary migrate.
The source's APScheduler `print_jobs` writes interpreter diagnostic stdout; IPC
owns stdout here, so that runtime diagnostic is not a business/instance log.
Source private TZ defaults to Shanghai only when unset, trims the settings value,
and rejects invalid/empty zones before scheduling. Notification dates/times use
the process local timezone, as in the source.

Response locale behavior uses the captured complete pure `LocaleHelper`, SHA-256
`3fdc78c7e7478a79f6a472716d7d4fc1b57b5306100d9e5c3cf6c8703117bd69`,
and only its three catalogs' `messages` and ordered `message_patterns` sections.
The embedded catalogs are derived public static text, not plugin functions or
enabled workflow/AI capabilities. Query locale has priority over the two locale
headers and quality/index-ordered Accept-Language. Exact text lookup precedes
full-string template matching, captured-value message translation and the source
Chinese fallback. Chinese `message` remains unchanged; only `message_i18n`
varies. The normal HTTP declaration permits those three headers; existing host
query transport already carries `locale`. No new host RPC, setting or global
language framework is used. Source-executed locale fixtures explicitly identify
their local Python 3.14 interpreter; they do not claim middleware/HTTP acceptance.

Actual host/site HTTP, browser rendering, disabled broadcast versus disabled API,
clone/cold-start/reset/update/uninstall and the final Source Sites native/App
dependency gate are acceptance work owned by the coordinator. The independent
CLI/SDK tests use synthetic resources and are not represented as those results.

2026-11-06 source-checked declaration correction: the manifest omits `get_service`, `get_command`. The independent actual-Python3.12 source inventory distinguishes inherited/explicit `pass` from an implemented `return None`; compatible null Handle branches remain. API/page/auth declarations retain their separate source order. See [interface source and host dependency](../../tests/sourceinterfaces/ORIGIN.md). This new executable requires the finite host optional-lifecycle/discovery-gate/default-dashboard correction; older published host75e13686 rejects its artifact acquisition. SDK98 transport remains unchanged. The coordinator binds the exact new host commit separately; this note does not invent a released minimum version.
