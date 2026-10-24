# Standard event target filtering

This focused change preserves MoviePilot's pinned EventManager behavior at
8a83efeacd3e26b82be495e17d50146e31c96a60, app/core/event.py:433–496:

- Only `message.action` treats a truthy `__mp_target_plugin_id` as a target.
- Every dictionary broadcast removes that top-level internal key before any
  subscriber sees it, including other event types and false/empty targets.
- Filtering uses the manager-bound source class identity. A clone targets its
  own renamed class (`InstanceID`), not every worker sharing its artifact ID.
- Missing, null, false, zero and empty-container targets do not restrict the
  broadcast. Truthy unknown targets reach nobody. Matching is case-sensitive.
- The source's shallow dictionary copy leaves the caller's dictionary unchanged
  and shares nested object references. The SDK preserves all nested JSON values
  and caller bytes but does not claim cross-process Python reference sharing.
- Numeric event values retain exact JSON integers. Target evaluation uses
  `UseNumber`; valid non-string targets cannot accidentally become a broadcast.
  Python JSON decimal underflow/overflow truthiness is accounted for separately.

This adds no ingress, queue, persistence, provider, business schema or plugin
implementation. Existing subscription, event-enabled, callback error isolation,
load ownership and shutdown admission behavior remains unchanged.

The owned fixture is a real standard Base SDK executable. The integration test
acquires it through the artifact endpoint, creates two clones, sends commands
through the authenticated Web endpoint, and checks separate process IDs and
persisted callback output. It covers source/clone/unknown/case-sensitive targets,
falsey and truthy values, exact large integers, unchanged declarations, trusted
Web identity, non-MessageAction key removal, unsubscribed events, disable, stop,
and independent delivery after one handler fails. It is synthetic protocol
coverage, not a live user plugin or a core business producer test.

The pinned Python method oracle passed 42 target/event cases plus invalid
handler-identity rejection and disable-after-submission source checks. No Go
format/build/test or Node command was run in preparation. The two proposed Go
roots require parent execution, with an actual freshly built worker supplied in
`OWNED_EVENT_TARGET_FIXTURE_BINARY`:

- `TestOriginalBroadcastEventTargetSourceValues`
- `TestOriginalEventTargetActualSDKCommandsClonesAndIsolation`

Other generic gaps remain separately recorded: disable recheck after submission,
standard callback error notifications, CommandRegister interception, and the
legacy Reload spelling. This layer does not claim to resolve them.
