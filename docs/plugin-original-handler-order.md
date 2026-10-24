# Installed plugin startup and Reset chain order

The manager sorts successfully resolved installed executable manifests by
`plugin_order`, ascending and stable in the incoming installed-list order.
Missing `plugin_order` means 0, matching an original class without the attribute.
MoviePilot's Base class instead declares 9999: a plugin inheriting that value
must export 9999 explicitly. These distinct cases are not collapsed into one
hard default. Negative and explicit zero values remain valid. No known-ID list
supplies execution order. Clones use their acquired code's same manifest value;
configuration and invocation identity remain independent. Per-plugin resolve or
initializer failures do not prevent later plugins from starting.

`reset_handlers` is an optional array of `{ "method": "reset_handler",
"priority": 10 }` declarations for the synchronous `plugin_data_reset` chain.
Each method must be declared in `methods` and unique in this array. Missing
priority defaults to 10; explicit zero is not treated as missing. Each method
receives the existing `{ "type": "plugin_data_reset", "data": {
"plugin_id": "target", "reset_config": true, "reset_data": true } }` envelope.
The data's target is the plugin being reset, not necessarily the subscriber.
This is not a new generic messaging system or a durable event queue.

Existing artifacts declaring `events: ["plugin_data_reset"]` and no explicit
reset handlers retain one `event` method at priority 10. Explicit declarations
replace this shorthand, avoiding duplicate invocation. No other event behavior
is changed. A loaded object's handlers are registered under manager-bound
instance/method identifiers. Replacement removes the old registration before
appending, with a stable ascending priority sort. Therefore equal-priority
replacement moves after existing equal-priority subscribers. Disabled and
unloaded objects are excluded; stop removes registrations. Dispatch snapshots
enabled handlers, calls real worker methods synchronously, and continues after
a method error. A stopped/replaced process cannot be invoked through a stale
binding. An empty enabled chain returns false.

Pinned reference: MoviePilot commit
`8a83efeacd3e26b82be495e17d50146e31c96a60`, plugin.py start (86–108),
plugin-base.py plugin_order (36), and event.py add_event_listener (176–202)
and synchronous chain dispatch (380–402). This change establishes ordering,
registration, and error isolation only. It does not claim full parity for
in-process Python event-object mutation across independent Go processes.

Evidence: the owned handler-order fixture is a real compiled process, acquired
through the production artifact install path. Tests observe invocation sequence
from three workers and an independently loaded clone, including multi-handler
priority, default shorthand, replacement, disabled state, failed init, failed
handler, and stop. This synthetic fixture is not a live user plugin or a browser
acceptance test. Ordinary and race results must be recorded separately.
