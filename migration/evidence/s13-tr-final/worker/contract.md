# Owned serial TR final fixture — preparation only

Baseline `cd21eaf2bd8aeeb4af012e7a934faad8c5b9b288`. Proposed new targets only: `cmd/owned-tr-final-fixture/main.go` and `main_test.go`. Both remain external candidates. No main-tree/ledger changes, Go compile/test, process, HTTP or network execution by this owner.

## Manifest and ordinary worker

`OwnedTRFinal`, version `1.0`, entry `owned-tr-final-fixture`; API key **`tr_final`**, path **`/tr-final`**, POST, bear auth, JSON required. Main calls `manifest.Validate()` before either `--manifest` output or serving. It uses existing ordinary **`plugins.Serve`**, not ServeConcurrent, with no new budget, clock, worker retry, network client, database or filesystem material operations. Exactly two capabilities: `downloader.transmission.set_tags` and `.change`. Fixed DownloaderID **`owned-tr-final-tr`**.

Inherited qB fixture helpers are byte-identical: `exactObject`, `jsonValue`, `requiredString`, `optionalBool`, `safeErrorCode`, `ownedValue`, `countedCall`. Structured duplicate/unknown inputs are rejected, JSON uses UseNumber, raw error text is never emitted. The old qB fixture is read-only and independently bound. This new fixture intentionally omits previous-caller probing, since it is not requested for this finite CLI slice.

## Exact finite requests for coordinator CLI

Top-level `{operations:[...],continue_on_error?:bool}`; 1–8 operations, 16 KiB. Each operation includes `kind:"transmission"`, `downloader_id:"owned-tr-final-tr"` and one fixed op:

* `set_tags`: exactly `ids:7`, `tags:["owned-new","owned-old","owned-new"]`, and `org_tags:["owned-old","owned-retained"]` or null.
* `change`: exactly `hash_string:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`; all four options explicitly present. `upload_limit` is `"0"`, `"owned-invalid"` or null; `download_limit` is -4 or null; `ratio_limit` is 2.675 or null; `seeding_time_limit` is 0 or null.

These are owned fixture values, **not new source/product restrictions**. They support duplicate/old labels, string-zero/negative/rounding limits, direct ValueError before HTTP, all-None vendor False, and a selected500 followed by a separately explicit same-worker recovery. The fixed peer chooses its own failure phase; worker inputs have no hidden transport selector. The coordinator's two-host normal CLI can repeat the same permitted request after natural close/reopen with the same DB and no artifact refetch. No real daemon, forwarding, CONNECT or environment proxy/TLS manipulation belongs to this fixture.

Examples (ordinary API route `/api/v1/plugin/OwnedTRFinal/tr-final`):

```json
{"operations":[{"kind":"transmission","op":"set_tags","downloader_id":"owned-tr-final-tr","ids":7,"tags":["owned-new","owned-old","owned-new"],"org_tags":["owned-old","owned-retained"]}]}
```

```json
{"operations":[{"kind":"transmission","op":"change","downloader_id":"owned-tr-final-tr","hash_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","upload_limit":"0","download_limit":-4,"ratio_limit":2.675,"seeding_time_limit":0}]}
```

For conversion failure replace upload_limit with `"owned-invalid"`; for no arguments set all four options null. To observe same-worker explicit recovery, supply two operations and `continue_on_error:true`; no failed mutation is automatically retried.

## Observable result distinctions

Response includes actual process PID, init count, invocation count, per-operation exact SDK HostCaller count/input/output JSON bytes, requested/completed/succeeded/failed, and raw bool results. HTTP200 is just the fixture envelope. `attempt.ok` means a completed **typed SDK return**, including false; `source_truthy` retains that bool and never claims daemon mutation success. All-None source False therefore has a successful callback and typed return with result false.

Direct SDK `TRChangeSourceError{Class:"ValueError"}` becomes attempt `error_code:"source_exception",source_error:"ValueError"`, with no error text/result. Its callback can have succeeded while the typed attempt failed. A selected HTTP500 host error remains a failed callback/platform error with no result. The fixture neither classifies transport outcomes nor retries them. Actual native requests/counts/log persistence, same-worker PID and cold host behavior must be independently asserted by the normal CLI driver, not inferred from this worker source.

## One planned unit root

`TestOwnedTRFinalFixtureManifestAndRoute`: valid underscore key and manifest, invalid hyphen key rejected, exact registered route→actual two SDK methods through an explicitly recording HostCaller. Three operations distinguish normal true, normal false and typed ValueError with exactly one successful callback each. No worker subprocess, real App/native HTTP or hidden build is run by the unit test. Pattern `^TestOwnedTRFinalFixtureManifestAndRoute$`; no fixture environment or testdata.

This is unexecuted source preparation. `manifest.declared.json` is a manually bound declaration, **not actual `--manifest` output**; actual build/program SHA and manifest receipt must be supplied by the coordinator before normal CLI use.
