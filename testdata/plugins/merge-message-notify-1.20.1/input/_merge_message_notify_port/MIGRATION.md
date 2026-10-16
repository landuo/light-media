# Integration and remaining work

Do not copy this candidate into the application as a completed MergeMessageNotify replacement. Keep the known dependency boundaries visible until their real Go implementations and source references pass. No root App file, existing plugin directory, or main go.mod/go.sum was modified.

The candidate's direct runtime imports are Go standard library, `light-media/internal/plugins` for the real SDK, and `light-media/internal/releasemeta/legacyanime` only for the already verified CPython strip predicate. If migrated, change module/import paths, move the exact source/fixtures/provenance/licenses, and keep streaming gzip tests. No Python runtime or dynamic Python-channel importer is needed in the product.

## Existing methods and dependencies

| Installed source method | Go candidate | Required next adapter |
| --- | --- | --- |
| `MergeMessageNotify.init_plugin` / `__fix_config` / registration | `Plugin.Init` with frozen persisted snapshot + trusted save-origin fact | Config owner/CAS; separate local-vs-persisted input if host can present different values |
| component prefix get/update; source None/default rules | `ComponentConfig` / `UpdateComponent` | Bound instance store, preserve unknown fields and exact numbers |
| `get_form` and recursive model prefixing | `Form`, original executed table filtered by actual registrations | Original UI platform renderer, no new page/API/service |
| `listen_notice_message_event` | `DispatchEvent` / `dispatch` | Frozen original event identity and payload, typed notification enum, durable event receipt |
| `CustomChannel.__test_once` | `Init(..., saveRequest=true)` | Explicit trusted request origin; never replay saved one-shot on boot |
| concrete channel builders / `send_message` | `send`, `bot`, `bark`, `onebot`, `httpChannel`, `email` | Granted per-destination protocol transports; full Mako / Apprise / MIME libraries below |
| `TemplateUtil.render_text` | `Host.Render`; limited `RenderBasic` only | Full Mako 1.4.1 semantic implementation, including fallback only after an actual rendering failure |
| `stop_service` / `__gc` | `Plugin.Stop` | Actual owner retire/reconfigure stop, separate from worker process stop |

## Minimal implementation sequence

1. Freeze a host namespace containing instance, generation, config revision and original notice ID; preserve event type presence/None and entire immutable notice data. The child worker only submits an opaque bound event ID, never a URL, credential, recipient or arbitrary notification body. Do not create a second arbitrary RPC API.
2. Implement exact persisted/local configuration snapshots and one transaction for default/one-shot updates. `saveRequest` is a trusted origin equivalent to the original stack's `app.api.endpoints.plugin.set_plugin_config`; a config key cannot authorize it. Source test-once sends even if the plugin-wide enable switch is False.
3. Finish source Mako dependency from `lexer.py` → `parsetree.py` / `pyparser.py` / `_ast_util.py` → `codegen.py` → `runtime.py` / `filters.py` / namespace/cache/lookup dependencies. Start with actual syntax probes already retained, then add complete source control/expressions; do not replace Python expressions by Go text/template. Runtime must remain Go.
4. Finish Apprise's actual `apprise.py` / asset/config/URL parsing / plugin manager → selected plugin modules and attachment/HTTP dependencies. A generic URL forwarder cannot reproduce all 1.9.9 services. Retain unsupported branches as explicit failures until real implementations exist, without removing them from the delivery target.
5. Finish MIME byte serializer and SMTP owner adapter, using the retained actual email/smtplib source and fresh offline original bytes. Add synthetic loopback SMTP/TLS servers for full conversation, deadlines/cancel, byte encoding, rejected AUTH/RCPT/DATA, and QUIT failures. Keep 18 current source-error branches and raw MIME reference.
6. Implement granted HTTP adapter under existing host policy: bind scheme/host/path as source configuration requires, preserve kwargs None/absence/bytes, source proxy selection, Pushover image GET timeout/stream behavior, and exact result parsing. Do not log raw errors containing secrets. Validate with only synthetic loopback servers. This package does not grant arbitrary worker-supplied requests.
7. Connect real notice producer and original routed-message exclusion. Config save/boot/event/Stop must share one owner; worker process cleanup must not clear a still-live owner's registrations. Outgoing channel results must not re-enter the same unrouted notice event and create a loop.
8. Add source operation receipts and uncertainty handling for non-idempotent notifications. A request timeout after a possible remote send cannot be blindly repeated because the source API does not promise idempotency. Reconfiguration, disable and uninstall must stop new granted effects and retire the old owner generation before replacement.

## SDK contract already exercised

`Manifest` retains ID/version/name/author and original `notice.message` event value. Methods are `config_form`, `invoke`, `stop`; capabilities are `merge_message.form`, `merge_message.configure`, `merge_message.event`. There is no public API/page/service/remote command. Init/reload sends an empty configure request; event sends exactly `{"event_id":"..."}` with no extra keys. The real SDK Runner/Serve subprocess test binds host identity, ignores worker config, initializes an owner, performs an actual controlled IYUU request through source-derived Go logic, obtains the original form and checks untrusted event data is rejected before the host.

`Runner` automatically sends worker `stop` after every invocation. `Handle("stop")` therefore returns None without calling the long-lived owner. Original `Plugin.Stop` must be called only by actual host lifecycle retirement; confusing these two stops clears every channel after init. The executable itself is still unregistered, and these local SDK results are not App integration or deployment evidence.

## Verification boundaries

No real endpoint, recipient, old instance, database migration or credentials were involved. The 4,720 channel corpus uses actual original channel methods and controlled protocol responses; it proves builder/result semantics for those inputs. The 102 origin/reset cases deliberately stub channel sends and prove only lifecycle behavior. The 19 Mako probes record both implemented and explicitly unimplemented behavior. SMTP comparison is complete at operation and decoded-message level for recorded cases, not raw MIME byte equivalence. All still-unimplemented branches listed in README remain required work.
