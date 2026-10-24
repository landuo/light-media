# Synchronous command registration interception

This slice restores the generic plugin framework hook, not a bot, channel,
message-provider integration, or plugin-specific menu feature.

## Declaration and process contract

`Manifest.ChainHandlers` declares `{event, method, priority?}` subscribers.
Priority defaults to 10; explicit zero is preserved. Within an event, identical
instance/method registration replaces the old entry, appends it, and stably sorts
by ascending priority. Different instances and clones have independent identity.

`Events: ["command.register"]` is shorthand for `event` at priority 10. One or
more explicit `ChainHandlers` for `command.register` replace that shorthand,
without double delivery. Generic event names are syntactically validated; a
manifest declaration is not itself an event producer. This slice connects only
the real command registration preparation boundary. Existing `ResetHandlers`
remains a supported SDK declaration; its canonical event is `plugin.data.reset`.

Handlers bind the actual loaded object, not an installed/enabled database flag.
Stop unregisters them; reload binds replacements and respects insertion order;
clone identity and worker lifetime remain distinct. Enabled state is snapshotted
before dispatch. The shared mutable-event dispatcher completes the chain
synchronously, continues after a handler failure, ignores ordinary return values,
and preserves valid mutations made before an exception or panic through the
explicit `HandleMutableEvent` / `DecodeMutableEventSnapshot` contract. JSON
integers are not coerced through float64. The shared SDK contract is JSON
mutation, not transfer of Python object identities.

## Source behavior retained

The executable plugin-command map is completely rebuilt first, whether or not
menu registration is later cancelled. The chain cannot add, remove, retarget or
otherwise rewrite executable command lookup.

The initial menu includes existing core preset metadata and enabled plugin
command metadata. `show` defaults to true; falsey explicit values are omitted.
Each initial row contains only `type`, `description`, `category`, and a truthy
`pid` when present. Missing categories are JSON null. `data`, `event`, scheduler
IDs and callable details are not leaked into menu metadata. There is no existing
other-command collection in this core, so none is invented.

The event starts as `command.register` with `origin: "CommandChain"`,
`service: null`, `cancel: false`, `source: "未知拦截源"`, and the initial commands.
The final event, rather than an ordinary callback return, supplies the menu:

1. `cancel` skips menu registration but not command execution rebuilding.
2. Matching nonempty requested plugin-instance ID and final `source` forces
   registration; an unscoped initialization does not force it.
3. Menu command names must be a subset of existing core-preset and executable
   plugin names. Unknown names are discarded. Hidden executable names remain
   eligible if an interceptor explicitly selects them.
4. An empty filtered menu is skipped unless forced.
5. A menu equal to the previous selected snapshot is skipped unless forced.
   Equality uses source-compatible JSON numeric comparison, not serialized key
   order or float64 conversion.

Source references are `command.py` 179–221 and 232–267,
`schemaEvent.py:CommandRegisterEventData`, and `event.py` synchronous chain
registration/dispatch. Exact source and dependency hashes are in the slice's
provenance files.

## Explicit unavailable downstream dependency

This core has no outbound `CommandChain.register_commands` provider. At a changed
or forced selection, the host retains the selected menu snapshot in the same
place the original updates `_registered_commands`, then returns
`errOriginalCommandRegisterProviderUnavailable`. Background initialization logs
that missing dependency. The snapshot is prepared metadata, not a delivery
receipt or proof that any menu is installed. An unchanged or cancelled selection
does not pretend to call a provider. There is no HTTP register API, provider stub,
channel I/O, queue/revision/recovery gate, or business-plugin-specific branch.

## Validation ownership

Owned actual SDK workers exercise projection, shared mutations before failure,
ignored ordinary returns, cancel, force, unchanged and empty selections, unknown
name filtering, disabled state, current-object replacement, clone independence,
and stop/failed-reload cleanup. A missing-provider result is asserted, never
reported as successful delivery. Tests are prepared for the parent's single
validation window; this slice does not claim they have run.
