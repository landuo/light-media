# Original core + endpoint composed oracle

Fixed original backend: 8a83efeacd3e26b82be495e17d50146e31c96a60. Executed 2026-10-23 UTC. Result: 12 cases pass, 0 fail.

## What actually executes
Unmodified extracted AST bodies of core start, init, stop, private stop, reload, clone, configuration methods and file/metadata/asset rewriting, composed with original endpoint clone, reload, register, save, reset and uninstall. Original source lines and exact extracted text are recorded in extracted-methods.json. The earlier oracle and oracle-v2 are unchanged; their narrower coverage must not be described as this endpoint composition.

Synthetic boundaries: plugin discovery reads known temporary metadata; plugin init is a deterministic recorder/mutator/exception injector; config/KV stores, events, schedule/command/API registration and folder operations are recording adapters. File operations affect only temporary synthetic directories. No network, downloaded plugin execution, real user data or whole-service validation. Registration records invocation/order, not actual routes or jobs installed. This verifies original orchestration, not a Go implementation.

## Literal implementation sequence
Core clone (plugin.py 1905–2003): validate → copy directory and rewrite names/assets → installed append → if source config truthy, copy it and unconditionally set both enable=false and enabled=false → core reload (stop→start/init→PluginReload) → re-read clone config and, only if running exists and latest config truthy, directly call instance.init_plugin again → return true/id. No clearing onlyonce, plugin_ids or other trigger fields; no copying source KV.
Endpoint clone (api-plugin.py 936–962): core clone → if success, endpoint reload (core stop→start/init→PluginReload; then jobs→commands→API registration) → folder copy → success. It does not deduplicate the earlier reload/init.

- Nonempty unchanged config: 3 init calls, 2 PluginReload events.
- Initially empty or None source config: 2 init calls, no clone config write.
- First init changes config but leaves it nonempty: next two calls see the changed config, not a frozen original.
- First init sets config to {}: direct extra init skipped, endpoint reload still initializes with {}; 2 init calls total, saved empty config remains.
- First start/init throws, second succeeds: start catches first error, endpoint proceeds; 2 calls, 2 reload events, success and final running instance.
- Both start/init calls throw: each start catches error; core and endpoint still report success, register boundaries called, class/config/code/installed remain but no running instance.
- Direct extra second init throws: core clone catches and returns false; endpoint does not reload/register/copy folder; first running instance plus copied config/code/installed remain. There is no automatic rollback in these original bodies.

Save (endpoint 975–989): save configuration → core init_plugin (not reload/stop) → register jobs → commands → API → success. Manager init invokes plugin init then state-based event enable/disable and cache invalidation.
Reset (739–761): reset event → stop → force delete config → force delete KV → endpoint reload → success. Endpoint reload includes a second stop (no live instance left), then start/init, reload event and registration. Reset restores a loaded configurable instance with default config.
Uninstall (992–1030): installed remove → API remove → jobs remove → if clone, force delete config/KV and code directory/class → folder remove → manager remove/stop → success. Ordinary uninstall retains config, KV and code; clone deletes only its own such data. Temp filesystem assertions verify the two paths.

## Evidence and reproducibility
Run: python run.py. results.json contains all inputs, complete recording events and responses. baseline-8-results.json preserves the first successful eight-case run before exception cases were added. bootstrap-failure.txt preserves the early harness setup KeyError before any cases; not an original-product failure. manifest.json hashes runner, outputs, source methods and the definition-only oracle dependencies.

These observations are literal source behavior, not recommendations to redesign or claim desirable error handling. They must not be silently normalized to fewer init calls or new compensation rules.
