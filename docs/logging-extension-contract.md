# M0 logging and extension contract v0.1

Status: M0 logging prototype plus **design-only** P0/P1 extension boundary. This document does not mean a plugin runtime, download mutation, relocation, torrent verification, or library refresh was implemented or tested. It follows design-v1.4 and uses only standard-library Go for the implemented logging package.

## Implemented logging sink

`internal/logging.New(writer, Options)` returns a standard `slog.Handler`. The caller owns the writer; the package does not open user directories, configure global logging, create background workers, or capture HTTP traffic. A production logger should write only its own JSONL file opened with restricted permissions, not a service's credentials file or stderr of an external program. M0 tests use memory and synthetic failures.

Every record contains UTC time, level and sanitized message. Permitted string attributes are `module`, `event`, `status`, `error_code`, `request_id`, `workflow_id`, `job_id`, `instance_id`, `site_id`, `capability`. They must match `[A-Za-z0-9_.:-]{1,96}`. `duration_ms`, `count`, `http_status`, `attempt` allow bounded nonnegative integers; `retryable` allows a boolean. Unknown keys, arbitrary objects, headers, request/response bodies, errors as objects, arrays and groups are omitted. Unknown `LogValuer` objects are not evaluated. This avoids accidentally serializing a client, credential container, or complete HTTP response.

`WithIDs(ctx, IDs{RequestID, WorkflowID, JobID})` carries opaque stable identifiers. Context IDs override same-named record attributes. A request ID identifies an interaction, workflow ID persists for the user-visible workflow, and job ID remains stable across execution attempts. Retrying increments `attempt`; it does not replace workflow/job identity. Values must come from application-generated identifiers, never URLs, tokens, file paths or titles. IDs provide correlation, not authorization.

Messages are for short developer-authored descriptions and error codes. They must not be raw response bodies or serialized credentials. Defense in depth:

- Register known credential values in `Options.Secrets` in memory; exact values, query-encoded and path-encoded forms are replaced, longer overlapping values first. At most 64 configured secrets, each at most 4096 bytes; never log the options.
- Recognizable password/API-key/token/passkey/secret assignments, Cookie/Authorization headers, Bearer values and complete HTTP(S) URLs are redacted. URLs are wholly omitted, including userinfo/path/query, rather than selectively removing a few known query names.
- Over-8192-byte input messages are replaced before scanning; messages still exceeding 512 bytes after redaction are omitted, not cut midway through a secret. Control characters are removed or changed to spaces; JSON encoding and a single trailing newline prevent a forged second JSONL record.
- Unknown, unlabelled secrets cannot be inferred by regex. Register them and never send response bodies to this sink. This is a defense-in-depth logger, not a guarantee that arbitrary hostile text is safe to publish.

Each JSONL record is capped at 4096 bytes. The sink shares a mutex across derived handlers, preserves line writes and has a total attempted-byte budget (default 1 MiB, configurable). Exhaustion drops the record and returns `ErrBudget`. No retries or unbounded queue. Write failures and short writes are returned; attempted bytes still consume budget. `Stats()` exposes written/failed/dropped counts and attempted bytes so the owner can report logging failure outside this failing sink. `slog.Logger` discards handler errors by standard behavior; a production owner must monitor Stats or call Handle when it needs synchronous error handling. The budget is a stop-on-limit prototype, **not log rotation**. Partial writes may leave an incomplete last line, and nothing claims fsync/crash durability or recovery from a stalled writer. The owner is responsible for local writer latency.

Synthetic tests verify correlation/override, cookie/API-key/password/Bearer/URL/known-secret redaction, quoted assignments, newline safety, field rejection, unknown LogValuer behavior, concurrent lines under `-race`, level filtering, oversized text, byte-budget exhaustion and observable writer failure.

## Extension event envelope draft

No event publisher, event persistence table, consumer cursor, plugin process, or command executor is implemented here. Proposed v0.1 envelope:

```json
{
  "schema_version": "0.1",
  "event_id": "event-opaque-id",
  "event_type": "import.completed",
  "occurred_at": "2026-10-14T00:00:00Z",
  "workflow_id": "workflow-opaque-id",
  "job_id": "job-opaque-id",
  "subject": {"kind": "asset", "id": "asset-opaque-id"},
  "origin": {"kind": "core", "plugin_id": null},
  "causation_id": null,
  "operation_id": "operation-opaque-id"
}
```

