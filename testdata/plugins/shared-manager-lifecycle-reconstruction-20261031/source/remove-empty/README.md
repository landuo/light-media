# S10 empty-PID remove source addendum — PREPARED, NOT EXECUTED

One extra source case only: `remove_plugin("")`, with two owned running classes in insertion order. First close/stop_service succeed; second close raises a fixed ValueError. The actual original algorithms determine callback order, whether second stop_service runs, error handling, final class/running registries and tools-cache revision. “Continue after error” here means continuing to registry/cache clearing after the second object's close error; there is no third plugin.

The script selects exactly five original methods from fixed 8a83 `app/core/plugin.py`: remove_plugin, stop, __stop_plugin, _clear_plugin_modules and clear_plugin_agent_tools_cache. Selected bodies/decorators/annotations/defaults are unchanged. A controlled class envelope preserves the PluginManager name and private-name mangling, without executing its source constructor, ConfigReloadMixin, Singleton, source module imports or unrelated fields. Callback/event/logger inputs and initial dictionaries/lock/cache are explicit fixtures, not real business plugins or EventManager.

## Safety / layer

- Pure stdlib imports only; no requests/urllib3 imports, source imports, subprocess, DB, socket, bind/connect/probe, HTTP, timers or environment writes. The script itself writes only stdout; it reads the exact source, script, input manifest and Python executable for hashes.
- Execute using the existing byte-bound Python with `-I -S -B`: no site/.pth processing or bytecode writes. The script requires the three default meta-path finders, never replaces any importer, and never fabricates a sys.modules entry.
- Before the call, the whole real `app.plugins` prefix must be absent. If any matching module exists, execution stops. Thus the unchanged all-prefix `_clear_plugin_modules` branch has no module to delete. It genuinely invokes `importlib.invalidate_caches()` on the standard finders; this is cache invalidation, not a claim that real plugin modules were evicted.
- Runtime prefix/module/thread checks are finite assertions, not a syscall trace or proof about arbitrary stdlib/native behavior. There is no fake network guard and no reopening of the prohibited requests/IPv6 probe.
- Source, script and resolved Python bytes are checked before and after the observation. No source/Go product, old collector, C fixture JSON, ledger or existing evidence is modified. There is no Go or source-method execution during preparation; only static AST syntax/node inspection.

## Coordinator execution after review

Use a new receipt prefix and refuse to overwrite prior outputs. Capture command/start/end/exit/stdout/stderr separately; retain any failure unchanged. The collector has no command-line input and runs this one case only.

```sh
/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/source-runtime/s10-venv/bin/python -I -S -B /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/audits/s10-remove-source-addendum/capture_empty_remove.py
```

Suggested new output prefix: `execution01.*` in this directory (not yet created). The resulting actual JSON may later become D3 reference data; this preparation does not claim the result, migration equivalence or a runtime PASS. No warm SQLite rerun, other seven-method replay, arbitrary recursive callback, real plugin or file deletion is requested.
