package app

// Historical tables only. No legacy queue, journal, recovery or native host
// code executes against these tables in the standard manager. Existing user
// rows are preserved; this is not a data purge or a compatibility executor.
const legacyPluginArchiveSchema = `CREATE TABLE IF NOT EXISTS recognition_word_updates(run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,word_revision INTEGER NOT NULL,total_count INTEGER NOT NULL,remote_count INTEGER NOT NULL); CREATE TABLE IF NOT EXISTS recognition_words(id TEXT PRIMARY KEY CHECK(id='CustomIdentifiers'),revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL,updated_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS plugin_storage_schema(version INTEGER PRIMARY KEY CHECK(version>0));

CREATE TABLE IF NOT EXISTS plugin_run_origins(run_id TEXT PRIMARY KEY,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_event_admissions(run_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS plugin_run_config_transitions(run_id TEXT NOT NULL,from_revision INTEGER NOT NULL,to_revision INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(run_id,from_revision),UNIQUE(run_id,to_revision));
CREATE TABLE IF NOT EXISTS plugin_site_control_watchers(instance_id TEXT PRIMARY KEY,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,config BLOB NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_site_control_operations(run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,result BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_site_statistic_operations(run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,result BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_subscribe_group_operations(run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,result BLOB NOT NULL,FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS resource_metadata_settings(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,sealed BLOB NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS subscription_words(subscription_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_api_calls(run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,scope BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_installations(
 instance_id TEXT PRIMARY KEY,plugin_id TEXT NOT NULL,version TEXT NOT NULL,manifest_schema INTEGER NOT NULL,
 manifest BLOB NOT NULL,desired_enabled INTEGER NOT NULL DEFAULT 0,installed INTEGER NOT NULL DEFAULT 1,generation INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_values(
 instance_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('config','data')),item_key TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,kind,item_key),FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE TABLE IF NOT EXISTS plugin_migration_runs(
 id TEXT PRIMARY KEY,source_fingerprint TEXT NOT NULL,source_plugin_id TEXT NOT NULL,instance_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('planned','running','imported','failed')),error_code TEXT NOT NULL DEFAULT '',
 details BLOB NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(source_fingerprint,source_plugin_id,instance_id));
CREATE TABLE IF NOT EXISTS plugin_migration_receipts(
 source_fingerprint TEXT NOT NULL,source_plugin_id TEXT NOT NULL,instance_id TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,imported_at TEXT NOT NULL,
 PRIMARY KEY(source_fingerprint,source_plugin_id,instance_id),
 FOREIGN KEY(run_id) REFERENCES plugin_migration_runs(id));
CREATE TABLE IF NOT EXISTS plugin_runs(
 id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,version TEXT NOT NULL,generation INTEGER NOT NULL,
 config_revision INTEGER NOT NULL,action TEXT NOT NULL,state TEXT NOT NULL,
 input BLOB NOT NULL,result BLOB,error_code TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE INDEX IF NOT EXISTS plugin_runs_pending ON plugin_runs(state,created_at,id);
CREATE TABLE IF NOT EXISTS plugin_event_subscriptions(
 instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,
 event_types TEXT NOT NULL,delay_ms INTEGER NOT NULL,active INTEGER NOT NULL,cursor INTEGER NOT NULL,
 PRIMARY KEY(instance_id,handler_key),FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE TABLE IF NOT EXISTS plugin_event_batches(
 id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,generation INTEGER NOT NULL,
 config_revision INTEGER NOT NULL,due_at INTEGER NOT NULL,run_id TEXT UNIQUE,created_at TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'collecting',item_count INTEGER NOT NULL DEFAULT 0,
 FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE INDEX IF NOT EXISTS plugin_event_batches_due ON plugin_event_batches(run_id,due_at);
CREATE INDEX IF NOT EXISTS plugin_event_batches_owner ON plugin_event_batches(instance_id,handler_key,generation,config_revision,state,run_id);
CREATE TABLE IF NOT EXISTS plugin_event_deliveries(
 instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,event_id TEXT NOT NULL,event_seq INTEGER NOT NULL,batch_id TEXT NOT NULL,
 PRIMARY KEY(instance_id,handler_key,event_id),FOREIGN KEY(batch_id) REFERENCES plugin_event_batches(id));
CREATE INDEX IF NOT EXISTS plugin_event_deliveries_batch ON plugin_event_deliveries(batch_id,event_seq);
CREATE TABLE IF NOT EXISTS plugin_media_refresh_operations(
 run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,service_fingerprint TEXT NOT NULL,state TEXT NOT NULL,result BLOB,updated_at TEXT NOT NULL,
 FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS plugin_media_refresh_items(
 run_id TEXT NOT NULL,asset_id TEXT NOT NULL,digest BLOB NOT NULL,
 PRIMARY KEY(run_id,asset_id),FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS plugin_schedules(
 instance_id TEXT NOT NULL,service_id TEXT NOT NULL,group_key TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,
 definition BLOB NOT NULL,definition_digest TEXT NOT NULL,active INTEGER NOT NULL,next_due TEXT,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,service_id),FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE INDEX IF NOT EXISTS plugin_schedules_due ON plugin_schedules(active,next_due);
CREATE TABLE IF NOT EXISTS plugin_schedule_occurrences(
 id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,service_id TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,
 scheduled_at TEXT NOT NULL,state TEXT NOT NULL,run_id TEXT UNIQUE,misfire_grace_ms INTEGER NOT NULL,manual INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,
 UNIQUE(instance_id,service_id,generation,config_revision,scheduled_at,manual),
 FOREIGN KEY(instance_id,service_id) REFERENCES plugin_schedules(instance_id,service_id),FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE INDEX IF NOT EXISTS plugin_schedule_occurrences_runs ON plugin_schedule_occurrences(instance_id,service_id,state);
CREATE TABLE IF NOT EXISTS plugin_download_file_events(
 file_event_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
INSERT OR IGNORE INTO plugin_storage_schema VALUES(1);`
