package app

import (
	"sort"
	"strings"
)

// recordColumn is a typed projection of one payload field. The value is copied
// from the canonical payload at write time with json_extract, so every index
// below reads a real column instead of scanning a JSON blob.
type recordColumn struct {
	name     string
	path     string
	affinity string
	// lit is used when path is empty: a raw SQL expression such as 'native'.
	lit string
}

// recordKeyLit marks a column filled from the record key instead of the payload.
// MoviePilot's key/value tables address rows by key, so the value is only known
// to the caller of Put.
const recordKeyLit = "\x00key"

func (c recordColumn) keyFromID() bool { return c.lit == recordKeyLit }

// insertValue projects the payload at INSERT time. A NOT NULL column cannot be
// repaired by an AFTER trigger, so the value has to be present in the statement.
func (c recordColumn) insertValue() string {
	if c.keyFromID() {
		return "?1"
	}
	if c.path == "" && c.lit == "" {
		return "NULL"
	}
	// Reuse the trigger projection so a non-JSON payload (sealed bytes) is
	// tolerated, then bind it to the INSERT parameter instead of NEW.payload.
	return strings.ReplaceAll(c.newValue(""), "NEW.payload", "?2")
}

func (c recordColumn) value() string {
	if c.keyFromID() {
		return "?1"
	}
	if c.path == "" {
		if c.lit == "" {
			return "NULL"
		}
		return c.lit
	}
	return "json_extract(?2,'" + c.path + "')"
}

// recordTable describes one feature table that replaces a slice of the old
// records(kind,id,payload) storage.
//
// mp marks a MoviePilot table: MoviePilot owns the integer `id` primary key, so
// light-media addresses rows through `uid` and keeps the payload extension
// column for the fields MoviePilot does not model.
type recordTable struct {
	name string
	mp   bool
	cols []recordColumn
	ddl  string
	idx  []string
}

const recordPayloadDDL = `payload JSON NOT NULL, updated_at TEXT NOT NULL, record_kind TEXT`

