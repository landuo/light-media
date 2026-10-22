# Shared system-message timezone correction

The browser acceptance found that a fresh UTC-server message appeared nine hours old in the cloud browser. The producer supplied an unzoned wall-clock `date`, which the browser interpreted in its own timezone. This was a real common display defect, not a RemoveLink or SiteStatistic business failure.

## Compatibility and implementation

- Preserve the original `date` field, source ordering, role, text, FIFO behavior, and minute-level duplicate key.
- Add `timestamp`, formatted as RFC3339Nano with the actual offset. The bell prefers this absolute instant. This additive native enhancement intentionally differs from the pinned original unzoned display contract.
- Frames without `timestamp` retain the old interpretation. Their original timezone cannot be recovered, so none is guessed. A present malformed timestamp is rejected instead of inventing an age. The queue is in-memory, not migrated message history.
- No plugin-specific timezone or clone logic is added.

## Validation

See `system-message-timezone-verification.json` for exact input and artifact hashes. The four source files stayed unchanged throughout frontend check, all 202 frontend tests, build, affected App ordinary/race tests (7 root / 16 inclusive each), vet and actual server build. All exited zero; this is not a full-repository test claim.

The timestamp regression covers UTC, positive/negative offsets, date rollover and spring/fall daylight-saving offsets across four consumer timezones. Existing relative-time thresholds, future/past dates and lifecycle tests remain in the full frontend set. Duplicate-minute semantics stay unchanged.

A bounded, owned fixture used the actual App handler, production embedded UI, authentication, FIFO and SSE DTO. It emitted two synthetic messages for the same instant using UTC and +08:00. In the assistant cloud Chrome notification bell, both displayed “几秒前”, with correct role icons and text. This directly validates the common display chain, not a production account or a real plugin producer. The fixture exited zero and its child was reaped. No user service, account, security setting or external exposure was used.

Existing SiteStatistic acceptance remains plugin-scoped. This correction closes the subsequently discovered shared display defect; generic clone/lifecycle development is tracked separately.
