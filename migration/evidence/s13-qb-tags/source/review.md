# qB tag cleanup/poll — actual source component accepted

Coordinator tool `1c91ee` exited 0 on 2026-10-31 23:50:53 UTC. Independent local readback confirms the exact pinned `-I -S -B` argv, result/exit 0, empty stderr and raw SHA-256 `267d2e4fa62ed38ce53e4e6a6bf8ebfc5676058b413adbc072e74aa81c9c8617`. The actual 16 case inputs equal the frozen cases; four complete original node bindings equal preexecution bindings; all 16 bound input files remain unchanged. The coordinator used fresh exclusive outputs, not the problematic mkdir-failure continuation in the preparation COMMAND draft.

Observed, not target-derived:

* Direct delete: missing client False/no call; False or None replies still yield True after removeTags then global deleteTags. First supplied exception stops before the second call; second supplied exception retains the first call and yields False. Original Chinese error log retained.
* Missing-client and empty-list polling request nine sleeps of three seconds and return None. This records **27 requested seconds**, not elapsed wall time. Every read follows its sleep request.
* Missing/null hash continues; a later empty-string hash stops, invokes both cleanup calls and returns empty string. False hash likewise stops, cleans up and returns False; neither is normalized to None.
* Full subset filtering preserves first selected row order. A later malformed tags value invalidates an earlier match, logs the original exception and causes another delayed read. Returned list objects are actually cleared by the original finally, including the malformed-row case.
* Query RuntimeError is logged and followed by another delayed read. Either cleanup exception logs/returns source False internally, but polling returns the previously found hash. Supplied None cleanup replies do not prove a real unsupported-version gate.
* Falsey tags with first row integer produces the actual uncaught AttributeError (`'int' object has no attribute 'get'`); the original list remains `[7]`. The execution has 15 return outcomes and one deliberately captured uncaught source exception, not “16 successful business results.”

The original four bodies executed against explicit recording qbc/logger/time ports. No original constructor/module import, vendor, HTTP, daemon or actual sleep was executed. Forbidden-import absence and zero input drift are collector observations; they are not a network syscall trace. Full raw traces, before-return snapshots and after-source list values must remain the oracle; no pruning into desired Go outcomes. This acceptance does not establish target implementation, native mutation/proof behavior or production timing. Machine bindings and outcome list: `source01-final.json`.
