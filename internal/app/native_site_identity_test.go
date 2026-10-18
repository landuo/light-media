package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/sites"
)

func identityTestSite(name, host string) SiteConfig {
	return SiteConfig{Config: sites.Config{Name: name, Kind: "nexusphp", BaseURL: "https://" + host}, Enabled: true, DailyBudget: 600}
}
func identityHTTP(t *testing.T, a *App, cookie *http.Cookie, method, path string, value any) int {
	t.Helper()
	body := ""
	if value != nil {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = string(b)
	}
	w := callTest(t, a.Handler(), method, path, body, cookie, "")
	if w.Code >= 500 {
		t.Log("owned HTTP returned", w.Code, w.Body.String())
	}
	return w.Code
}
func identityCount(t *testing.T, a *App, table string) int {
	t.Helper()
	if !strings.HasPrefix(table, "native_site_") {
		t.Fatal("bad test table")
	}
	var n int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func identityRequire(t *testing.T, a *App, appID string) nativeSiteIdentity {
	t.Helper()
	v, err := a.Store.nativeSiteByAppID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func identityFixtureSourceFingerprint(s string) string {
	b := sha256.Sum256([]byte("owned-independent-source-installation:" + s))
	return hex.EncodeToString(b[:])
}

func TestNativeSiteIdentityActualHTTPStableRenameConcurrentAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	cookie := setupTestAdmin(t, a)
	if status := identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("Alpha", "alpha.example.com")); status != 200 {
		t.Fatal(status)
	}
	site := a.Settings().Sites[0]
	first := identityRequire(t, a, site.ID)
	if first.SourceID < 1 || first.State != "active" || first.CanonicalOrigin != "https://alpha.example.com" {
		t.Fatal(first)
	}
	site.Name = "Renamed display only"
	if status := identityHTTP(t, a, cookie, "POST", "/api/sites/manage", site); status != 200 {
		t.Fatal(status)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			edit := site
			edit.Name = "Concurrent rename"
			raw, _ := json.Marshal(edit)
			w := callTest(t, a.Handler(), "POST", "/api/sites/manage", string(raw), cookie, "")
			statuses <- w.Code
		})
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 {
			t.Fatal("concurrent edit", status)
		}
	}
	if current := identityRequire(t, a, site.ID); current != first {
		t.Fatal("rename/save changed source identity", first, current)
	}
	if identityCount(t, a, "native_site_identities") != 1 || identityCount(t, a, "native_site_migration_receipts") != 0 {
		t.Fatal("save allocated or claimed twice")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	byNumeric, err := a.Store.nativeSiteByID(ctx, first.SourceID)
	if err != nil || byNumeric != first || a.Settings().Sites[0].ID != site.ID {
		t.Fatal("cold reopen changed identity", err)
	}
}
func TestNativeSiteIdentityActualHTTPConcurrentNewUniqueAndDuplicateOrigin(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	var wg sync.WaitGroup
	statuses := make(chan int, 16)
	for i := 0; i < 16; i++ {
		i := i
		wg.Go(func() {
			v := identityTestSite(fmt.Sprint(i), fmt.Sprintf("s%d.example.com", i))
			raw, _ := json.Marshal(v)
			w := callTest(t, a.Handler(), "POST", "/api/sites/manage", string(raw), cookie, "")
			statuses <- w.Code
		})
	}
	wg.Wait()
	close(statuses)
	for s := range statuses {
		if s != 200 {
			t.Fatal(s)
		}
	}
	inventory, err := a.nativeSiteInventory(context.Background())
	if err != nil || len(inventory) != 16 {
		t.Fatal(len(inventory), err)
	}
	seen := map[int64]bool{}
	for _, v := range inventory {
		if seen[v.SourceID] || v.SourceID < 1 {
			t.Fatal("numeric reuse", v)
		}
		seen[v.SourceID] = true
	}
	same := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			raw, _ := json.Marshal(identityTestSite("duplicate", "same.example.com"))
			same <- callTest(t, a.Handler(), "POST", "/api/sites/manage", string(raw), cookie, "").Code
		})
	}
	wg.Wait()
	close(same)
	ok, conflict := 0, 0
	for s := range same {
		if s == 200 {
			ok++
		} else if s == 400 {
			conflict++
		} else {
			t.Fatal(s)
		}
	}
	if ok != 1 || conflict != 1 || identityCount(t, a, "native_site_identities") != 17 {
		t.Fatal("duplicate allocation", ok, conflict)
	}
}
func TestNativeSiteIdentityActualHTTPDeleteReaddNeverRecyclesOrRevives(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("Alpha", "alpha.example.com")) != 200 {
		t.Fatal("create")
	}
	old := a.Settings().Sites[0]
	first := identityRequire(t, a, old.ID)
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+old.ID, nil) != 200 {
		t.Fatal("delete")
	}
	deleted := identityRequire(t, a, old.ID)
	if deleted.SourceID != first.SourceID || deleted.State != "deleted" || deleted.DeletedAt == "" {
		t.Fatal(deleted)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", old) != 400 {
		t.Fatal("opaque tombstone revived")
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("Same name and URL", "alpha.example.com")) != 200 {
		t.Fatal("readd")
	}
	replacement := a.Settings().Sites[0]
	second := identityRequire(t, a, replacement.ID)
	if second.SourceID <= first.SourceID || replacement.ID == old.ID || identityCount(t, a, "native_site_identities") != 2 {
		t.Fatal("ID recycled", first, second)
	}
	if again := identityRequire(t, a, old.ID); again != deleted {
		t.Fatal("deleted fact replaced")
	}
}
func TestNativeSiteIdentityActualSaveRollbackIncludesMappingConfigAndDeleteCache(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_identity_config_fail BEFORE INSERT ON records WHEN NEW.kind='config' BEGIN SELECT RAISE(ABORT,'owned config failure'); END`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("Alpha", "alpha.example.com")) != 500 {
		t.Fatal("expected SQL failure")
	}
	if len(a.Settings().Sites) != 0 || identityCount(t, a, "native_site_identities") != 0 {
		t.Fatal("mapping escaped config rollback")
	}
	var n int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='config'`).Scan(&n)
	if n != 0 {
		t.Fatal("config persisted")
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_identity_config_fail`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("Alpha", "alpha.example.com")) != 200 {
		t.Fatal("create")
	}
	site := a.Settings().Sites[0]
	before := identityRequire(t, a, site.ID)
	if err := a.Store.Put(ctx, "site_connection", site.ID, map[string]string{"owned": "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_identity_cache_fail BEFORE DELETE ON records WHEN OLD.kind='site_connection' BEGIN SELECT RAISE(ABORT,'owned cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+site.ID, nil) != 500 {
		t.Fatal("expected cache SQL failure")
	}
	if len(a.Settings().Sites) != 1 || identityRequire(t, a, site.ID) != before {
		t.Fatal("delete partially committed")
	}
	var cache map[string]string
	if err := a.Store.Get(ctx, "site_connection", site.ID, &cache); err != nil {
		t.Fatal("cache partially removed", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_identity_cache_fail`); err != nil {
		t.Fatal(err)
	}
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+site.ID, nil) != 200 {
		t.Fatal("delete retry")
	}
	if err := a.Store.Get(ctx, "site_connection", site.ID, &cache); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cache retained", err)
	}
	if identityRequire(t, a, site.ID).State != "deleted" {
		t.Fatal("not retired")
	}
}
func TestNativeSiteIdentityActualImportPreservesUnclaimedOriginalIDAndRollback(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	doc := json.RawMessage(`[{"id":42,"name":"Original","url":"https://alpha.example.com","cookie":"synthetic-only"}]`)
	if identityHTTP(t, a, cookie, "POST", "/api/sites/import", doc) != 200 {
		t.Fatal("import")
	}
	site := a.Settings().Sites[0]
	native := identityRequire(t, a, site.ID)
	if native.SourceID == 42 || identityCount(t, a, "native_site_import_refs") != 1 {
		t.Fatal("foreign numeric ID became native")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM native_site_import_refs`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	var ref importedSiteIdentity
	if a.Store.Unseal(sealed, &ref) != nil || ref.OriginalSourceID != 42 || ref.NativeSourceID != native.SourceID || ref.SiteID != site.ID || ref.Status != "unclaimed" {
		t.Fatal("original identity evidence missing", ref)
	}
	if identityHTTP(t, a, cookie, "POST", "/api/sites/import", doc) != 200 || identityRequire(t, a, site.ID) != native || identityCount(t, a, "native_site_import_refs") != 1 {
		t.Fatal("repeat import changed identity")
	}
	if _, err := a.migratedSiteIdentity(ctx, identityFixtureSourceFingerprint("not-registered"), 42); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("read claimed source", err)
	}
	if identityCount(t, a, "native_site_migration_receipts") != 0 || identityCount(t, a, "native_site_source_snapshots") != 0 {
		t.Fatal("import silently migrated selections")
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_identity_import_fail BEFORE INSERT ON native_site_import_refs BEGIN SELECT RAISE(ABORT,'owned import failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := json.RawMessage(`[{"id":99,"name":"Other","url":"https://beta.example.com"}]`)
	if identityHTTP(t, a, cookie, "POST", "/api/sites/import", failed) != 400 {
		t.Fatal("expected atomic import failure")
	}
	if len(a.Settings().Sites) != 1 || identityCount(t, a, "native_site_identities") != 1 || identityCount(t, a, "native_site_import_refs") != 1 {
		t.Fatal("failed import partly committed")
	}
}
func TestNativeSiteIdentityExplicitOldSettingsBootstrapAndReadOnlyQueries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	config := Settings{Sites: []SiteConfig{identityTestSite("Z name", "z.example.com"), identityTestSite("A name", "a.example.com")}}
	config.Sites[0].ID = "legacy-z"
	config.Sites[1].ID = "legacy-a"
	sealed, err := store.Seal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?)`, sealed, now()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.nativeSiteByAppID(ctx, "legacy-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("read allocated", err)
	}
	var n int
	store.DB.QueryRow(`SELECT COUNT(*) FROM native_site_identities`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	first := identityRequire(t, a, "legacy-a")
	second := identityRequire(t, a, "legacy-z")
	if first.SourceID != 1 || second.SourceID != 2 {
		t.Fatal("bootstrap is not deterministic opaque order", first, second)
	}
	before := identityCount(t, a, "native_site_identities")
	cookie := setupTestAdmin(t, a)
	if identityHTTP(t, a, cookie, "GET", "/api/sites/manage", nil) != 200 {
		t.Fatal("read")
	}
	if _, err = a.nativeSiteInventory(ctx); err != nil {
		t.Fatal(err)
	}
	if identityCount(t, a, "native_site_identities") != before || identityCount(t, a, "native_site_migration_receipts") != 0 {
		t.Fatal("read changed identity")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if identityRequire(t, a, "legacy-a") != first || identityRequire(t, a, "legacy-z") != second {
		t.Fatal("reopen reallocated")
	}
}
func TestNativeSiteIdentityMigrationRequiresIndependentSourceSnapshotExactOriginAndTombstone(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	if _, err := a.saveManagedSite(ctx, identityTestSite("Shared display name", "alpha.example.com")); err != nil {
		t.Fatal(err)
	}
	site := a.Settings().Sites[0]
	native := identityRequire(t, a, site.ID)
	if _, err := a.saveManagedSite(ctx, identityTestSite("Shared display name", "beta.example.com")); err != nil {
		t.Fatal(err)
	}
	other := a.Settings().Sites[1]
	fp := identityFixtureSourceFingerprint("original-install-one")
	snapshot, err := a.registerSiteIdentitySourceSnapshot(ctx, fp, []siteIdentitySourceEntry{{OriginalSourceID: 777, CanonicalOrigin: "https://alpha.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 777, other.ID); err == nil {
		t.Fatal("name/domain guess allowed")
	}
	if _, err = a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 778, site.ID); err == nil {
		t.Fatal("unobserved original ID claimed")
	}
	receipt, err := a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 777, site.ID)
	if err != nil || receipt.OriginalSourceID != 777 || receipt.NativeSourceID != native.SourceID {
		t.Fatal(receipt, err)
	}
	repeated, err := a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 777, site.ID)
	if err != nil || repeated != receipt {
		t.Fatal("migration receipt not stable", err)
	}
	resolved, err := a.migratedSiteIdentity(ctx, fp, 777)
	if err != nil || resolved != native {
		t.Fatal("lookup", err)
	}
	if _, err = a.migratedSiteIdentity(ctx, identityFixtureSourceFingerprint("other-install"), 777); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross-source numeric collision", err)
	}
	site.Name = "Renamed"
	if _, err = a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if after, err := a.migratedSiteIdentity(ctx, fp, 777); err != nil || after != native {
		t.Fatal("display rename changed migration", err)
	}
	if _, err = a.deleteManagedSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.migratedSiteIdentity(ctx, fp, 777); err == nil {
		t.Fatal("tombstone resolved as live")
	}
	if _, err = a.saveManagedSite(ctx, identityTestSite("Again", "alpha.example.com")); err != nil {
		t.Fatal(err)
	}
	replacement := a.Settings().Sites[len(a.Settings().Sites)-1]
	if identityRequire(t, a, replacement.ID).SourceID == native.SourceID {
		t.Fatal("recycled")
	}
	if _, err = a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 777, replacement.ID); err == nil {
		t.Fatal("old source receipt rebound to new record")
	}
	if identityCount(t, a, "native_site_migration_receipts") != 1 {
		t.Fatal("old receipt erased")
	}
}
func TestNativeSiteIdentityMigrationReceiptAndMapTamperFailClosed(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	if _, err := a.saveManagedSite(ctx, identityTestSite("Alpha", "alpha.example.com")); err != nil {
		t.Fatal(err)
	}
	site := a.Settings().Sites[0]
	original := identityRequire(t, a, site.ID)
	fp := identityFixtureSourceFingerprint("owned")
	snapshot, err := a.registerSiteIdentitySourceSnapshot(ctx, fp, []siteIdentitySourceEntry{{OriginalSourceID: 1, CanonicalOrigin: "https://alpha.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 1, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE native_site_identities SET canonical_origin='https://beta.example.com' WHERE site_id=?`, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.nativeSiteByID(ctx, original.SourceID); err == nil {
		t.Fatal("tampered mapping accepted")
	}
	if _, err = a.migratedSiteIdentity(ctx, fp, 1); err == nil {
		t.Fatal("tampered receipt backing accepted")
	}
	a.settingsMu.Lock()
	config := a.Settings()
	err = a.saveSettings(ctx, config)
	a.settingsMu.Unlock()
	if err == nil {
		t.Fatal("save silently repaired/claimed tampered mapping")
	}
	if !reflect.DeepEqual(a.Settings().Sites, []SiteConfig{site}) {
		t.Fatal("failed save changed config")
	}
}

func TestNativeSiteIdentityActualSettingsAndPrioritiesShareAtomicProducer(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	config := a.Settings()
	one := identityTestSite("One", "one.example.com")
	one.ID = "explicit-local-one"
	two := identityTestSite("Two", "two.example.com")
	two.ID = "explicit-local-two"
	config.Sites = []SiteConfig{one, two}
	if identityHTTP(t, a, cookie, "PUT", "/api/settings", config) != 200 {
		t.Fatal("actual settings save")
	}
	first, second := identityRequire(t, a, one.ID), identityRequire(t, a, two.ID)
	if identityHTTP(t, a, cookie, "POST", "/api/sites/priorities", []sitePriority{{ID: one.ID, Priority: 9}, {ID: two.ID, Priority: 1}}) != 200 {
		t.Fatal("actual priorities")
	}
	if identityRequire(t, a, one.ID) != first || identityRequire(t, a, two.ID) != second {
		t.Fatal("other producer allocated")
	}
	config = a.Settings()
	config.Sites = []SiteConfig{config.Sites[1]}
	if identityHTTP(t, a, cookie, "PUT", "/api/settings", config) != 200 {
		t.Fatal("settings removal")
	}
	if identityRequire(t, a, one.ID).State != "deleted" || identityRequire(t, a, two.ID) != second {
		t.Fatal("generic save bypassed tombstone")
	}
}
func TestNativeSiteIdentityExplicitMigrationSQLRollbackNewSnapshotAndChangedOrigin(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	if _, err := a.saveManagedSite(ctx, identityTestSite("Alpha", "alpha.example.com")); err != nil {
		t.Fatal(err)
	}
	site := a.Settings().Sites[0]
	first := identityRequire(t, a, site.ID)
	fp := identityFixtureSourceFingerprint("one")
	snapshot, err := a.registerSiteIdentitySourceSnapshot(ctx, fp, []siteIdentitySourceEntry{{OriginalSourceID: 9, CanonicalOrigin: "https://alpha.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_identity_migration_fail BEFORE INSERT ON native_site_migration_receipts BEGIN SELECT RAISE(ABORT,'owned migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 9, site.ID); err == nil {
		t.Fatal("SQL failure ignored")
	}
	if identityCount(t, a, "native_site_migration_receipts") != 0 || identityRequire(t, a, site.ID) != first {
		t.Fatal("failed migration partly committed")
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_identity_migration_fail`); err != nil {
		t.Fatal(err)
	}
	receipt, err := a.commitSiteIdentityMigration(ctx, snapshot.SnapshotID, 9, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := a.registerSiteIdentitySourceSnapshot(ctx, fp, []siteIdentitySourceEntry{{OriginalSourceID: 10, CanonicalOrigin: "https://beta.example.com"}, {OriginalSourceID: 9, CanonicalOrigin: "https://alpha.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := a.commitSiteIdentityMigration(ctx, newer.SnapshotID, 9, site.ID)
	if err != nil || replayed != receipt {
		t.Fatal("new snapshot rebound existing receipt", err)
	}
	site.BaseURL = "https://changed.example.com"
	if _, err = a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	updated := identityRequire(t, a, site.ID)
	if updated.SourceID != first.SourceID || updated.CanonicalOrigin == first.CanonicalOrigin {
		t.Fatal("edit changed native numeric identity")
	}
	if _, err = a.migratedSiteIdentity(ctx, fp, 9); err == nil {
		t.Fatal("old origin receipt silently followed new origin")
	}
	wrong, err := a.registerSiteIdentitySourceSnapshot(ctx, identityFixtureSourceFingerprint("wrong-origin"), []siteIdentitySourceEntry{{OriginalSourceID: 9, CanonicalOrigin: "https://sub.changed.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.commitSiteIdentityMigration(ctx, wrong.SnapshotID, 9, site.ID); err == nil {
		t.Fatal("domain suffix guessed target")
	}
	if identityCount(t, a, "native_site_migration_receipts") != 1 {
		t.Fatal("failed source claim wrote receipt")
	}
}
