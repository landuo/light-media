# Source collector 01 — frozen for static review, NOT RUN

Prepared after coordinator's successful standard uv install (`evidence/s10-source-dependencies01/result.json`, exit 0). The installation is dependency evidence, not source-method execution. Earlier `PLAN.md` / `DEPENDENCIES.md` / 32-input preparation files remain unchanged.

## Actual new environment

`source-runtime/s10-venv`, CPython 3.12.14; SQLAlchemy 2.0.50, aiosqlite 0.22.1, APScheduler 3.11.2, Pydantic 2.13.4, pydantic-core 2.46.4, annotated-types 0.8.0, greenlet 3.5.6, typing-extensions 4.16.0, typing-inspection 0.4.4, tzlocal 5.4.5. This is a **new pinned component environment, not the historical production or old query-oracle environment**. aiosqlite is installed but the collector does not import it or exercise an async connection.

`collector-inputs.sha256` binds 619 current files: 11 actual source files, the collector, venv configuration, resolved Python executable, and 605 installed package/metadata/native-extension files. Shell SHA readback passed for all 619 (`collector-input-readback.txt`); no Python syntax check, AST execution or package import was run to produce that result. Installed site-packages currently has no `.pyc` file. Runtime `-B` prevents new bytecode writes; import file hashes, including standard-library modules, are emitted by the collector if it is later run.

## Static import assessment and explicit side-effect boundaries

- Start with `-I -S -B`; add only the exact new installed site-packages directory, with no site/.pth processing. There is no source package import, fake sys.modules entry, custom import hook or module patch. No proxy/TLS/environment value is assigned or printed.
- SQLAlchemy's real util.concurrency imports asyncio and greenlet. Therefore standard-library socket may be loaded transitively, and the driver imports asyncio for original SystemConfigOper's lock. This is **not evidence that bind/connect happened**. No loop, asyncio runner, socket, connection probe or network operation is invoked by the driver. Do not falsely assert socket-module absence. New requests/urllib3 imports remain prohibited and are not in the installed environment/selected source imports; before/after checks record their absence.
- Source module import sections and global Engine/AsyncEngine startup are not executed. Real synchronous ORM definitions/decorators and DAO selected bodies run on one fresh owned SQLite file; no existing application DB/config is read. Class envelopes for Manager/Scheduler omit only ConfigReloadMixin, retaining the original singleton metaclasses and selected original method bodies. The original constructors/startup/monitor are not executed; owned state is explicit.
- Original ObjectUtils.check_method is compiled in a separate ordinary globals mapping containing stdlib inspect. Original ORM Base.update uses SQLAlchemy inspect. Neither body is patched; no fabricated module is installed.
- Pydantic's real main/fields and core implement the original Plugin class. Inspected default remains validate_assignment=False, with ordinary field setter writing unchanged values into `__dict__`. Existing metadata contains no Pydantic plugin entry point; no plugin-disabling environment variable is introduced.
- APScheduler import reads its own installed entry-point metadata. The driver creates an actual BackgroundScheduler with explicit timezone.utc and a fixed UTC DateTrigger. It never calls scheduler.start/shutdown, starts a thread, submits a callback, or instantiates external job stores. Pending-job add/get/remove are real APScheduler operations. Explicit timezone avoids invoking tzlocal's system-zone discovery path. An owned callback raises if unexpectedly executed.
- `dependency-static-observations.txt` records actual relevant package lines and a bounded source-text scan. No suspicious-pattern match is only a finite scan finding, **not proof of all possible Python behavior or a runtime network trace**. Native extensions are actual package binaries, byte-bound, not reimplemented or claimed source-audited internally.

## Actual proposed observations

1. Six Manager methods: owned callback/event traces; missing/enabled/disabled/init failure; class-only/running/close failure removal; ordinary standard-JSON state return and state exception; actual Plugin defaults/field assignment/page/auth-threshold/stable order. No real business plugins, EventManager host or site authentication.
2. Real source SQLite path: class/force gates, configuration/data deletion with unrelated rows preserved, already-missing rows, and a real owned SQLite delete-abort trigger. Capture original cache publication before SQL failure, rollback, and later readonly cold reopen in a separate coordinator process. The callback/DAO results are collected, not derived from Go.
3. Both original Scheduler algorithms: inactive/empty-pid no-op, explicit-ID removal irrespective of pid, physical `|` prefix matching, same-prefix service replacement, actual invalid-max_instances add failure leaving a registry entry, continuation, and pid-wide cleanup. Actual stopped APScheduler state is captured, but no timer firing/concurrency/worker claim. JobLookupError is the actual package class; its catch branch is not forced by a fake engine.

No nonempty sys.modules eviction is exercised: the driver checks that its exact owned prefix has no existing real entries and runs the original helper without mutation/patch. RSA/key-99 and missing real SitesHelper remain outside this finite observation. Threshold inputs are explicitly owned integration values, not production authentication.

Every selected original node is emitted with source path, exact line-byte SHA and AST SHA. The extraction preserves selected function bodies/decorators/defaults and selected class fields; it does not claim full modules or full source-host acceptance. Source files have no future-annotations import; compilation uses dont_inherit=True, not the driver's own future flag. SQLite/type observations use the actual source definitions, not reconstructed expected results.

## Proposed coordinator commands; no execution authorization implied

Run warm first and retain its original stdout/stderr/exit even if it fails. Only a successful warm with receipt permits cold. `capture01` must not already exist; the driver refuses overwrite. No hidden child Python process is launched.

```sh
/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/source-runtime/s10-venv/bin/python -I -S -B /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/audits/s10-source-preparation/capture_source_s10.py warm capture01
/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/source-runtime/s10-venv/bin/python -I -S -B /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/audits/s10-source-preparation/capture_source_s10.py cold capture01
```

Cold verifies warm database/driver/input-manifest hashes and opens SQLite mode=ro. The raw source outputs are not generated yet. No S10 method acceptance, migrated Go equivalence, SDK/CLI/browser or full-source environment PASS is awarded by this static freeze.
