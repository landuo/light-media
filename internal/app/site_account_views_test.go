package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"light-media/internal/sites"
)

func ownedAccountObservation(t *testing.T, a *App, site SiteConfig, day, clock, failure string, upload *int64) siteSnapshot {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04:05", day+" "+clock, shanghai())
	if err != nil {
		t.Fatal(err)
	}
	s := siteSnapshot{SiteID: site.ID, Name: site.Name, Domain: siteStatisticDomain(site), Day: day, ConfigKey: a.managedSiteKey(site), Connection: managedConnection{Status: "ok", CheckedAt: at.UTC().Format(time.RFC3339)}}
	if failure != "" {
		s.Connection.Status, s.Connection.Error = "failed", failure
	} else {
		s.Stats = &sites.UserStats{Uploaded: upload, CapturedAt: at.UTC().Format(time.RFC3339)}
	}
	return s
}

func ownedRecordAccount(t *testing.T, a *App, site SiteConfig, s siteSnapshot) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.recordSiteAccountObservationTx(ctx, tx, site, s); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSiteAccountDailyFailuresAndRecoveryMatchOriginal(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	site := parallelSearchSite("account-daily", "http://127.0.0.1:9911")
	config := a.Settings()
	config.Sites = []SiteConfig{site}
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	zero, seven := int64(0), int64(7)
	attempts := []struct {
		day, clock, failure                 string
		up                                  *int64
		days                                int
		storedClock, storedError, latestDay string
	}{
		{"2026-10-19", "08:00:00", "first failure", nil, 1, "08:00:00", "first failure", ""},
		{"2026-10-19", "09:00:00", "later failure", nil, 1, "08:00:00", "first failure", ""},
		{"2026-10-19", "10:00:00", "", &zero, 1, "10:00:00", "", "2026-10-19"},
		{"2026-10-19", "11:00:00", "after good", nil, 1, "10:00:00", "", "2026-10-19"},
		{"2026-10-20", "08:00:00", "next day failure", nil, 2, "08:00:00", "next day failure", "2026-10-19"},
		{"2026-10-20", "09:00:00", "", &seven, 2, "09:00:00", "", "2026-10-20"},
	}
	for _, test := range attempts {
		ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, test.day, test.clock, test.failure, test.up))
		var day siteSnapshot
		if err := a.Store.Get(ctx, "site_stats_history", site.ID+":"+test.day, &day); err != nil {
			t.Fatal(err)
		}
		data, err := statisticData(day, site)
		if err != nil || data.UpdatedTime != test.storedClock || data.Error != test.storedError {
			t.Fatal("daily row differs from SiteOper.update_userdata", data, err)
		}
		history, err := a.managedSiteHistory(ctx, site.ID)
		if err != nil || len(history.(map[string]any)["items"].([]managedSiteAccount)) != test.days {
			t.Fatal("failed days missing or duplicated", err)
		}
		var latest siteSnapshot
		err = a.Store.Get(ctx, "site_stats_latest", site.ID, &latest)
		if test.latestDay == "" {
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("first failure became valid data", err)
			}
		} else if err != nil || latest.Day != test.latestDay {
			t.Fatal("failure displaced last valid day", err)
		}
		view, err := a.siteStatisticsSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if test.latestDay == "" {
			if len(view.Latest) != 0 {
				t.Fatal("failure reached current totals")
			}
		} else if len(view.Latest) != 1 || view.Latest[0].UpdatedDay != test.latestDay {
			t.Fatal("cards and chart disagree")
		}
	}
	// Successful collections must no longer erase unrelated history at 90 days.
	old := ownedAccountObservation(t, a, site, "2026-01-01", "01:02:03", "", &seven)
	ownedRecordAccount(t, a, site, old)
	if _, err := a.Store.DB.Exec(`UPDATE records SET updated_at='2020-01-01T00:00:00Z' WHERE kind='site_stats_history'`); err != nil {
		t.Fatal(err)
	}
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-21", "12:00:00", "", &seven))
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='site_stats_history'`).Scan(&count); err != nil || count != 4 {
		t.Fatal("history still pruned", count, err)
	}
}

func TestSiteAccountFirstHTTPFailureIsRecordedAndAtomic(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "owned unavailable", 503) }))
	defer server.Close()
	site := parallelSearchSite("account-http", server.URL)
	config := a.Settings()
	config.Sites = []SiteConfig{site}
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER reject_owned_history BEFORE INSERT ON records WHEN NEW.kind='site_stats_history' BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.collectManagedSite(ctx, site.ID, "", nil); err == nil {
		t.Fatal("history failure reported success")
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_history')`).Scan(&count)
	if count != 0 {
		t.Fatal("first failure partially committed")
	}
	a.Store.DB.Exec(`DROP TRIGGER reject_owned_history`)
	if _, err := a.collectManagedSite(ctx, site.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	history, err := a.managedSiteHistory(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	items := history.(map[string]any)["items"].([]managedSiteAccount)
	if len(items) != 1 || items[0].Connection.Status != "failed" || items[0].Stats == nil {
		t.Fatal("failed collection invisible in account history")
	}
	var events int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&events); err != nil || events != 0 {
		t.Fatal("failed observation emitted success", err)
	}
}

