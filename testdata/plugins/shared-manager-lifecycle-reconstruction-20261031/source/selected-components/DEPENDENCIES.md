# Minimal dependency preparation request — not executed

Coordinator owns installation and source execution. Do not modify old venv, system proxy, package source/network route or installed application source. Stop on an explicit standard-tool denial; no alternate route/probe/hook.

| Exact top-level pin | Required actual component |
|---|---|
| `SQLAlchemy==2.0.50` | Original sync DB decorators, real ORM tables/query/delete/transaction and SQLite sessions. Source declares `~=2.0.50`. |
| `aiosqlite==0.22.1` | Coordinator-requested source SQLite dependency; source declares `~=0.22.1`. No asynchronous DB scenario or global AsyncEngine is needed for these eight methods. |
| `pydantic==2.13.4` | Actual BaseModel/Field used by original Plugin AST, including default construction and non-validating field assignment. Source lower bound 2.13.4. |
| `APScheduler==3.11.2` | Actual JobLookupError and unstarted BackgroundScheduler, if real scheduler state is captured. Source declares `~=3.11.2`. May be deferred if only explicitly labelled scheduler-adapter algorithm observations are authorized first. |

No requests, urllib3, FastAPI, HTTPX, full source requirements, site helpers, business plugins or PostgreSQL drivers are requested. Ordinary transitive dependencies must be resolved by the standard installer and frozen from their actual installed METADATA/RECORD; do not guess their versions now. Existing Pydantic 2.13.4 METADATA explicitly pins pydantic-core 2.46.4 and requires annotated-types >=0.6.0, typing-extensions >=4.14.1, typing-inspection >=0.4.2. No extras requested. Current old oracle metadata is only a static planning input, not a binding for the future venv.

## Proposed import / source-selection edges, not yet a closed audit

1. `pydantic.BaseModel` resolves to `.main`; `pydantic.Field` resolves to `.fields`; real pydantic-core and ordinary package internals support original `schemas.Plugin` class AST. No import of `app.schemas.__init__`.
2. SQLAlchemy `Column/String/JSON/Index/Integer/Sequence`, sync engine/session/ORM symbols, actual SQLite DBAPI support selected unchanged source `get_id_column`, `DbOper`, sync decorators and Base/ORM methods. Source AST definitions are evaluated in an explicit owned namespace with owned SQLite settings/session and log sink; source global Engine/AsyncEngine/config/logger module initialization is not imported. No full app.db import and no handwritten ORM/DAO pretending to be the original one.
3. Original Singleton/enum source nodes, `SystemConfigOper` selected sync methods, `PluginDataOper.del_data`, SystemConfig and PluginData ORM nodes form the finite persistence component. Seed and reopen only a new owned DB in this audit area; no existing application DB/config access.
4. APScheduler background scheduler, JobLookupError and default in-memory store/executor construction need a static review of the newly installed files before first import. Keep scheduler unstarted, use owned callbacks, no fired jobs or timer loop. Original Scheduler methods remain unchanged. Adapter-only execution is a separately labelled algorithm layer, not this real scheduler component.
5. Selected Manager methods and private helpers have original bodies, with explicit owned event/log/auth-level inputs, not fake `sys.modules` packages. No Manager start/constructor/monitor/import of helpers, source Scheduler constructor or SchedulerChain host startup. No RSA/private-key access or real SitesHelper implementation is claimed.

After standard installation, perform **static file review first**: METADATA/RECORD/version/hash, package entrypoints and concrete transitive import closure, import-time actions (including asyncio/timezone support). Reject any route that imports paused requests/urllib3 or executes a socket probe; do not suppress it with hooks. Standard-library socket appearing as a transitive import must not be equated with a bind/connect occurring; all selected imports still need coordinator approval. Never claim the not-yet-installed packages' closure is already safe or observed.

Next gate is a frozen unexecuted driver and independent static review. No install or capture command is supplied as executed evidence by this document.
