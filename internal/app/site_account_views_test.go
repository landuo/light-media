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
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
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

func TestSiteAccountBoundedViewsPreserveHistorySelection(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	site := parallelSearchSite("account-selection", "http://127.0.0.1:9911")
	upload := int64(7)
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-18", "08:00:00", "", &upload))
	latest := ownedAccountObservation(t, a, site, "2026-10-19", "08:00:00", "", &upload)
	ownedRecordAccount(t, a, site, latest)
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-20", "08:00:00", "later failure", nil))
	// A last refresh may refer to an earlier date. It cannot hide a newer day
	// in the daily history. Stale credentials must also be skipped.
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-18", "10:00:00", "", &upload))
	stale := ownedAccountObservation(t, a, site, "2026-10-21", "08:00:00", "", &upload)
	stale.ConfigKey = "previous-credentials"
	if err := a.Store.Put(ctx, "site_stats_history", site.ID+":"+stale.Day, stale); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	full, err := a.siteAccountViewTx(ctx, tx, []SiteConfig{site})
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.readSiteAccountViewTx(ctx, tx, []SiteConfig{site}, siteAccountViewScope{latestOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	want := full.latest(site.ID)
	if len(want) != 1 || want[0].Header.Day != latest.Day || !reflect.DeepEqual(current.latest(site.ID), want) {
		t.Fatal("latest lookup changed historical selection")
	}
	day, err := a.readSiteAccountViewTx(ctx, tx, []SiteConfig{site}, siteAccountViewScope{day: "2026-10-20"})
	if err != nil || len(day.Rows) != 1 || day.Rows[0].Header.Good || day.Rows[0].Header.Day != "2026-10-20" {
		t.Fatalf("same-day lookup lost a failed observation: %v", err)
	}
}

func BenchmarkSiteAccountLatest180Days(b *testing.B) {
	ctx := b.Context()
	a, err := New(Options{DataDir: b.TempDir(), SafeMode: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = a.Close() })
	site := parallelSearchSite("account-growth", "http://127.0.0.1:9911")
	configKey := a.managedSiteKey(site)
	start := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for i := range 180 {
		at := start.AddDate(0, 0, i)
		day := at.Format("2006-01-02")
		snapshot := siteSnapshot{SiteID: site.ID, ConfigKey: configKey, Day: day,
			Connection: managedConnection{Status: "ok", CheckedAt: at.Format(time.RFC3339)},
			Stats:      &sites.UserStats{Username: strings.Repeat("fixture", 256)}}
		if err := a.Store.putSiteAccountSnapshotTx(ctx, tx, "site_stats_history", site.ID+":"+day, snapshot); err != nil {
			tx.Rollback()
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	want := start.AddDate(0, 0, 179).Format("2006-01-02")
	for _, latestOnly := range []bool{false, true} {
		name := "full"
		if latestOnly {
			name = "latest"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					b.Fatal(err)
				}
				view, err := a.readSiteAccountViewTx(ctx, tx, []SiteConfig{site}, siteAccountViewScope{latestOnly: latestOnly})
				tx.Rollback()
				if latest := view.latest(site.ID); err != nil || len(latest) != 1 || latest[0].Header.Day != want {
					b.Fatalf("latest result differs: %v", err)
				}
			}
		})
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
	if _, err := a.Store.DB.Exec(`UPDATE site_statistic_event SET updated_at='2020-01-01T00:00:00Z' WHERE 1=1`); err != nil {
		t.Fatal(err)
	}
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-21", "12:00:00", "", &seven))
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM site_statistic_event WHERE 1=1`).Scan(&count); err != nil || count != 4 {
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
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER reject_owned_history BEFORE INSERT ON site_statistic_event WHEN NEW.record_kind='site_stats_history' BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.collectManagedSite(ctx, site.ID, "", nil); err == nil {
		t.Fatal("history failure reported success")
	}
	var count int
	count = countRecordKinds(t, a.Store.DB, "site_connection", "site_stats_history")
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
	if err := s.DB.QueryRow(`SELECT payload FROM sitestatistic WHERE uid='owned'`).Scan(&raw); err != nil {
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
	if _, err := s.DB.Exec(`UPDATE sitestatistic SET payload=? WHERE uid='owned'`, legacy); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site_stats_latest", "owned", &read); err != nil || !reflect.DeepEqual(read, snapshot) {
		t.Fatal("pre-upgrade snapshot unreadable", err)
	}
	if _, err := s.DB.Exec(`UPDATE sitestatistic SET payload=? WHERE uid='owned'`, bad); err != nil {
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

func TestLatestSourceAccountViewChecksPermissionAndCommittedVersion(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	site := parallelSearchSite("cached-account", "http://127.0.0.1:9911")
	settings := a.Settings()
	settings.Sites = []SiteConfig{site}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	in := plugins.SiteAccountHostRequestV1{Latest: true}
	latest := func() json.RawMessage {
		t.Helper()
		raw, err := a.originalHostSiteAccountsMode(ctx, sourceSitesScope(), in, true)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := latest()
	if len(raw) == 0 || a.latestSourceAccounts.Load() == nil {
		t.Fatal("latest result not stored")
	}
	raw[0] = 'X'
	if !json.Valid(latest()) {
		t.Fatal("cached bytes were modified by a caller")
	}
	denied := errors.New("no longer authorized")
	scope := originalBaseCallScope{validate: func(context.Context) error { return denied }}
	if raw, err := a.originalHostSiteAccountsMode(ctx, scope, in, true); !errors.Is(err, denied) || raw != nil {
		t.Fatal("cached read skipped the live scope")
	}
	up := int64(71)
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-19", "08:00:00", "", &up))
	if raw = latest(); !bytes.Contains(raw, []byte("2026-10-19")) {
		t.Fatal("did not observe new account snapshot")
	}
}

func TestNativeLatestCandidatesRetainDomainsAndSameDayFailures(t *testing.T) {
	a := newTestApp(t)
	aSite := parallelSearchSite("owned-a", "http://127.0.0.1:9911")
	bSite := parallelSearchSite("owned-b", "http://127.0.0.1:9912")
	c := a.Settings()
	c.Sites = []SiteConfig{aSite, bSite}
	if err := a.saveSettings(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	up := int64(123)
	write := func(site SiteConfig, domain, day, problem string) {
		obs := ownedAccountObservation(t, a, site, day, "08:00:00", problem, &up)
		obs.Domain = domain
		ownedRecordAccount(t, a, site, obs)
	}
	write(aSite, "other.test", "2026-10-12", "")
	write(aSite, "shared.test", "2026-10-13", "")
	write(aSite, "shared.test", "2026-10-14", "failed")
	write(bSite, "shared.test", "2026-10-14", "")
	request := plugins.SiteAccountHostRequestV1{Latest: true}
	for i := 0; i < 2; i++ {
		raw, err := a.siteAccountProjection(t.Context(), request, true)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(raw, []byte(`"domain":"shared.test"`)) != 2 || bytes.Count(raw, []byte(`"domain":"other.test"`)) != 1 || bytes.Contains(raw, []byte("2026-10-13")) {
			t.Fatalf("lost a domain or same-day row: %s", raw)
		}
		// Unrelated changes invalidate the dashboard but can reuse native candidates.
		a.latestSourceAccounts.Store(nil)
	}
	write(bSite, "shared.test", "2026-10-15", "")
	raw, err := a.siteAccountProjection(t.Context(), request, true)
	if err != nil || bytes.Contains(raw, []byte("2026-10-14")) || !bytes.Contains(raw, []byte("2026-10-15")) {
		t.Fatal(string(raw), err)
	}
}
