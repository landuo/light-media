# Standard Web command ingress: bounded framework restoration

## Fixed upstream contract

Repository: https://github.com/jxxghp/MoviePilot

Commit: `8a83efeacd3e26b82be495e17d50146e31c96a60`.

Except for the explicitly minimized `core-config.py` excerpt, Python reference
files are exact GitHub blob bytes. The two source manifests record the upstream
path, blob SHA, and immutable URL. The configuration excerpt preserves only
`Settings.API_V1_STR` from upstream line 66, flattened to a module-level constant.
All other configuration code and values are intentionally omitted. Its manifest
separately identifies the full upstream blob and the local excerpt hashes; it does
not claim that the local excerpt is the complete original file. The oracle does
not parse, import, or execute this configuration file: it is path-constant evidence
and participates only in the reference-file hash inventory. The previously copied
`command.py` and `event.py` each had one extra terminal newline; this testdata
copy removes that newline so it exactly matches the pinned official blob.
No original reference or frozen tree was changed.

- `core-config.py`: `API_V1_STR = "/api/v1"`.
- `api-apiv1.py`: mounts `message.router` at `/message`.
- `api-message.py`, `web_message`: `POST /api/v1/message/web`; query `text`,
  optionally overridden by JSON `text`; authenticated superuser supplies
  `channel=Web`, `source=current_user.name`, `userid=current_user.name`,
  and `username=current_user.name`.
- `db-user-oper.py` and `core-security.py`: the upstream dependency resolves a
  verified token to an existing superuser. This host retains its existing
  administrator session authentication and write-origin protection; this change
  does not introduce MoviePilot JWT compatibility, API-key login, or a new grant.
- `chain-message.py`: slash-command path sends `EventType.CommandExcute` with
  `cmd`, `user`, `channel`, `source`, and optional `processing_status`.
- `schema-types.py`: the actual enum values are `command.excute` and
  `plugin.action`. The spelling `excute` is original, not a new event name.
- `command.py`: registration preserves each declaration's event and data;
  `command_event` uses Python whitespace splitting; `__run_command` deep-copies
  data, overlays message context, and only overrides `arg_str` for nonempty args;
  `send_plugin_event` broadcasts the declared event. `data.action` is not a
  direct Python or SDK method name.

The other original endpoint, `POST /api/v1/message/`, uses API-token verification
and a configured channel parser. It is deliberately NOT restored or bypassed by
this patch. No invented unauthenticated webhook or arbitrary-event endpoint is
added, and no Lark queue/receipt/business algorithm is revived.

## Implemented scope

`standardWebCommandHTTP` is called inside the existing authenticated API wrapper.
It handles the original `/api/v1/message/web` path and native `/api/message/web`
alias. The current administrator name is read from the host's administrator
table. Request `user`, `source`, `channel`, `event`, `action`, `data`, and `method`
cannot select the execution identity or target.

The command branch emits the genuine `command.excute` observer event, then the
core Command consumer resolves the installed live declaration and broadcasts its
event through `broadcastOriginalPluginEvent`. That existing dispatcher invokes
the actual loaded SDK `event` handler only for subscribed, event-enabled objects.
It does not use a job queue or persist an execution receipt. The response says
`accepted`, not that asynchronous business work has completed.

The registry keeps JSON integers exact with `UseNumber`, permits missing action
and null data, and does not mutate declared nested data. Unknown command names
and case mismatches retain the original no-op behavior. Explicit `/noai` text
escape and the original case-insensitive `/ai` prefix classification are kept.

The current scoped migration also restores the ordinary text fallback as
`user.message`, with the fixed `text`, `userid`, `channel`, `source`, `chat_id`,
and `reply_to_message_id` fields. An actual pending input still takes precedence.
Its fixed-source replay is `../standard-subscription-events/user-message-oracle.json`.
The earlier bounded-stage assertion that ordinary text must return 501 is
withdrawn; its actual red overlay evidence is retained in the current validation
artifacts as `events36-retired-user-message-red-tests.jsonl`.

AI, image-only and unimplemented built-in command branches return explicit 501.
Slash
commands may include image metadata: as in the original command branch, only
text and trusted identity enter CommandExcute. This bridge does not parse image
metadata, retrieve media URLs, or implement media business behavior.
Input JSON is bounded by the host's existing limit and requires a single JSON
object with string-or-null `text`; malformed/nonobject JSON is rejected rather
than using the upstream endpoint's permissive parse-failure fallback. Processing
status is null because this narrow Web ingress does not fabricate a typing or
message-history subsystem. These are deliberate, documented scope limits, not
claims of whole-MessageChain parity.

## Evidence and execution status

`source_oracle.py` executes AST-extracted pinned upstream method bodies for the
endpoint text parser, MessageChain command branch, and Command registry/dispatch.
It generates `source-oracle.json`: 11 owned cases, including Unicode whitespace,
no action, null data, hidden commands, identity forgery inputs, query fallback,
body override, no-AI escape, case-sensitive lookup, and the explicit AI branch.
The captured dependency stubs are not evidence of product authentication or IPC.

`plugin_original_command_ingress_test.go` installs the separate actual compiled
`cmd/owned-command-ingress-fixture` through an owned local artifact endpoint and
drives requests only through `a.Handler()`. The fixture declares action
`not_a_method`, has no such SDK method, and records both `command.excute` and its
declared `owned.command` handler; a genuine Base SaveData is observed as output.
It tests authentication, tampered/expired sessions, cross-origin/cross-site
rejection, request identity isolation, exact large integers, registry immutability,
no-action/null/hidden commands, query text, no-op commands, disable, and zero
retired `plugin_runs` rows.

The old standard fixture and its E2E test now use HTTP -> declared event -> actual
event handler instead of directly invoking action as a method. Existing interval,
same-object, Base, clone, reset, close/stop, and zero-queue assertions remain.

The earlier analysis-stage execution restriction is historical and superseded
by the user's authorized development/acceptance task. Actual Go SDK and Linux
cmd/server evidence is recorded separately in
`../plugin-module-no-official-auth-20261026/services40-result.json` and the
current ordinary-event increment artifacts. Relevant original ingress tests:

- `TestOriginalWebCommandActualSDKIngress`
- `TestOriginalWebCommandTextSourceRules`
- `TestOriginalStandardDirectModuleEndToEnd`

The shared HTTP attachment is integrated in the current target working tree.
