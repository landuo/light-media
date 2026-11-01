# Candidate02 — native WARNING to existing host WARN seam

Independent static review of candidate01 found that the existing host logger recognizes `WARN`, not native Python-style `WARNING`; the original candidate would filter that warning at a WARN threshold. This is a static finding, not an executed test failure. Candidate01, its source/test bytes, bindings, contract and checkpoint remain unchanged.

Candidate02 changes only the new App Log closure to map `WARNING` → `WARN`, exactly as the existing `pluginOriginalLogHost` does. Native/source level and message remain unchanged; no global logger/config/permission behavior changes. In the same existing wire test root, the owned App's config is set to `LogLevel="WARN"` before the native calls and the persisted warning's level must equal `WARN`, along with the original exact message and trusted plugin/call attribution. No new root, fixture, wire field or API.

The two resource hooks remain byte-identical. Candidate02's named product/test files supersede candidate01 only as the proposed integration inputs. They have not been compiled or run by this owner.
