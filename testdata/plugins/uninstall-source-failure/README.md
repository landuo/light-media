# Installed PluginUnInstall 2.2 dependency/fault reference

`fault-source-without-metadata.json` contains the full plugin methods with class metadata removed, and the actual installed Manager, Scheduler, SystemConfigOper, Base.update_config, database decorator and model method sources. Full file hashes identify the read-only installation; no original configuration or private icon URL is included.

`generate-fault-oracle.py` executes only these bounded AST methods with synthetic in-memory stores and newly owned temporary package directories. It never imports the original app, connects to a site, opens an original database, or removes an existing package. Config-model commit stubs model the reviewed actual db_update commit/rollback/rethrow boundary; this is not an original live SQLAlchemy run.

Run locally with Python for reference generation only; the delivered application is all Go. The generated `fault-oracle.json` covers success, close/stop exceptions caught by actual Manager, actual Scheduler backend get/remove exceptions caught, notification exceptions escaping at first/second target, separate installed/own config commit failures including cache-before-commit, and first/second scheduler-entry failures.

Actual native App HTTP/worker/SQLite/cold tests live in `internal/app/plugin_uninstall_source_failure_test.go`. All uninstall targets are owned synthetic instances. Historical before failures and final whole-source/focused SHA manifests are in the independent delivery handoff, not on a production service.

R2 adds the actual installed API install method and five synthetic AST cases in install-order-source.json / install-order-oracle.json. Only new or reinstalled IDs append; installed upgrades do not. Native local installation sequence is separate from pending original global installed-list/folder-layout migration.

R3 `cross-run-oracle.json` executes the same bounded installed init/Manager/
SystemConfig AST twice after an owned first installed-list commit failure.
It separates warm cache and cold durable list; the source set() skips a write
when its prior cache equals the remaining list and a durable row exists. The
warm second run thus leaves the old durable IDs intact; the cold second run
visits the removed IDs again. Original full-app startup/network synchronization
is not executed. `generate-cross-run-oracle.py` reruns these owned references.

`cross-run-before-summary.json` records actual R2 HTTP/SDK/cold/new-run FAIL and
an intermediate R3 same-cache-value oracle FAIL. The final source-global list
has independent durable state, revision-bound warm cache, and explicit source
row presence (absent versus explicit empty). It does not migrate the original
public installation order or folders.
