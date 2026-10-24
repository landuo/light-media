# Original reset-event source review

Read-only review, 2026-10-23. No Go execution or product changes.

Fixed MoviePilot ref: 8a83efeacd3e26b82be495e17d50146e31c96a60.

- `reference/schemaEvent.py:67–78` defines PluginDataResetEventData as a plugin-owned compensation hook, with exact plugin_id and reset_config/reset_data fields.
- `reference/api-plugin.py:739–761` sends the synchronous chain event before stop and the two forced deletions. It ignores the returned event.
- `reference/event.py:375–401` looks up registered chain handlers, returns false when none or none enabled, and invokes enabled handlers sequentially in priority order. There is no hardcoded reset-specific standard compensator in EventManager.
- `reference/mpEndpointTest.py:406–453` verifies event → stop → config delete → data delete → reload intent. Its synthetic ID is SubscribeAssistantEnhanced. It does not execute a compensation subscriber.
- Official introduction commit 93056ed1ff27bebc728dff34b46994efff6c1a0b (#5957), saved as `reference/reset-event-introduction.json`, modifies only endpoint, schemas/event.py, schemas/types.py and endpoint test. It adds no standard subscriber.
- The available original class texts for all 21 compiled IDs contain no PluginDataReset registration. See compiled-original-order-and-reset-v2.json. Cloudflare is now official same-version 1.5.1 source; its installed full-source hash is not available for comparison.

Limits: fixed-ref full application subscriber inventory was NOT obtained. The current default-branch GitHub search finds only schema/producers/tests and an empty consumer_fingerprints baseline, but that branch is not the fixed ref and is NOT proof for it. Do not label the entire original platform as having zero handlers based on this review. Known source supports a generic chain dispatch before stop, not an invented mandatory compensation algorithm or a journal refusal.
