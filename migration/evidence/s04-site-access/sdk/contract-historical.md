# S04 site access SDK — candidate 01, not executed

Scope: two new SDK files only, exposing the original `SiteOper.success` and `SiteOper.fail`. This is not all SiteOper, not the access-statistics algorithm/storage implementation, and not an ordinary installed-worker acceptance result. Source methods remain fixed at MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`, `app/db/site_oper.py:225–285`.

## Public API / exact wire

```go
const SiteAccessSuccessCapability = "site.access.success"
const SiteAccessFailCapability = "site.access.fail"
type SharedSiteAccess struct{ Host HostCaller }
func (s SharedSiteAccess) Success(ctx context.Context, domain string, seconds *int64) error
func (s SharedSiteAccess) Fail(ctx context.Context, domain string) error
```

Success sends exactly `{"domain": <string>, "seconds": <null-or-int64>}`. A nil pointer is explicit JSON null; zero and negative numbers remain present, without clamping. The host may also accept an omitted seconds key as source None; this SDK always emits it. Fail sends exactly `{"domain": <string>}` and no seconds key. These two fixed typed structs cannot emit duplicate or unknown request fields. No additional operation selector/envelope or new resource family is introduced.

The domain is not parsed as a URL, normalized, trimmed, made nonempty or looked up. Empty strings, whitespace, Unicode and JSON-escaped control characters remain ordinary strings. Invalid UTF-8 is rejected rather than silently replaced by encoding/json. Timing uses the authorized finite SQLite int64 wire domain, not Python arbitrary numeric objects; extreme int64 wire preservation tests do not claim all source arithmetic has been tested.

Only one valid JSON null reply is success. UTF-8 validation, json.Valid, the existing MaxPayloadBytes limit and exact null-token comparison reject false/true, objects (including unknown or duplicate fields), arrays, strings, numbers, trailing values and non-JSON whitespace. There is no need to depend on the S03 object decoder for a scalar-only result.

The existing ordinary resource request bound remains 128 KiB. Overflow is an explicit SDK error, not domain truncation or a claimed unbounded source equivalence. No source algorithm/clock/DB/network work occurs in the SDK.

## Error and authority boundary

The current HostCaller and nonnil context are required. Pre-cancelled calls do not reach the host. Host errors return unchanged; cancellation observed after a successful host reply remains an error. Every invocation makes at most one callback, with no retry. A post-call error/cancellation does **not** assert that the host transaction was rolled back or no counter changed. Capability, current identity/scope, DB transaction and unknown-effect handling remain host responsibilities.

## Proposed finite gate

Exact package: `./internal/plugins`.

Exact roots:

```text
TestSharedSiteAccessWire
TestSharedSiteAccessBoundaries
```

The first records exact two-capability wire payloads, null/zero/negative/int64 endpoints, verbatim domains and valid null replies. The second rejects malformed/non-null replies and invalid envelope/input cases; checks original host errors, pre/post cancellation and no retries. Both use only a recording HostCaller. No worker, source import, SQLite, daemon, network, environment override, hidden compilation or runtime clock is used by these tests.

Current status: source written and the two exact files formatted with the pinned gofmt. **No go list/build/test, source execution, or gate PASS.** The source collector and App implementation are separate layers with separate owners. No shared hooks, ledger or prior source evidence was modified. The SDK must be independently reviewed and then compiled/run by the coordinator before its tests are credited.
