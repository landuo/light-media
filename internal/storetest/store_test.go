package storetest

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/app"

	_ "modernc.org/sqlite"
)

// v1 fixture: the pre-split schema, one row per representative storage shape.
func writeV1Fixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instance.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
CREATE TABLE records(kind TEXT NOT NULL,id TEXT NOT NULL,payload BLOB NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(kind,id));
INSERT INTO migrations VALUES(1,'2026-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		kind, id, payload string
	}{
		{"subscription", "sub_1", `{"id":"sub_1","keyword":"dune","season":2,"state":"active","total_episodes":8,"best_version":true,"media":{"id":438631,"media_type":"movie","title":"Dune","year":2021,"poster_path":"/p.jpg"},"policy":{"resolution":"2160p"},"site_ids":["mteam"],"downloader_id":"qb","created_at":"2026-01-02T03:04:05Z"}`},
		{"intent", "it_1", `{"id":"it_1","hash":"abc","state":"downloading","downloader_id":"qb","download_path":"/dl/a","media":{"id":438631,"media_type":"movie","title":"Dune"},"created_at":"2026-01-02T03:05:05Z"}`},
		{"native_download", "nd_1", `{"id":"nd_1","hash":"def","state":"downloaded","downloader_id":"qb","download_path":"/dl/b","media":{"id":438631,"media_type":"movie","title":"Dune"},"created_at":"2026-01-02T03:06:05Z"}`},
		{"notification", "nt_1", `{"id":"nt_1","title":"任务失败","message":"整理失败","read":false,"created_at":"2026-01-02T03:07:05Z"}`},
		{"import_plan", "plan_1", `{"id":"plan_1","state":"ready","intent_id":"it_1","media_key":"movie:438631","created_at":"2026-01-02T03:08:05Z","files":[{"id":"f1","source":"/dl/a.mkv","target":"/lib/a.mkv","status":"pending"},{"id":"f2","source":"/dl/b.mkv","target":"/lib/b.mkv","status":"pending"}]}`},
		{"managed_file", "mf_1", `{"path":"/lib/a.mkv","root_id":"root1","asset_id":"asset_1","sha256":"aa"}`},
		{"site_icon", "mteam", "\x00sealed-icon"},
		{"plugin_dashboard_settings", "desktop", `{"layout":"grid"}`},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO records VALUES(?,?,?,?)`, r.kind, r.id, []byte(r.payload), "2026-01-02T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyRecordsMigrateToFeatureTables(t *testing.T) {
	dir := t.TempDir()
	writeV1Fixture(t, dir)
	store, err := app.OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	var leftover int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='records'`).Scan(&leftover); err != nil {
		t.Fatalf("records object: %v", err)
	}
	if leftover != 0 {
		t.Fatal("records table or view still present")
	}
	var frozen int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='records_frozen_v1'`).Scan(&frozen); err != nil || frozen != 1 {
		t.Fatalf("frozen table missing: %d %v", frozen, err)
	}
	var version int
	if err := store.DB.QueryRow(`SELECT MAX(version) FROM migrations`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("migration version: %d %v", version, err)
	}
	var state string
	var tmdb int
	if err := store.DB.QueryRow(`SELECT state,tmdbid FROM subscribe WHERE uid='sub_1'`).Scan(&state, &tmdb); err != nil {
		t.Fatalf("subscribe projection: %v", err)
	}
	if state != "active" || tmdb != 438631 {
		t.Fatalf("unexpected subscribe row %q %d", state, tmdb)
	}
	var downloads int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM downloadhistory`).Scan(&downloads); err != nil || downloads != 2 {
		t.Fatalf("downloadhistory rows: %d %v", downloads, err)
	}
	var plans, files int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM import_plan`).Scan(&plans); err != nil || plans != 1 {
		t.Fatalf("import_plan rows: %d %v", plans, err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM import_plan_file WHERE plan_id='plan_1'`).Scan(&files); err != nil || files != 2 {
		t.Fatalf("import_plan_file rows: %d %v", files, err)
	}
	var unread int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM message WHERE read=0`).Scan(&unread); err != nil || unread != 1 {
		t.Fatalf("message unread: %d %v", unread, err)
	}
	var archived int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_record WHERE kind='plugin_dashboard_settings'`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("plugin_record rows: %d %v", archived, err)
	}

}