Candidate event types: `download.completed`, `import.completed`, `scrape.completed`, `library.visible`, `job.failed`. Typed subject kinds: download, asset, scrape_job, library_item, job. Events carry references and facts after the matching state transaction commits, not credentials, torrent bytes, raw paths, private tracker URLs, arbitrary serialized HTTP bodies or free-form plugin context. Version 0.1 has no generic payload field; event-specific fields require a separately specified versioned schema. `download.completed` requires the intended selected files complete; stopped, metadata-known and seeding are separate observations. `library.visible` requires read-back evidence, not merely a successful refresh request.

Future publication should use a transactional outbox with at-least-once delivery and durable per-plugin cursors. Consumers deduplicate by `(plugin_id,event_id)` and commands by `(plugin_id,operation_id)`. `causation_id` records the initiating event; origin prevents self-trigger loops. Recovery after restart rechecks external state before retrying ambiguous writes. Schema evolution is explicit: reject unsupported major schema/API versions and unknown mutating commands; missing/unknown fields cannot expand authority. None of these durability claims has been tested in M0.

## Capability contract draft

Capability declarations have separate facts: `protocol_known`, `implemented`, `read_observed`, `write_verified`, permitted instances/directories, and observed version. A protocol method documented upstream is not proven for a particular account, version, permission set or filesystem. Every plugin operation needs a host-issued grant scoped to explicit resources; possessing an ID or observing an event grants nothing.

| Capability | Meaning | M0 evidence / future constraint |
|---|---|---|
| Downloader.Probe / List / Files | Read authorized instance and stable `(instance_id,hash)` identity | Separate integration probe only; synthetic tests and later explicitly labeled live probes. Numeric session IDs are not durable identity. |
| Site.Check / Search | Check a named site, bounded search | Candidate probes only; actual domain identity and individual template support require validation. |
| Material.ResolveRef | Read protected metainfo through an opaque handle | Unimplemented. Material is credential-grade; cannot reconstruct private .torrent from hash/name. |
| Downloader.AddStopped | Add into a verified stopped state | No write tested. Must reconcile timeouts and duplicate stable hash before any retry. |
| Downloader.Verify | Trigger then observe full data verification | No write tested. Not a passive read; requires separate grant. |
| Downloader.RelocateExisting | Repoint to existing data without moving bytes | No write tested. Unsupported if backend lacks this precise semantic. |
| Downloader.MoveData | Actually move source data | No write tested; independent explicit plan, reference checks and authorization. |
| Downloader.RemoveTaskKeepData | Remove task while retaining files | No write tested. Never silently translated into remove-with-data. |
| Library.Refresh / Visible | Request scan, then independently verify result | No refresh write tested; read visibility does not prove NFO import or refresh behavior. |

The future plugin manifest identifies plugin/version/host-API range and requested capabilities. Runtime command envelopes include schema version, plugin ID, operation ID, scoped subject and typed arguments. The host—not a plugin—checks ownership, capability grants, global automation pause, referenced-file protection and exact operation semantics. Default rejection applies; no arbitrary RPC passthrough, SQL, shell, direct file deletion, secret export, or generic `execute`. P0 reserves these semantics; P1 process lifecycle, sandboxing, package install/update, background schedules and UI are outside this implementation. A stdio process boundary alone is not a security sandbox.

## Commands and remaining work

Run `go test -race ./internal/logging` and `go vet ./internal/logging` for this package. These prove synthetic formatting/bounds/concurrency cases, not production logs, rotation, long-term disk budgets, database recovery or plugin safety. No secrets were placed in fixtures, no production logs were ingested, and no mutation endpoint was called by this work.

Remaining P0 work: logger wiring, stable IDs generated/persisted by job orchestration, bounded actual file sink and rotation, diagnostics status independent of logger, log retrieval/access checks and durable task timeline. Remaining P1 work: all runtime, grants, event persistence/consumers, mutation execution and real acceptance tests described above.
