# Owned S13 four-command worker — prepared, not built or run

Intended new path: `cmd/owned-downloader-controls-fixture/main.go` (plural **controls**, distinct from old `owned-downloader-control-fixture`, which is not used or modified).

- PluginID `OwnedDownloaderControls`, version 1.0, entry `owned-downloader-controls-fixture`.
- Standard persistent serial `plugins.Serve`, ordinary lifecycle + snapshot/HTTP. No concurrency/session framework replacement.
- Fixed POST API key `controls`, path `/controls`, bearer authentication. No worker network listener/client or URL field.
- Exactly four manifest capabilities: qB set_tags/set_category/stop and TR stop. No source/base config capability, arbitrary cap or old eight command exposure.
- Exact downloader IDs `owned-controls-qb` with kind qbittorrent and `owned-controls-tr` with kind transmission. Controller must configure these as owned synthetic peers; fixture never selects or discovers other config, endpoint or credentials.

## Input and return

All operations are decoded before any HostCaller use. Exact keys (duplicates/case aliases/unknown fields rejected), body at most 16 KiB, 1–8 operations, optional `continue_on_error` and `probe_previous` booleans. Present null is required where allowed; omitted source args are invalid. These finite **fixture** bounds do not change product source selector semantics.

```json
{
  "operations": [
    {"kind":"qbittorrent","op":"set_tags","downloader_id":"owned-controls-qb","ids":["owned-a","owned-a"],"tags":["owned-label","owned-label"]},
    {"kind":"qbittorrent","op":"set_category","downloader_id":"owned-controls-qb","hash_string":"owned-a","category":null},
    {"kind":"qbittorrent","op":"stop","downloader_id":"owned-controls-qb","ids":null},
    {"kind":"transmission","op":"stop","downloader_id":"owned-controls-tr","ids":[7]}
  ]
}
```

Finite values (only synthetic constants; no caller URL, raw-frame escape hatch or genuine task IDs):

- qB ids: null, empty string, `owned-a`, `owned-fail`, `owned-a|owned-a`, empty list, single `owned-a` list, or duplicate `owned-a` list.
- qB tags: null, empty list, `owned-label` string, duplicate `owned-label` list, or single `owned-fail` list.
- qB hash_string: null, empty string, `owned-a`, `owned-fail`; category: null, empty string, `owned-category`, false.
- TR ids: null, empty list, integer 7, `[7]`, fixed forty-`a` synthetic string, or `[0, forty-a-string]`.

Each operation uses the actual corresponding typed SDK method and the current HostCaller exactly once; there are no synthetic success results. `result_kind=source_none_not_mutation_success` with raw null for tag; `source_bool` with actual true/false for others. `source_truthy` is false for None and actual bool for controls. Error clears result/type/truth fields, exposes only bounded safe error code, and never echoes Error() or sensitive inputs. `ok/succeeded` count completed SDK returns, **not successful mutations**; False and None remain separately visible. The controller must inspect real results and peer calls, not HTTP 200 or `ok` alone.

Snapshot includes PID/init_count/invocation/requested/completed/succeeded/failed/results. Per-result host_calls counts actual attempted callbacks and JSON payload byte lengths, not physical wire/frame counts or daemon state. `continue_on_error` enables one later listed operation after a failure without retrying any operation.

## Explicit stale-caller observation

Only when `probe_previous=true`, one immediately previous HostCaller is attempted once using fixed qB set_category and `{op:"invoke",downloader_id:"owned-controls-qb",hash_string:"",category:null}`. This is **not a read API** or a replay of a successful mutation. Under correct source semantics empty hash returns False before selected native category dispatch, even if an identity bug erroneously admits the callback. Never replay captured tag/stop/upload mutation. Denied requires error plus empty response plus actual `invalid_host_call`/`PLUGIN_CALL_CLOSED`; `available:false` proves no denial. Current business calls always use current caller. No background registry/retry/rollback.

## Reuse and evidence boundaries

Five parser/error helpers are byte-copied from actual S03 ordinary SDK fixture; original handle/Serve/main structure retained with only API name/path and fixed stderr description changed. The old provider-style `owned-downloader-control-fixture` with direct HTTP is **not reused**. Parent approved this exact new fixture identity and nonmutating-source-branch stale probe before freeze.

This owner only ran a stdlib text writer and pinned gofmt on an audit candidate. No Go list/build/test, SDK callback, native client, HTTP, original-source/vendor import, DB, env/credential access or process launch. Source original17 and SDK/leaf gates are separately credited; native/installed-worker/two-normal-host and configuration guards remain upcoming coordinator gates. Preparation is not PASS. Fixture author is not its independent reviewer.