func TestStoreRoutesKindsToFeatureTables(t *testing.T) {
	store, err := app.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sub := map[string]any{"id": "sub_9", "state": "active", "season": 1, "media": map[string]any{"id": 7, "media_type": "tv", "title": "Show"}}
	if err := store.Put(ctx, "subscription", "sub_9", sub); err != nil {
		t.Fatalf("put: %v", err)
	}
	var got map[string]any
	if err := store.Get(ctx, "subscription", "sub_9", &got); err != nil || got["id"] != "sub_9" {
		t.Fatalf("get: %v %v", got, err)
	}
	var tmdb int
	if err := store.DB.QueryRow(`SELECT tmdbid FROM subscribe WHERE uid='sub_9'`).Scan(&tmdb); err != nil || tmdb != 7 {
		t.Fatalf("projection: %d %v", tmdb, err)
	}
	list, err := store.List(ctx, "subscription", 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	// A payload-only update must keep typed columns current.
	if _, err := store.DB.ExecContext(ctx, `UPDATE subscribe SET payload=json_set(payload,'$.state','paused') WHERE uid='sub_9'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	var state string
	if err := store.DB.QueryRow(`SELECT state FROM subscribe WHERE uid='sub_9'`).Scan(&state); err != nil || state != "paused" {
		t.Fatalf("stale projection: %q %v", state, err)
	}
	if err := store.Delete(ctx, "subscription", "sub_9"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.Get(ctx, "subscription", "sub_9", &got); err == nil {
		t.Fatal("deleted row still readable")
	}
	// Unknown plugin kinds stay addressable through plugin_record.
	raw := map[string]any{"plugin_id": "p1", "key": "k", "value": []any{1, 2}}
	if err := store.Put(ctx, "plugin_custom_state", "k1", raw); err != nil {
		t.Fatalf("plugin put: %v", err)
	}
	var back map[string]any
	if err := store.Get(ctx, "plugin_custom_state", "k1", &back); err != nil || back["plugin_id"] != "p1" {
		t.Fatalf("plugin get: %v %v", back, err)
	}
	if err := store.Delete(ctx, "plugin_custom_state", "k1"); err != nil {
		t.Fatalf("plugin delete: %v", err)
	}
}

func TestSitesAndSettingsUseMoviePilotKeyColumns(t *testing.T) {
	store, err := app.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	cfg, _ := json.Marshal(map[string]any{"version": 1})
	if err := store.Put(ctx, "config", "main", cfg); err != nil {
		t.Fatalf("config put: %v", err)
	}
	var key string
	if err := store.DB.QueryRow(`SELECT key FROM systemconfig WHERE uid='main'`).Scan(&key); err != nil || key != "main" {
		t.Fatalf("systemconfig key: %q %v", key, err)
	}
	if err := store.Put(ctx, "ui_preferences", "catalog_explore", map[string]any{"page": 1}); err != nil {
		t.Fatalf("prefs put: %v", err)
	}
	var username, prefKey string
	if err := store.DB.QueryRow(`SELECT username,key FROM userconfig WHERE uid='catalog_explore'`).Scan(&username, &prefKey); err != nil || prefKey != "catalog_explore" || username != "admin" {
		t.Fatalf("userconfig: %q %q %v", username, prefKey, err)
	}
}

func TestSealedPayloadsSurviveProjection(t *testing.T) {
	store, err := app.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	// Raw non-JSON bytes used by the sealed plugin namespaces must not abort
	// the projection triggers.
	sealed := []byte{0x00, 0x01, 0x02, 0xff}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO subscription_acquisition(id,payload,updated_at) VALUES('a1',?,?)`, sealed, "2026-01-01"); err != nil {
		t.Fatalf("sealed insert: %v", err)
	}
	var n int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM subscription_acquisition WHERE id='a1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sealed row: %d %v", n, err)
	}
}

func TestMigrationBackfillsSubscriptionAliases(t *testing.T) {
	dir := t.TempDir()
	writeV1Fixture(t, dir)
	store, err := app.OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	var number int64
	if err := store.DB.QueryRow(`SELECT number FROM original_subscription_aliases WHERE subscription_id='sub_1'`).Scan(&number); err != nil || number <= 0 {
		t.Fatalf("alias row: %d %v", number, err)
	}
}

func TestMigrationReportAfterSplit(t *testing.T) {
	dir := t.TempDir()
	writeV1Fixture(t, dir)
	store, err := app.OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	report, err := app.CheckRecordsMigration(context.Background(), store.DB)
	store.Close()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Version != 2 {
		t.Fatalf("version: %d", report.Version)
	}
	if report.SourceRows != 0 {
		t.Fatalf("legacy rows still visible: %d", report.SourceRows)
	}
}

func TestAssembledSQLUsesSplitSchema(t *testing.T) {
	a, err := app.New(app.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close()
	if errs := app.ValidateAssembledSQL(context.Background(), a.Store.DB); len(errs) > 0 {
		for _, err := range errs {
			t.Errorf("assembled SQL: %v", err)
		}
	}
}

func TestHotQueriesUseFeatureIndexes(t *testing.T) {
	store, err := app.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	cases := []struct {
		query, index string
	}{
		{`SELECT payload FROM library_asset WHERE path=?`, "asset_path"},
		{`SELECT payload FROM managed_file WHERE asset_id=?`, "managed_file_asset"},
		{`SELECT payload FROM organize_queue_member WHERE group_id=?`, "organize_queue_group"},
		{`SELECT payload FROM downloadhistory WHERE download_hash=?`, "ix_downloadhistory_download_hash_date"},
		{`SELECT payload FROM message WHERE read=0`, "notification_unread"},
		{`SELECT payload FROM import_plan WHERE state=?`, "import_plan_state"},
		{`SELECT payload FROM directory_file WHERE directory_id=? ORDER BY last_seen DESC`, "directory_file_dir"},
		{`SELECT payload FROM cache_entry WHERE expires_at<?`, "cache_entry_expires"},
	}
	for _, tc := range cases {
		rows, err := store.DB.Query(`EXPLAIN QUERY PLAN `+tc.query, 1)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		var plan string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			plan += detail + " "
		}
		rows.Close()
		if !strings.Contains(plan, tc.index) {
			t.Errorf("%s\nuses: %s\nwant index %s", tc.query, plan, tc.index)
		}
	}
}