// mpTables are copied column for column from MoviePilot's SQLAlchemy models
// (app/db/models/*.py) and the generated SQLite schema. uid is the light-media
// key; payload keeps fields MoviePilot stores in other tables or not at all.
var mpTables = map[string]recordTable{
	"user": {name: "user", mp: true, ddl: `CREATE TABLE IF NOT EXISTS "user"(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL, email TEXT, hashed_password TEXT, is_active INTEGER DEFAULT 1,
 is_superuser INTEGER DEFAULT 0, avatar TEXT, is_otp INTEGER DEFAULT 0, otp_secret TEXT,
 permissions JSON DEFAULT '{}', settings JSON DEFAULT '{}', ` + recordPayloadDDL + `)`,
		idx: []string{`CREATE INDEX IF NOT EXISTS ix_user_name ON "user"(name)`}},
	"userconfig": {name: "userconfig", mp: true, ddl: `CREATE TABLE IF NOT EXISTS userconfig(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 username TEXT, "key" TEXT, value JSON, ` + recordPayloadDDL + `, UNIQUE(username,"key"))`},
	"systemconfig": {name: "systemconfig", mp: true, ddl: `CREATE TABLE IF NOT EXISTS systemconfig(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 "key" TEXT NOT NULL UNIQUE, value JSON, ` + recordPayloadDDL + `)`},
	"plugindata": {name: "plugindata", mp: true, ddl: `CREATE TABLE IF NOT EXISTS plugindata(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 plugin_id TEXT NOT NULL, "key" TEXT NOT NULL, value JSON, ` + recordPayloadDDL + `)`,
		idx: []string{`CREATE INDEX IF NOT EXISTS ix_plugindata_plugin_id_key ON plugindata(plugin_id,"key")`}},
	"site": {name: "site", mp: true, ddl: `CREATE TABLE IF NOT EXISTS site(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL, domain TEXT, url TEXT NOT NULL DEFAULT '', pri INTEGER DEFAULT 1,
 rss TEXT, cookie TEXT, ua TEXT, apikey TEXT, token TEXT, proxy INTEGER, filter TEXT,
 render INTEGER, public INTEGER, note JSON, limit_interval INTEGER DEFAULT 0,
 limit_count INTEGER DEFAULT 0, limit_seconds INTEGER DEFAULT 0, timeout INTEGER DEFAULT 15,
 is_active INTEGER DEFAULT 1, lst_mod_date TEXT, downloader TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{`CREATE INDEX IF NOT EXISTS ix_site_domain ON site(domain)`}},
	"siteicon": {name: "siteicon", mp: true, ddl: `CREATE TABLE IF NOT EXISTS siteicon(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL DEFAULT '', domain TEXT, url TEXT NOT NULL DEFAULT '', base64 TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{`CREATE INDEX IF NOT EXISTS ix_siteicon_domain ON siteicon(domain)`}},
	"sitestatistic": {name: "sitestatistic", mp: true, ddl: `CREATE TABLE IF NOT EXISTS sitestatistic(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 domain TEXT, success INTEGER DEFAULT 0, "fail" INTEGER DEFAULT 0, seconds INTEGER DEFAULT 0,
 lst_state INTEGER, lst_mod_date TEXT, note JSON, ` + recordPayloadDDL + `)`,
		idx: []string{`CREATE INDEX IF NOT EXISTS ix_sitestatistic_domain ON sitestatistic(domain)`}},
	"siteuserdata": {name: "siteuserdata", mp: true, ddl: `CREATE TABLE IF NOT EXISTS siteuserdata(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 domain TEXT, name TEXT, username TEXT, userid TEXT, user_level TEXT, join_at TEXT,
 bonus REAL DEFAULT 0, upload REAL DEFAULT 0, download REAL DEFAULT 0, ratio REAL DEFAULT 0,
 seeding REAL DEFAULT 0, leeching REAL DEFAULT 0, seeding_size REAL DEFAULT 0,
 leeching_size REAL DEFAULT 0, seeding_info JSON DEFAULT '{}', message_unread INTEGER DEFAULT 0,
 message_unread_contents JSON DEFAULT '[]', err_msg TEXT, updated_day TEXT, updated_time TEXT,
 ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_siteuserdata_updated_day_id ON siteuserdata(updated_day,id)`,
			`CREATE INDEX IF NOT EXISTS ix_siteuserdata_domain_updated_day_updated_time ON siteuserdata(domain,updated_day,updated_time)`,
		}},
	"message": {name: "message", mp: true, ddl: `CREATE TABLE IF NOT EXISTS message(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 channel TEXT, source TEXT, mtype TEXT, title TEXT, text TEXT, image TEXT, link TEXT,
 userid TEXT, reg_time TEXT, action INTEGER, note JSON, read INTEGER DEFAULT 0,
 ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_message_reg_time_id ON message(reg_time,id)`,
			`CREATE INDEX IF NOT EXISTS ix_message_read ON message(read)`,
			`CREATE INDEX IF NOT EXISTS notification_unread ON message(read)`,
			`CREATE INDEX IF NOT EXISTS notification_display_order ON message(updated_at DESC,id)`,
		}},
	"subscribe": {name: "subscribe", mp: true, ddl: `CREATE TABLE IF NOT EXISTS subscribe(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL DEFAULT '', year TEXT, type TEXT, keyword TEXT, tmdbid INTEGER,
 imdbid TEXT, tvdbid INTEGER, doubanid TEXT, bangumiid INTEGER, anilistid INTEGER,
 mediaid TEXT, media_source TEXT, media_id TEXT, season INTEGER, poster TEXT, backdrop TEXT,
 vote REAL, description TEXT, filter TEXT, include TEXT, exclude TEXT, quality TEXT,
 resolution TEXT, effect TEXT, total_episode INTEGER, start_episode INTEGER,
 lack_episode INTEGER, note JSON, state TEXT NOT NULL DEFAULT 'N', last_update TEXT,
 date TEXT, username TEXT, sites JSON DEFAULT '[]', downloader TEXT,
 best_version INTEGER DEFAULT 0, best_version_full INTEGER DEFAULT 0,
 current_priority INTEGER, episode_priority JSON DEFAULT '{}', save_path TEXT,
 search_imdbid INTEGER DEFAULT 0, manual_total_episode INTEGER DEFAULT 0,
 custom_words TEXT, media_category TEXT, filter_groups JSON DEFAULT '[]',
 episode_group TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_subscribe_doubanid ON subscribe(doubanid)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_state ON subscribe(state)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_tmdbid ON subscribe(tmdbid)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_media_source ON subscribe(media_source)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_type_date ON subscribe(type,date)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_mediaid ON subscribe(mediaid)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_bangumiid ON subscribe(bangumiid)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_anilistid ON subscribe(anilistid)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_media_id ON subscribe(media_id)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_media_identity ON subscribe(media_source,media_id)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_name ON subscribe(name)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_username ON subscribe(username)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribe_display ON subscribe(state,date,id)`, `CREATE INDEX IF NOT EXISTS subscription_media_key ON subscribe((json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id')))`,
		}},
	"subscribehistory": {name: "subscribehistory", mp: true, ddl: `CREATE TABLE IF NOT EXISTS subscribehistory(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL DEFAULT '', year TEXT, type TEXT, keyword TEXT, tmdbid INTEGER,
 imdbid TEXT, tvdbid INTEGER, doubanid TEXT, bangumiid INTEGER, anilistid INTEGER,
 mediaid TEXT, media_source TEXT, media_id TEXT, season INTEGER, poster TEXT, backdrop TEXT,
 vote REAL, description TEXT, filter TEXT, include TEXT, exclude TEXT, quality TEXT,
 resolution TEXT, effect TEXT, total_episode INTEGER, start_episode INTEGER, date TEXT,
 username TEXT, sites JSON DEFAULT '[]', best_version INTEGER DEFAULT 0,
 best_version_full INTEGER DEFAULT 0, episode_priority JSON DEFAULT '{}', save_path TEXT,
 search_imdbid INTEGER DEFAULT 0, custom_words TEXT, media_category TEXT,
 filter_groups JSON DEFAULT '[]', episode_group TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_subscribehistory_name ON subscribehistory(name)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribehistory_tmdbid ON subscribehistory(tmdbid)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribehistory_type_date ON subscribehistory(type,date)`,
			`CREATE INDEX IF NOT EXISTS ix_subscribehistory_media_identity ON subscribehistory(media_source,media_id)`,
		}},
	"downloadhistory": {name: "downloadhistory", mp: true, ddl: `CREATE TABLE IF NOT EXISTS downloadhistory(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 path TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
 year TEXT, tmdbid INTEGER, imdbid TEXT, tvdbid INTEGER, doubanid TEXT, bangumiid INTEGER,
 anilistid INTEGER, media_source TEXT, media_id TEXT, seasons TEXT, episodes TEXT, image TEXT,
 poster TEXT, downloader TEXT, download_hash TEXT, torrent_name TEXT, torrent_description TEXT,
 torrent_site TEXT, userid TEXT, username TEXT, channel TEXT, date TEXT, note JSON,
 media_category TEXT, episode_group TEXT, custom_words TEXT,
 state TEXT DEFAULT '', progress REAL DEFAULT 0, error TEXT, workflow_id TEXT,
 downloader_snapshot TEXT, material_ref TEXT, import_plan_id TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_path ON downloadhistory(path)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_tmdbid ON downloadhistory(tmdbid)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_bangumiid ON downloadhistory(bangumiid)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_anilistid ON downloadhistory(anilistid)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_media_source ON downloadhistory(media_source)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_media_id ON downloadhistory(media_id)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_media_identity ON downloadhistory(media_source,media_id)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_download_hash_date ON downloadhistory(download_hash,date)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_date_id ON downloadhistory(date,id)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_state ON downloadhistory(state)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadhistory_downloader ON downloadhistory(downloader,state)`, `CREATE INDEX IF NOT EXISTS intent_media_key ON downloadhistory((json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id'))) WHERE record_kind='intent'`,
		}},
	"downloadfiles": {name: "downloadfiles", mp: true, ddl: `CREATE TABLE IF NOT EXISTS downloadfiles(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 downloader TEXT, download_hash TEXT, fullpath TEXT, savepath TEXT, filepath TEXT,
 torrentname TEXT, state INTEGER NOT NULL DEFAULT 1, history_uid TEXT, asset_id TEXT,
 ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_downloadfiles_fullpath_id ON downloadfiles(fullpath,id)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadfiles_download_hash_state ON downloadfiles(download_hash,state)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadfiles_savepath ON downloadfiles(savepath)`,
		}},
	"downloadfailure": {name: "downloadfailure", mp: true, ddl: `CREATE TABLE IF NOT EXISTS downloadfailure(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 fingerprint TEXT NOT NULL UNIQUE, type TEXT, title TEXT, year TEXT, tmdbid INTEGER,
 doubanid TEXT, bangumiid INTEGER, anilistid INTEGER, media_source TEXT, media_id TEXT,
 seasons TEXT, episodes TEXT, site INTEGER, site_name TEXT, torrent_id TEXT, torrent_name TEXT,
 torrent_size REAL, downloader TEXT, source TEXT, error_message TEXT, retry_count INTEGER DEFAULT 0,
 first_failed_at TEXT, last_failed_at TEXT, next_retry_at TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_downloadfailure_next_retry_at ON downloadfailure(next_retry_at)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadfailure_media_site ON downloadfailure(type,tmdbid,doubanid,site)`,
			`CREATE INDEX IF NOT EXISTS ix_downloadfailure_media_identity_site ON downloadfailure(type,media_source,media_id,site)`,
		}},
	"mediaserveritem": {name: "mediaserveritem", mp: true, ddl: `CREATE TABLE IF NOT EXISTS mediaserveritem(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 server TEXT, library TEXT, item_id TEXT, item_type TEXT, title TEXT, original_title TEXT,
 year TEXT, tmdbid INTEGER, imdbid TEXT, tvdbid INTEGER, path TEXT, seasoninfo JSON DEFAULT '{}',
 note JSON, lst_mod_date TEXT, ` + recordPayloadDDL + `, UNIQUE(server,item_id))`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_mediaserveritem_item_id ON mediaserveritem(item_id)`,
			`CREATE INDEX IF NOT EXISTS ix_mediaserveritem_title ON mediaserveritem(title)`,
			`CREATE INDEX IF NOT EXISTS ix_mediaserveritem_tmdbid_item_type ON mediaserveritem(tmdbid,item_type)`,
		}},
	"workflow": {name: "workflow", mp: true, ddl: `CREATE TABLE IF NOT EXISTS workflow(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL DEFAULT '', description TEXT, timer TEXT, trigger_type TEXT DEFAULT 'timer',
 event_type TEXT, event_conditions JSON DEFAULT '{}', state TEXT NOT NULL DEFAULT 'W',
 current_action TEXT, result TEXT, run_count INTEGER DEFAULT 0, actions JSON,
 flows JSON, context JSON DEFAULT '{}', execution_config JSON DEFAULT '{}',
 execution_state JSON DEFAULT '{}', add_time TEXT, last_time TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_workflow_state ON workflow(state)`,
			`CREATE INDEX IF NOT EXISTS ix_workflow_name ON workflow(name)`,
		}},
	"agentchat": {name: "agentchat", mp: true, ddl: `CREATE TABLE IF NOT EXISTS agentchat(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 session_id TEXT NOT NULL DEFAULT '', client_session_id TEXT, user_id TEXT, username TEXT,
 channel TEXT, source TEXT, original_chat_id TEXT, title TEXT, preview TEXT,
 agent_messages JSON, display_messages JSON, message_count INTEGER DEFAULT 0,
 created_at TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_agentchat_session_user ON agentchat(session_id,user_id)`,
			`CREATE INDEX IF NOT EXISTS ix_agentchat_user_updated ON agentchat(user_id,updated_at,id)`,
			`CREATE INDEX IF NOT EXISTS ix_agentchat_channel_updated ON agentchat(channel,updated_at,id)`,
		}},
	"agenttask": {name: "agenttask", mp: true, ddl: `CREATE TABLE IF NOT EXISTS agenttask(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '', trigger_type TEXT NOT NULL DEFAULT '',
 cron_expression TEXT, run_at TEXT, enabled INTEGER NOT NULL DEFAULT 1, user_id TEXT NOT NULL DEFAULT '',
 username TEXT, session_id TEXT NOT NULL DEFAULT '', channel TEXT, source TEXT,
 original_chat_id TEXT, last_status TEXT NOT NULL DEFAULT 'waiting', last_run_at TEXT,
 last_result TEXT, run_count INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '',
 ` + recordPayloadDDL + `)`,
		idx: []string{
			`CREATE INDEX IF NOT EXISTS ix_agenttask_enabled ON agenttask(enabled)`,
			`CREATE INDEX IF NOT EXISTS ix_agenttask_user_created ON agenttask(user_id,created_at,id)`,
		}},
	"passkey": {name: "passkey", mp: true, ddl: `CREATE TABLE IF NOT EXISTS passkey(
 id INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT NOT NULL UNIQUE,
 user_id INTEGER NOT NULL DEFAULT 1, credential_id TEXT NOT NULL UNIQUE,
 public_key TEXT NOT NULL DEFAULT '', sign_count INTEGER DEFAULT 0, name TEXT,
 aaguid TEXT, created_at TEXT, last_used_at TEXT, is_active INTEGER DEFAULT 1,
 transports TEXT, ` + recordPayloadDDL + `)`,
		idx: []string{`CREATE INDEX IF NOT EXISTS ix_passkey_user_id ON passkey(user_id)`}},
}

// nativeTables are the light-media workflow tables. MoviePilot has no model for
// these, so they follow the same rule: one feature per table, typed columns for
// every indexed path, payload kept as the canonical record.
var nativeTables = []recordTable{
	{name: "site_state", cols: []recordColumn{{"site_id", "$.id", "TEXT", ""}, {"status", "$.status", "TEXT", ""}, {"checked_at", "$.checked_at", "TEXT", ""}, {"cooldown_until", "$.until", "INTEGER", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS site_state_status ON site_state(status,checked_at)`}},
	{name: "site_statistic_event", cols: []recordColumn{{"site_id", "$.site_id", "TEXT", ""}, {"ts", "$.ts", "INTEGER", ""}, {"kind", "$.kind", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS site_statistic_event_site ON site_statistic_event(site_id,ts)`}},
	{name: "site_message_sync", cols: []recordColumn{{"site_id", "$.site_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"collected_at", "$.collected_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS site_message_sync_site ON site_message_sync(site_id,state)`}},
	{name: "site_system_error", cols: []recordColumn{{"site_id", "$.site_id", "TEXT", ""}, {"ts", "$.ts", "INTEGER", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS site_system_error_site ON site_system_error(site_id,ts)`}},
	{name: "scheduler_state"},
	{name: "subscription_event", cols: []recordColumn{{"subscription_id", "$.subscription_id", "TEXT", ""}, {"ts", "$.ts", "INTEGER", ""}, {"event", "$.event", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS subscription_event_sub ON subscription_event(subscription_id,ts)`}},
	{name: "subscription_acquisition", cols: []recordColumn{{"subscription_id", "$.subscription_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"media_key", "$.media_key", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS subscription_acquisition_state ON subscription_acquisition(state)`}},
	{name: "subscription_receipt", cols: []recordColumn{{"subscription_id", "$.subscription_id", "TEXT", ""}, {"stage", "$.stage", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"plan_id", "$.plan_id", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS subscription_receipt_stage ON subscription_receipt(stage,state)`}},
	{name: "subscription_origin", cols: []recordColumn{{"subscription_id", "$.subscription_id", "TEXT", ""}}},
	{name: "download_preview", cols: []recordColumn{{"hash", "$.hash", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"expires_at", "$.expires_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS download_preview_expires ON download_preview(expires_at)`, `CREATE INDEX IF NOT EXISTS download_preview_hash ON download_preview(hash)`}},
	{name: "download_selection", cols: []recordColumn{{"intent_id", "$.intent_id", "TEXT", ""}, {"scope_type", "$.scope_type", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS download_selection_intent ON download_selection(intent_id)`}},
	{name: "material", cols: []recordColumn{{"ref", "$.ref", "TEXT", ""}, {"hash", "$.hash", "TEXT", ""}, {"expires_at", "$.expires_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS material_expires ON material(expires_at)`, `CREATE INDEX IF NOT EXISTS material_hash ON material(hash)`}},
	{name: "material_blob"},
	{name: "media_exclusion", cols: []recordColumn{{"media_key", "$.media_key", "TEXT", ""}, {"blocked", "$.blocked", "INTEGER", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS media_exclusion_key ON media_exclusion(media_key)`}},
	{name: "import_plan", cols: []recordColumn{{"state", "$.state", "TEXT", ""}, {"intent_id", "$.intent_id", "TEXT", ""}, {"media_key", "$.media_key", "TEXT", ""}, {"created_at", "$.created_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS import_plan_state ON import_plan(state)`, `CREATE INDEX IF NOT EXISTS import_plan_intent ON import_plan(intent_id)`, `CREATE INDEX IF NOT EXISTS import_plan_created ON import_plan(created_at,id)`}},
	{name: "import_plan_file", cols: []recordColumn{{"plan_id", "$.plan_id", "TEXT", ""}, {"status", "$.status", "TEXT", ""}, {"source", "$.source", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS import_plan_file_plan ON import_plan_file(plan_id,status)`, `CREATE INDEX IF NOT EXISTS import_plan_file_source ON import_plan_file(source)`}},
	{name: "import_receipt", cols: []recordColumn{{"target", "$.target", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"plan_id", "$.plan_id", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS import_receipt_target ON import_receipt(target)`, `CREATE INDEX IF NOT EXISTS import_receipt_state ON import_receipt(state)`, `CREATE INDEX IF NOT EXISTS import_receipt_plan ON import_receipt(plan_id)`}},
	{name: "import_receipt_member", cols: []recordColumn{{"receipt_id", "$.receipt_id", "TEXT", ""}, {"asset_id", "$.asset_id", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS import_receipt_member_asset ON import_receipt_member(asset_id)`}},
	{name: "import_meta", cols: []recordColumn{{"plan_id", "$.plan_id", "TEXT", ""}, {"committed_at", "$.committed_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS import_meta_plan ON import_meta(plan_id)`}},
	{name: "managed_file", cols: []recordColumn{{"asset_id", "$.asset_id", "TEXT", ""}, {"path", "$.path", "TEXT", ""}, {"root_id", "$.root_id", "TEXT", ""}, {"sha256", "$.sha256", "TEXT", ""}, {"media_key", "$.media_key", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS managed_file_asset ON managed_file(asset_id)`, `CREATE INDEX IF NOT EXISTS managed_file_path ON managed_file(path)`, `CREATE INDEX IF NOT EXISTS managed_file_sha ON managed_file(sha256)`}},
	{name: "library_asset", cols: []recordColumn{{"path", "$.path", "TEXT", ""}, {"root_id", "$.root_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"media_key", "$.media_key", "TEXT", ""}, {"fnos_guid", "$.fnos_guid", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS library_asset_path ON library_asset(path)`, `CREATE INDEX IF NOT EXISTS asset_path ON library_asset(path)`, `CREATE INDEX IF NOT EXISTS asset_media_key ON library_asset((json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id')))`, `CREATE INDEX IF NOT EXISTS library_asset_root ON library_asset(root_id,state)`, `CREATE INDEX IF NOT EXISTS library_asset_media ON library_asset(media_key)`}},
	{name: "library_asset_proof", cols: []recordColumn{{"asset_id", "$.asset_id", "TEXT", ""}, {"path", "$.path", "TEXT", ""}}},
	{name: "library_asset_presence", cols: []recordColumn{{"asset_id", "$.asset_id", "TEXT", ""}, {"present", "$.present", "INTEGER", ""}}},
	{name: "library_candidate", cols: []recordColumn{{"root_id", "$.root_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"path", "$.path", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS library_candidate_root ON library_candidate(root_id,state)`, `CREATE INDEX IF NOT EXISTS library_candidate_path ON library_candidate(path)`}},
	{name: "library_candidate_current", cols: []recordColumn{{"root_id", "$.root_id", "TEXT", ""}}},
	{name: "library_scan", cols: []recordColumn{{"root_id", "$.root_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}}},
	{name: "removal_preview", cols: []recordColumn{{"asset_id", "$.asset_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"expires_at", "$.expires_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS removal_preview_asset ON removal_preview(asset_id,state)`, `CREATE INDEX IF NOT EXISTS removal_preview_expires ON removal_preview(expires_at)`}},
	{name: "organize_queue_member", cols: []recordColumn{{"group_id", "$.group", "TEXT", ""}, {"job_id", "$.job", "TEXT", ""}, {"file_id", "$.file", "TEXT", ""}, {"source", "$.source", "TEXT", ""}, {"meta", "$.meta", "TEXT", ""}, {"media_key", "$.media", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS organize_queue_group ON organize_queue_member(group_id)`, `CREATE INDEX IF NOT EXISTS organize_queue_job ON organize_queue_member(job_id)`, `CREATE INDEX IF NOT EXISTS organize_queue_source ON organize_queue_member(source)`, `CREATE INDEX IF NOT EXISTS organize_queue_meta ON organize_queue_member(meta,group_id)`, `CREATE INDEX IF NOT EXISTS organize_queue_media ON organize_queue_member(media_key,group_id)`}},
	{name: "organize_listing", cols: []recordColumn{{"job_id", "$.job_id", "TEXT", ""}}},
	{name: "organize_cancel", cols: []recordColumn{{"job_id", "$.job_id", "TEXT", ""}}},
	{name: "directory_file", cols: []recordColumn{{"directory_id", "$.directory_id", "TEXT", ""}, {"path", "$.path", "TEXT", ""}, {"state", "$.state", "TEXT", ""}, {"last_seen", "$.last_seen", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS directory_file_dir ON directory_file(directory_id,last_seen)`, `CREATE INDEX IF NOT EXISTS directory_file_state_idx ON directory_file(state)`}},
	{name: "directory_file_state", cols: []recordColumn{{"directory_id", "$.directory_id", "TEXT", ""}}},
	{name: "directory_import", cols: []recordColumn{{"directory_id", "$.directory_id", "TEXT", ""}, {"state", "$.state", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS directory_import_state ON directory_import(state)`}},
	{name: "directory_status", cols: []recordColumn{{"status", "$.status", "TEXT", ""}, {"checked_at", "$.checked_at", "TEXT", ""}}},
	{name: "directory_overwrite", cols: []recordColumn{{"directory_id", "$.directory_id", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS directory_overwrite_dir ON directory_overwrite(directory_id)`}},
	{name: "file_operation", cols: []recordColumn{{"state", "$.state", "TEXT", ""}, {"created_at", "$.created_at", "TEXT", ""}}},
	{name: "history_receipt", cols: []recordColumn{{"history_id", "$.history_id", "TEXT", ""}, {"created_at", "$.created_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS history_receipt_id ON history_receipt(history_id)`}},
	{name: "history_target_receipt", cols: []recordColumn{{"history_id", "$.history_id", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS history_target_receipt_id ON history_target_receipt(history_id)`}},
	{name: "history_parent_cleanup", cols: []recordColumn{{"history_id", "$.history_id", "TEXT", ""}}},
	{name: "history_hidden", cols: []recordColumn{{"history_id", "$.history_id", "TEXT", ""}}},
	{name: "history_cleanup", cols: []recordColumn{{"state", "$.state", "TEXT", ""}}},
	{name: "download_file_event", cols: []recordColumn{{"event_id", "$.event_id", "TEXT", ""}, {"ts", "$.ts", "INTEGER", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS download_file_event_id ON download_file_event(event_id)`}},
	{name: "download_removal_receipt", cols: []recordColumn{{"event_id", "$.event_id", "TEXT", ""}, {"level", "$.level", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS download_removal_receipt_event ON download_removal_receipt(event_id)`}},
	{name: "download_removal_root", cols: []recordColumn{{"event_id", "$.event_id", "TEXT", ""}}},
	{name: "download_removal_chain", cols: []recordColumn{{"event_id", "$.event_id", "TEXT", ""}}},
	{name: "download_context", cols: []recordColumn{{"intent_id", "$.intent_id", "TEXT", ""}}},
	{name: "download_post_add", cols: []recordColumn{{"intent_id", "$.intent_id", "TEXT", ""}}},
	{name: "plugin_original_callback", cols: []recordColumn{{"state", "$.state", "TEXT", ""}, {"job_id", "$.job_id", "TEXT", ""}}},
	{name: "plugin_original_failure", cols: []recordColumn{{"source", "$.source", "TEXT", ""}, {"hash", "$.hash", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS plugin_original_failure_source ON plugin_original_failure(source,hash)`}},
	{name: "plugin_original_batch", cols: []recordColumn{{"state", "$.state", "TEXT", ""}}},
	{name: "plugin_original_directory", cols: []recordColumn{{"state", "$.state", "TEXT", ""}}},
	{name: "plugin_host_state", cols: []recordColumn{{"instance_id", "$.instance_id", "TEXT", ""}, {"state_key", "$.key", "TEXT", ""}, {"expires_at", "$.expires_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS plugin_host_state_expires ON plugin_host_state(expires_at)`}},
	{name: "settings_lifecycle", cols: []recordColumn{{"key", "$.key", "TEXT", ""}}},
	{name: "audit_log", cols: []recordColumn{{"ts", "$.time", "TEXT", ""}, {"workflow_id", "$.workflow_id", "TEXT", ""}, {"event", "$.event", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS audit_log_ts ON audit_log(ts)`, `CREATE INDEX IF NOT EXISTS audit_log_workflow ON audit_log(workflow_id)`}},
	{name: "decision_log", cols: []recordColumn{{"workflow_id", "$.workflow_id", "TEXT", ""}, {"media_key", "$.media_key", "TEXT", ""}}},
	{name: "cache_entry", cols: []recordColumn{{"namespace", "$.namespace", "TEXT", ""}, {"expires_at", "$.expires_at", "TEXT", ""}},
		idx: []string{`CREATE INDEX IF NOT EXISTS cache_entry_expires ON cache_entry(expires_at)`, `CREATE INDEX IF NOT EXISTS cache_entry_namespace ON cache_entry(namespace)`}},
	{name: "workflow_lock", cols: []recordColumn{{"media_key", "$.media_key", "TEXT", ""}, {"created_at", "$.created_at", "TEXT", ""}}},
}

// recordKinds maps every storage kind to its feature table. The keys are the
// historical records.kind values: existing SQL, plugin host calls and jobs keep
// using them, only the physical table changes.
var recordKinds = map[string]string{
	// Site and source domain.
	"site_connection":                "site_state",
	"site_cooldown":                  "site_state",
	"site_icon":                      "siteicon",
	"site_stats_latest":              "sitestatistic",
	"site_stats_history":             "site_statistic_event",
	"site_access_statistics":         "site_statistic_event",
	"site_message_collection":        "site_message_sync",
	"site_message_history_execution": "site_message_sync",
	"site_system_error":              "site_system_error",
	"site_refresh_clock":             "scheduler_state",
	"source_file_extension_settings": "systemconfig",
	"source_statistics_settings":     "systemconfig",
	"source_settings_migration":      "systemconfig",
	"source_site_message_settings":   "systemconfig",
	"source_network_settings":        "systemconfig",
	"source_runtime_settings":        "systemconfig",

	// Subscription domain.
	"subscription":       "subscribe",
	"subscription_added": "subscription_event",

	// Download domain.
	"intent":                     "downloadhistory",
	"native_download":            "downloadhistory",
	"download_added":             "downloadhistory",
	"download_review":            "download_preview",
	"download_preview":           "download_preview",
	"download_season_scope":      "download_selection",
	"download_episode_selection": "download_selection",
	"download_context":           "download_context",
	"download_post_add":          "download_post_add",
	"resource":                   "cache_entry",
	"material_info":              "material",
	"material_blob":              "material_blob",
	"exclusion":                  "media_exclusion",

	// Import and library domain.
	"asset":                     "library_asset",
	"asset_identity":            "library_asset_proof",
	"asset_presence":            "library_asset_presence",
	"managed_file":              "managed_file",
	"import_plan":               "import_plan",
	"import_receipt":            "import_receipt",
	"import_meta":               "import_meta",
	"library_candidate":         "library_candidate",
	"library_candidate_current": "library_candidate_current",
	"library_scan":              "library_scan",
	"removal_preview":           "removal_preview",

	// Organize queue and directory monitoring.
	"organize_queue_member": "organize_queue_member",
	"organize_file_listing": "organize_listing",
	"organize_file_cancel":  "organize_cancel",
	"directory_file":        "directory_file",
	"directory_file_state":  "directory_file_state",
	"directory_import":      "directory_import",
	"directory_status":      "directory_status",
	"directory_overwrite":   "directory_overwrite",
	"file_operation":        "file_operation",

	// History receipts.
	"history_source_removal":      "history_receipt",
	"history_target_removal":      "history_target_receipt",
	"history_parent_cleanup":      "history_parent_cleanup",
	"transfer_history_hidden":     "history_hidden",
	"reorganize_cleanup":          "history_cleanup",
	"history_download_file_event": "download_file_event",
	"core_download_removal":       "download_removal_receipt",
	"core_download_removal_root":  "download_removal_root",
	"core_download_removal_chain": "download_removal_chain",

	// Plugin compatibility state.
	"original_transfer_callback":        "plugin_original_callback",
	"original_transfer_task_failures":   "plugin_original_failure",
	"original_scrape_batch":             "plugin_original_batch",
	"original_metadata_directory":       "plugin_original_directory",
	"plugin_host_tmdb_cache":            "plugin_host_state",
	"plugin_host_media_recognize_count": "plugin_host_state",
	"plugin_host_setting_presence":      "plugin_host_state",
	"core_settings_lifecycle":           "settings_lifecycle",

	// MoviePilot tables written directly by the settings/auth paths.
	"user": "user",
	"site": "site",

	// Notifications, settings and cache.
	"notification":             "message",
	"message":                  "message",
	"audit":                    "audit_log",
	"decision":                 "decision_log",
	"config":                   "systemconfig",
	"control":                  "systemconfig",
	"cache":                    "cache_entry",
	"ui_preferences":           "userconfig",
	"task_data_schema_version": "systemconfig",
	"workflow":                 "workflow_lock",
}

// recordKindAlias keeps the subscription receipt stages apart inside one table.
var recordKindAlias = map[string]string{}

func recordTableFor(kind string) (recordTable, bool) {
	name, ok := recordKinds[kind]
	if !ok {
		return recordTable{}, false
	}
	if t, ok := mpTables[name]; ok {
		t.cols = mpCols[name]
		return t, true
	}
	for _, t := range nativeTables {
		if t.name == name {
			return t, true
		}
	}
	return recordTable{}, false
}

// recordKeyColumn is the column light-media addresses a row by. MoviePilot
// tables own the integer id, so their external key lives in uid.
func recordKeyColumn(t recordTable) string {
	if t.mp {
		return "uid"
	}
	return "id"
}

func recordInsertSQL(kind string, t recordTable) string { return recordInsertSQLMode(kind, t, true) }

// recordInsertSQLNoop keeps the first writer for immutable facts.
func recordInsertSQLNoop(kind string, t recordTable) string {
	return recordInsertSQLMode(kind, t, false)
}

func recordInsertSQLMode(kind string, t recordTable, upsert bool) string {
	key := recordKeyColumn(t)
	cols := []string{key, "payload", "updated_at", "record_kind"}
	vals := []string{"?1", "?2", "?3", "'" + kind + "'"}
	updates := []string{"payload=excluded.payload", "updated_at=excluded.updated_at", "record_kind=excluded.record_kind"}
	if stage, ok := recordKindAlias[kind]; ok {
		cols = append(cols, "stage")
		vals = append(vals, "'"+stage+"'")
		updates = append(updates, "stage=excluded.stage")
	}
	for _, c := range t.cols {
		if c.name == "stage" {
			continue
		}
		cols = append(cols, c.name)
		vals = append(vals, c.insertValue())
		updates = append(updates, c.name+"=excluded."+c.name)
	}
	conflict := " ON CONFLICT(\"" + key + "\") DO NOTHING"
	if upsert {
		conflict = " ON CONFLICT(\"" + key + "\") DO UPDATE SET " + strings.Join(updates, ",")
	}
	return "INSERT INTO \"" + t.name + "\"(" + strings.Join(cols, ",") + ") VALUES(" +
		strings.Join(vals, ",") + ")" + conflict
}

// recordProjection builds the trigger body that copies payload fields into the
// typed columns. Triggers keep MoviePilot's real columns correct for every
// write path, including payload-only UPDATEs.
func recordProjection(t recordTable) (string, bool) {
	key := recordKeyColumn(t)
	var sets []string
	for _, c := range t.cols {
		if c.name == "stage" || c.keyFromID() {
			continue
		}
		sets = append(sets, c.name+"="+c.newValue(key))
	}
	if len(sets) == 0 {
		return "", false
	}
	return strings.Join(sets, ","), true
}

func (c recordColumn) newValue(key string) string {
	if c.path != "" {
		// Sealed plugin values are stored as raw bytes in payload, so every
		// projection has to survive a non-JSON payload instead of aborting the
		// write with "malformed JSON".
		return "CASE WHEN json_valid(NEW.payload) THEN json_extract(NEW.payload,'" + c.path + "') END"
	}
	if c.lit == "" {
		return "NULL"
	}
	if strings.Contains(c.lit, "json_extract(NEW.payload") {
		return "COALESCE(CASE WHEN json_valid(NEW.payload) THEN " + c.lit + " END,'')"
	}
	return c.lit
}

// pluginRecordDDL is the compatibility namespace for the sealed values that
// plugins store through the host. They are not light-media features, so they
// keep the old (kind,id) shape instead of inventing a domain table.
const pluginRecordDDL = `CREATE TABLE IF NOT EXISTS plugin_record(
 kind TEXT NOT NULL, id TEXT NOT NULL, payload JSON NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY(kind,id));
CREATE INDEX IF NOT EXISTS plugin_record_updated ON plugin_record(kind,updated_at DESC,id);
`

func recordDDL() string {
	var b strings.Builder
	names := make([]string, 0, len(mpTables))
	for name := range mpTables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := mpTables[name]
		t.cols = mpCols[name]
		b.WriteString(t.ddl)
		b.WriteString(";\n")
		for _, idx := range t.idx {
			b.WriteString(idx)
			b.WriteString(";\n")
		}
		b.WriteString(recordTriggerDDL(t))
	}
	for _, t := range nativeTables {
		cols := make([]string, 0, len(t.cols))
		for _, c := range t.cols {
			cols = append(cols, c.name+" "+c.affinity)
		}
		b.WriteString("CREATE TABLE IF NOT EXISTS " + t.name +
			"(id TEXT PRIMARY KEY,payload JSON NOT NULL,updated_at TEXT NOT NULL,record_kind TEXT")
		if len(cols) > 0 {
			b.WriteString("," + strings.Join(cols, ","))
		}
		b.WriteString(");\n")
		for _, idx := range t.idx {
			b.WriteString(idx)
			b.WriteString(";\n")
		}
		b.WriteString(recordTriggerDDL(t))
	}
	b.WriteString(pluginRecordDDL)
	b.WriteString(";\n")
	return b.String()
}

func recordTriggerDDL(t recordTable) string {
	projection, ok := recordProjection(t)
	if !ok {
		return ""
	}
	key := recordKeyColumn(t)
	var b strings.Builder
	b.WriteString("CREATE TRIGGER IF NOT EXISTS " + t.name + "_project_insert AFTER INSERT ON " + t.name +
		" BEGIN UPDATE " + t.name + " SET " + projection + " WHERE " + key + "=NEW." + key + "; END;\n")
	b.WriteString("CREATE TRIGGER IF NOT EXISTS " + t.name + "_project_update AFTER UPDATE OF payload ON " + t.name +
		" BEGIN UPDATE " + t.name + " SET " + projection + " WHERE " + key + "=NEW." + key + "; END;\n")
	return b.String()
}

// mpCols projects the light-media payload onto MoviePilot's columns. Every path
// below is read from the stored payload at write time, so a MoviePilot table can
// be queried and indexed with plain SQL while the canonical record stays whole.
var mpCols = map[string][]recordColumn{
	"user": {
		{"name", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.username'),'')"}, {"email", "$.email", "TEXT", ""},
		{"hashed_password", "", "TEXT", "json_extract(NEW.payload,'$.password_hash')"},
		{"is_active", "", "INTEGER", "1"}, {"is_superuser", "", "INTEGER", "0"},
		{"permissions", "$.permissions", "JSON", ""}, {"settings", "$.settings", "JSON", ""},
	},
	"userconfig": {
		{"username", "", "TEXT", "COALESCE(NULLIF(json_extract(NEW.payload,'$.username'),''),'admin')"},
		{"key", "", "TEXT", recordKeyLit}, {"value", "$.value", "JSON", ""},
	},
	"systemconfig": {{"key", "", "TEXT", recordKeyLit}, {"value", "$.value", "JSON", ""}},
	"plugindata": {
		{"plugin_id", "$.plugin_id", "TEXT", ""}, {"key", "$.key", "TEXT", ""},
		{"value", "$.value", "JSON", ""},
	},
	"site": {
		{"name", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.name'),'')"},
		{"domain", "$.domain", "TEXT", ""},
		{"url", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.base_url'),'')"}, {"pri", "$.pri", "INTEGER", ""},
		{"rss", "$.rss", "TEXT", ""}, {"cookie", "$.cookie", "TEXT", ""},
		{"ua", "$.ua", "TEXT", ""}, {"apikey", "$.api_key", "TEXT", ""},
		{"token", "$.token", "TEXT", ""}, {"proxy", "$.proxy", "INTEGER", ""},
		{"filter", "$.filter", "TEXT", ""}, {"render", "$.render", "INTEGER", ""},
		{"public", "$.public", "INTEGER", ""}, {"note", "$.note", "JSON", ""},
		{"limit_interval", "$.limit_interval", "INTEGER", ""},
		{"limit_count", "$.limit_count", "INTEGER", ""},
		{"limit_seconds", "$.limit_seconds", "INTEGER", ""},
		{"timeout", "$.timeout", "INTEGER", ""}, {"is_active", "$.enabled", "INTEGER", ""},
		{"lst_mod_date", "$.updated_at", "TEXT", ""}, {"downloader", "$.downloader", "TEXT", ""},
	},
	"siteicon": {
		{"name", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.name'),'')"},
		{"domain", "$.domain", "TEXT", ""},
		{"url", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.url'),'')"},
	},
	"sitestatistic": {
		{"domain", "$.domain", "TEXT", ""}, {"success", "$.success", "INTEGER", ""},
		{"fail", "$.fail", "INTEGER", ""}, {"seconds", "$.seconds", "INTEGER", ""},
		{"lst_state", "$.lst_state", "INTEGER", ""}, {"lst_mod_date", "$.lst_mod_date", "TEXT", ""},
		{"note", "$.note", "JSON", ""},
	},
	"siteuserdata": {
		{"domain", "$.domain", "TEXT", ""}, {"name", "$.name", "TEXT", ""},
		{"username", "$.username", "TEXT", ""}, {"userid", "$.userid", "TEXT", ""},
		{"user_level", "$.user_level", "TEXT", ""}, {"join_at", "$.join_at", "TEXT", ""},
		{"bonus", "$.bonus", "REAL", ""}, {"upload", "$.upload", "REAL", ""},
		{"download", "$.download", "REAL", ""}, {"ratio", "$.ratio", "REAL", ""},
		{"seeding", "$.seeding", "REAL", ""}, {"leeching", "$.leeching", "REAL", ""},
		{"seeding_size", "$.seeding_size", "REAL", ""}, {"leeching_size", "$.leeching_size", "REAL", ""},
		{"message_unread", "$.message_unread", "INTEGER", ""},
		{"message_unread_contents", "$.message_unread_contents", "JSON", ""},
		{"err_msg", "$.err_msg", "TEXT", ""}, {"updated_day", "$.updated_day", "TEXT", ""},
		{"updated_time", "$.updated_time", "TEXT", ""},
	},
	"message": {
		{"channel", "$.channel", "TEXT", ""}, {"source", "$.source", "TEXT", ""},
		{"mtype", "$.mtype", "TEXT", ""}, {"title", "$.title", "TEXT", ""},
		{"text", "$.message", "TEXT", ""}, {"image", "$.image", "TEXT", ""},
		{"link", "$.link", "TEXT", ""}, {"userid", "", "TEXT", "json_extract(NEW.payload,'$.userid')"},
		{"reg_time", "$.created_at", "TEXT", ""}, {"action", "$.action", "INTEGER", ""},
		{"note", "$.note", "JSON", ""}, {"read", "$.read", "INTEGER", ""},
	},
	"subscribe": {
		{"name", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.media.title'),'')"},
		{"year", "$.media.year", "TEXT", ""},
		{"type", "$.media.media_type", "TEXT", ""}, {"keyword", "$.keyword", "TEXT", ""},
		{"tmdbid", "$.media.id", "INTEGER", ""}, {"imdbid", "$.media.imdb_id", "TEXT", ""},
		{"doubanid", "", "TEXT", "NULL"}, {"bangumiid", "", "INTEGER", "NULL"},
		{"anilistid", "", "INTEGER", "NULL"},
		{"mediaid", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.media.media_source'),'')||':'||COALESCE(json_extract(NEW.payload,'$.media.media_id'),'')"},
		{"media_source", "$.media.media_source", "TEXT", ""}, {"media_id", "$.media.media_id", "TEXT", ""},
		{"season", "$.season", "INTEGER", ""}, {"poster", "$.media.poster_path", "TEXT", ""},
		{"backdrop", "$.media.backdrop_path", "TEXT", ""}, {"vote", "$.media.vote_average", "REAL", ""},
		{"description", "$.media.overview", "TEXT", ""}, {"filter", "$.policy.include", "TEXT", ""},
		{"include", "$.policy.include", "TEXT", ""}, {"exclude", "$.policy.exclude", "TEXT", ""},
		{"quality", "$.policy.resolution", "TEXT", ""}, {"resolution", "$.policy.resolution", "TEXT", ""},
		{"effect", "", "TEXT", "NULL"}, {"total_episode", "$.total_episodes", "INTEGER", ""},
		{"start_episode", "$.start_episode", "INTEGER", ""}, {"lack_episode", "$.lack_episode", "INTEGER", ""},
		{"note", "$.policy", "JSON", ""}, {"state", "$.state", "TEXT", ""},
		{"last_update", "$.last_check", "TEXT", ""}, {"date", "$.created_at", "TEXT", ""},
		{"username", "", "TEXT", "''"}, {"sites", "$.site_ids", "JSON", ""},
		{"downloader", "$.downloader_id", "TEXT", ""}, {"best_version", "$.best_version", "INTEGER", ""},
		{"best_version_full", "$.best_version_full", "INTEGER", ""},
		{"current_priority", "$.current_priority", "INTEGER", ""},
		{"episode_priority", "$.episode_priority", "JSON", ""},
		{"save_path", "$.download_path", "TEXT", ""},
		{"search_imdbid", "$.source_search_imdb", "INTEGER", ""},
		{"manual_total_episode", "$.manual_total_episode", "INTEGER", ""},
		{"custom_words", "$.keyword", "TEXT", ""}, {"media_category", "", "TEXT", "NULL"},
		{"filter_groups", "$.policy.filter_groups", "JSON", ""},
		{"episode_group", "", "TEXT", "NULL"},
	},
	"subscribehistory": {
		{"name", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.media.title'),'')"},
		{"year", "$.media.year", "TEXT", ""},
		{"type", "$.media.media_type", "TEXT", ""}, {"keyword", "$.keyword", "TEXT", ""},
		{"tmdbid", "$.media.id", "INTEGER", ""}, {"media_source", "$.media.media_source", "TEXT", ""},
		{"media_id", "$.media.media_id", "TEXT", ""}, {"season", "$.season", "INTEGER", ""},
		{"poster", "$.media.poster_path", "TEXT", ""}, {"backdrop", "$.media.backdrop_path", "TEXT", ""},
		{"total_episode", "$.total_episodes", "INTEGER", ""},
		{"start_episode", "$.start_episode", "INTEGER", ""},
		{"date", "$.created_at", "TEXT", ""}, {"sites", "$.site_ids", "JSON", ""},
		{"save_path", "$.download_path", "TEXT", ""}, {"description", "$.media.overview", "TEXT", ""},
	},
	"downloadhistory": {
		{"path", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.download_path'),'')"},
		{"type", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.media.media_type'),'')"},
		{"title", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.media.title'),'')"}, {"year", "$.media.year", "TEXT", ""},
		{"tmdbid", "$.media.id", "INTEGER", ""}, {"media_source", "$.media.media_source", "TEXT", ""},
		{"media_id", "$.media.media_id", "TEXT", ""}, {"seasons", "", "TEXT", "NULL"},
		{"episodes", "", "TEXT", "NULL"}, {"image", "$.media.backdrop_path", "TEXT", ""},
		{"poster", "$.media.poster_path", "TEXT", ""}, {"downloader", "$.downloader_id", "TEXT", ""},
		{"download_hash", "$.hash", "TEXT", ""}, {"torrent_name", "$.resource_title", "TEXT", ""},
		{"channel", "", "TEXT", "'native'"},
		{"date", "", "TEXT", "COALESCE(json_extract(NEW.payload,'$.created_at'),json_extract(NEW.payload,'$.updated_at'))"},
		{"state", "$.state", "TEXT", ""}, {"progress", "$.progress", "REAL", ""},
		{"error", "$.error", "TEXT", ""}, {"workflow_id", "$.workflow_id", "TEXT", ""},
		{"downloader_snapshot", "$.downloader_snapshot", "TEXT", ""},
		{"material_ref", "$.material_ref", "TEXT", ""}, {"import_plan_id", "$.import_plan_id", "TEXT", ""},
	},
	"downloadfiles": {
		{"downloader", "$.downloader", "TEXT", ""}, {"download_hash", "$.download_hash", "TEXT", ""},
		{"fullpath", "$.fullpath", "TEXT", ""}, {"savepath", "$.savepath", "TEXT", ""},
		{"filepath", "$.filepath", "TEXT", ""}, {"torrentname", "$.torrentname", "TEXT", ""},
		{"state", "", "INTEGER", "COALESCE(json_extract(NEW.payload,'$.state'),1)"}, {"history_uid", "$.history_uid", "TEXT", ""},
		{"asset_id", "$.asset_id", "TEXT", ""},
	},
	"downloadfailure": {
		{"fingerprint", "$.fingerprint", "TEXT", ""}, {"type", "$.type", "TEXT", ""},
		{"title", "$.title", "TEXT", ""}, {"year", "$.year", "TEXT", ""},
		{"tmdbid", "$.tmdbid", "INTEGER", ""}, {"doubanid", "$.doubanid", "TEXT", ""},
		{"media_source", "$.media_source", "TEXT", ""}, {"media_id", "$.media_id", "TEXT", ""},
		{"seasons", "$.seasons", "TEXT", ""}, {"episodes", "$.episodes", "TEXT", ""},
		{"site", "$.site", "INTEGER", ""}, {"site_name", "$.site_name", "TEXT", ""},
		{"torrent_id", "$.torrent_id", "TEXT", ""}, {"torrent_name", "$.torrent_name", "TEXT", ""},
		{"torrent_size", "$.torrent_size", "REAL", ""}, {"downloader", "$.downloader", "TEXT", ""},
		{"source", "$.source", "TEXT", ""}, {"error_message", "$.error_message", "TEXT", ""},
		{"retry_count", "$.retry_count", "INTEGER", ""},
		{"first_failed_at", "$.first_failed_at", "TEXT", ""},
		{"last_failed_at", "$.last_failed_at", "TEXT", ""},
		{"next_retry_at", "$.next_retry_at", "TEXT", ""},
	},
	"mediaserveritem": {
		{"server", "$.server", "TEXT", ""}, {"library", "$.library", "TEXT", ""},
		{"item_id", "$.item_id", "TEXT", ""}, {"item_type", "$.item_type", "TEXT", ""},
		{"title", "$.title", "TEXT", ""}, {"year", "$.year", "TEXT", ""},
		{"tmdbid", "$.tmdbid", "INTEGER", ""}, {"path", "$.path", "TEXT", ""},
		{"seasoninfo", "$.seasoninfo", "JSON", ""}, {"note", "$.note", "JSON", ""},
	},
	"workflow": {
		{"name", "$.name", "TEXT", ""}, {"state", "$.state", "TEXT", ""},
		{"trigger_type", "$.trigger_type", "TEXT", ""}, {"timer", "$.timer", "TEXT", ""},
	},
	"agentchat": {
		{"session_id", "$.session_id", "TEXT", ""}, {"user_id", "$.user_id", "TEXT", ""},
		{"channel", "$.channel", "TEXT", ""}, {"title", "$.title", "TEXT", ""},
		{"created_at", "$.created_at", "TEXT", ""}, {"updated_at", "$.updated_at", "TEXT", ""},
	},
	"agenttask": {
		{"name", "$.name", "TEXT", ""}, {"content", "$.content", "TEXT", ""},
		{"trigger_type", "$.trigger_type", "TEXT", ""}, {"enabled", "$.enabled", "INTEGER", ""},
		{"user_id", "$.user_id", "TEXT", ""}, {"created_at", "$.created_at", "TEXT", ""},
	},
	"passkey": {
		{"credential_id", "$.credential_id", "TEXT", ""}, {"public_key", "$.public_key", "TEXT", ""},
		{"sign_count", "$.sign_count", "INTEGER", ""}, {"name", "$.name", "TEXT", ""},
	},
}