func TestSiteAccountSnapshotEncryptionBindingAndLegacyRead(t *testing.T) {
	s, _ := pluginStoreFixture(t)
	ctx := context.Background()
	snapshot := siteSnapshot{SiteID: "owned", ConfigKey: "synthetic", Stats: &sites.UserStats{Username: "owned-private-user"}}
	if err := s.Put(ctx, "site_stats_latest", "owned", snapshot); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id='owned'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte(siteAccountSnapshotPrefix)) || bytes.Contains(raw, []byte(snapshot.Stats.Username)) {
		t.Fatal("private native account data saved as JSON")
	}
	for _, pair := range [][2]string{{"site_stats_latest", "other"}, {"site_connection", "owned"}} {
		var read siteSnapshot
		if s.openSiteAccountSnapshot(pair[0], pair[1], raw, &read) == nil {
			t.Fatal("ciphertext reusable under another identity")
		}
	}
	bad := append([]byte(nil), raw...)
	bad[len(bad)-1] ^= 1
	var read siteSnapshot
	if s.openSiteAccountSnapshot("site_stats_latest", "owned", bad, &read) == nil {
		t.Fatal("modified native account accepted")
	}
	legacy, _ := json.Marshal(snapshot)
	if _, err := s.DB.Exec(`UPDATE records SET payload=? WHERE kind='site_stats_latest' AND id='owned'`, legacy); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site_stats_latest", "owned", &read); err != nil || !reflect.DeepEqual(read, snapshot) {
		t.Fatal("pre-upgrade snapshot unreadable", err)
	}
	if _, err := s.DB.Exec(`UPDATE records SET payload=? WHERE kind='site_stats_latest' AND id='owned'`, bad); err != nil {
		t.Fatal(err)
	}
	if s.Get(ctx, "site_stats_latest", "owned", &read) == nil {
		t.Fatal("Store.Get ignored native snapshot corruption")
	}
}

func TestSiteAccountDateLookupRetainsInsertionOrderAcrossSiteSorting(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	one, two := parallelSearchSite("owned-a", "http://127.0.0.1:9911"), parallelSearchSite("owned-b", "http://127.0.0.1:9912")
	one.Name, two.Name = "shared-name", "shared-name"
	config := a.Settings()
	config.Sites = []SiteConfig{one, two}
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	up := int64(1)
	for _, site := range []SiteConfig{two, one} {
		ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-20", "12:00:00", "", &up))
	}
	for _, site := range []SiteConfig{one, two} {
		ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-21", "12:00:00", "", &up))
	}
	ownedRecordAccount(t, a, two, ownedAccountObservation(t, a, two, "2026-10-20", "13:00:00", "", &up))
	view, err := a.siteStatisticsSnapshot(ctx)
	if err != nil || len(view.History) != 2 || view.History[0].SiteID != two.ID || view.History[1].SiteID != one.ID {
		t.Fatal("site-card ordering changed original date lookup precedence", err)
	}
}

func TestSiteAccountImportedProjectionPreservesFractionsNullAndJSON(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	raw := []byte(`{"id":1,"domain":"owned.invalid","name":"owned","username":null,"userid":"007","user_level":null,"join_at":null,"bonus":null,"upload":1125899906842624.5,"download":0.25,"ratio":null,"seeding":0.0,"leeching":null,"seeding_size":1234.75,"leeching_size":null,"seeding_info":"[[2,1024]]","message_unread":0,"message_unread_contents":"[]","err_msg":null,"updated_day":"2026-10-21","updated_time":"12:34:56"}`)
	_, h, err := decodeSourceAccount(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := p.seal(sourceAccountKind, "owned", "1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO source_site_account_rows VALUES('owned',1,?,?,?,?,?)`, h.Domain, h.Day, h.Time, h.Good, sealed); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	a := &App{Store: s, pluginStore: p}
	view := siteAccountView{Binding: &sourceAccountBinding{SourceFingerprint: "owned"}}
	item, err := a.managedSiteAccountTx(ctx, tx, view, accountObservation{SiteID: "native-owned", Header: h})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Stats map[string]any `json:"stats"`
	}
	if json.Unmarshal(encoded, &actual) != nil {
		t.Fatal("invalid response")
	}
	stats := actual.Stats
	if stats["username"] != nil || stats["level"] != nil || stats["downloaded"] != 0.25 || stats["uploaded"] != 1125899906842624.5 || stats["seeding_bytes"] != 1234.75 || !reflect.DeepEqual(stats["seeding_info"], []any{[]any{float64(2), float64(1024)}}) || !reflect.DeepEqual(stats["message_unread_contents"], []any{}) {
		t.Fatal("site management normalized original account fields")
	}
}
