package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/statistic"
	"light-media/internal/sites"
)

func TestSourceAccountRowPreservesAllNullableSQLiteFields(t *testing.T) {
	raw := []byte(`{"id":1,"domain":"owned.invalid","name":"旧名称","username":null,"userid":"007","user_level":null,"join_at":null,"bonus":null,"upload":1125899906842624.5,"download":0.25,"ratio":null,"seeding":0.0,"leeching":null,"seeding_size":1234.75,"leeching_size":null,"seeding_info":"{\"x\":[0,1.25]}","message_unread":null,"message_unread_contents":"null","err_msg":null,"updated_day":"2026-10-21","updated_time":"12:34:56"}`)
	r, h, e := decodeSourceAccount(raw)
	if e != nil || !h.Good || r.Username != nil || r.MessageUnread != nil || *r.UserID != "007" || *r.Download != 0.25 || *r.SeedingSize != 1234.75 || *r.SeedingInfo != `{"x":[0,1.25]}` || *r.MessageUnreadContents != "null" {
		t.Fatal("SQLite values lost their original type or precision", e)
	}
	page := statistic.Page(statistic.Selected{Current: []statistic.Data{r.statistic("owned", "owned.invalid")}})
	var cells []statistic.Node
	var walk func(statistic.Node)
	walk = func(n statistic.Node) {
		if n.Component == "tbody" && len(n.Content) == 1 {
			cells = n.Content[0].Content
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	for _, n := range page {
		walk(n)
	}
	if len(cells) != 10 {
		t.Fatal("original account table missing")
	}
	for _, i := range []int{2, 3} {
		encoded, e := json.Marshal(cells[i].Text)
		if e != nil || string(encoded) != "null" {
			t.Fatal("unknown username/level changed from null to a reported empty value")
		}
	}
	_, p := pluginStoreFixture(t)
	sealed, e := p.seal(sourceAccountKind, "owned-source", "1", raw)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := p.unseal(sourceAccountKind, "owned-source", "1", sealed)
	if e != nil || !bytes.Equal(actual, raw) || bytes.Contains(sealed, []byte("旧名称")) {
		t.Fatal("raw account archive was normalized or not encrypted", e)
	}
	for _, change := range [][]byte{bytes.Replace(raw, []byte(`"id":1`), []byte(`"id":1,"id":2`), 1), bytes.Replace(raw, []byte(`"12:34:56"`), []byte(`"24:34:56"`), 1), bytes.Replace(raw, []byte(`"domain":"owned.invalid"`), []byte(`"domain":null`), 1)} {
		if _, _, e := decodeSourceAccount(change); e == nil {
			t.Fatal("invalid source account accepted")
		}
	}
}

func TestReviewedStatisticRejectsUnreviewedSource(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"version":1}`, `{"version":1,"account_rows":[]}`} {
		if ValidateReviewedSiteStatisticInput([]byte(raw)) == nil {
			t.Fatal("incomplete source history was accepted")
		}
	}
}

func TestSourceAccountEqualTimeNativeAndOriginalOrderIsTransitive(t *testing.T) {
	rows := []accountObservation{{Header: sourceAccountHeader{ID: 1, Time: "12:00:00"}}, {Header: sourceAccountHeader{ID: 0, Time: "12:00:00"}}, {Header: sourceAccountHeader{ID: 2, Time: "12:00:00"}}}
	rank := map[int64]int{1: 1, 2: 0}
	less := func(i, j int) bool { return accountObservationLess(rows[i], rows[j], rank) }
	equal := func(i, j int) bool { return !less(i, j) && !less(j, i) }
	for a := range rows {
		for b := range rows {
			for c := range rows {
				if less(a, b) && less(b, c) && !less(a, c) || equal(a, b) && equal(b, c) && !equal(a, c) {
					t.Fatal("mixed native/source ordering is not a strict weak order")
				}
			}
		}
	}
	if !less(2, 0) || !less(0, 1) {
		t.Fatal("equal-time native row changed the original source sequence")
	}
}

func TestSourceAccountLatestMatchesOriginalSQLiteWithErrorsAndDuplicates(t *testing.T) {
	s, _ := pluginStoreFixture(t)
	if _, e := s.DB.Exec(`CREATE TABLE owned_original_siteuserdata(id INTEGER PRIMARY KEY,domain TEXT,updated_day TEXT,updated_time TEXT,err_msg TEXT)`); e != nil {
		t.Fatal(e)
	}
	values := []struct{ domain, day, clock, err string }{
		{"a.invalid", "2026-10-19", "23:59:59", ""},
		{"a.invalid", "2026-10-20", "08:00:00", ""},
		{"a.invalid", "2026-10-20", "09:00:00", "error"},
		{"a.invalid", "2026-10-21", "10:00:00", "error"},
		{"b.invalid", "2026-06-01", "22:00:00", ""},
		{"c.invalid", "2026-10-21", "12:00:00", "error"},
	}
	for i, v := range values {
		id := int64(i + 1)
		if _, e := s.DB.Exec(`INSERT INTO owned_original_siteuserdata VALUES(?,?,?,?,?)`, id, v.domain, v.day, v.clock, v.err); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(`INSERT INTO source_site_account_rows VALUES('owned',?,?,?,?,?,?)`, id, v.domain, v.day, v.clock, v.err == "", []byte("owned opaque payload")); e != nil {
			t.Fatal(e)
		}
	}
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT s.id FROM owned_original_siteuserdata s JOIN (SELECT domain,MAX(updated_day) latest_day FROM owned_original_siteuserdata WHERE err_msg IS NULL OR err_msg='' GROUP BY domain) d ON s.domain=d.domain AND s.updated_day=d.latest_day ORDER BY s.updated_time DESC`)
	if e != nil {
		t.Fatal(e)
	}
	want := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	rows.Close()
	actual, e := sourceAccountLatestIDsTx(context.Background(), tx, "owned")
	if e != nil || !reflect.DeepEqual(actual, want) || !reflect.DeepEqual(actual, []int64{5, 3, 2}) {
		t.Fatal("latest-good-day join lost same-day errors, duplicates or time ordering", e)
	}
}

func TestSourceAccountOrdinaryUnreviewedImportDoesNotClaimCoreHistory(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m, e := p.BeginMigration(ctx, tokenHash("owned unreviewed source"), "SiteStatistic", "SiteStatistic")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Import(ctx, PluginImport{RunID: m.ID, Installation: PluginInstallation{InstanceID: "SiteStatistic", PluginID: "SiteStatistic", Version: "1.9.7", ManifestSchemaVersion: 1, Manifest: reviewedIYUUJSON(builtin.SiteStatisticManifest())}, Config: json.RawMessage(`{"enabled":false}`)}); e != nil {
		t.Fatal(e)
	}
	a := &App{Store: s, pluginStore: p}
	view, e := a.siteStatisticsSnapshot(ctx)
	if e != nil || len(view.Latest) != 0 {
		t.Fatal("ordinary import fabricated or broke unrelated core history", e)
	}
}

func statisticReviewedOwned(t *testing.T, source reviewedStatisticSnapshot) (*Store, *PluginStore, Settings) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	refs := []reviewedIYUUSite{}
	for _, v := range source.Sites {
		refs = append(refs, reviewedIYUUSite{OriginalID: v.OriginalID, URL: v.URL})
	}
	settings := reviewedIYUUOwnedTargetSettings(t, s, reviewedIYUUSnapshot{Sites: refs})
	return s, p, settings
}
func statisticReviewedImport(t *testing.T, p *PluginStore, raw []byte) (PluginMigrationRun, PluginMigrationReceipt) {
	t.Helper()
	fp, e := ReviewedSiteStatisticFingerprint(raw)
	if e != nil {
		t.Fatal("reviewed source no longer matches", e)
	}
	m, e := p.BeginMigration(context.Background(), fp, "SiteStatistic", "SiteStatistic")
	if e != nil {
		t.Fatal(e)
	}
	r, e := p.ImportReviewedSiteStatistic(context.Background(), m.ID, raw)
	if e != nil {
		t.Fatal("reviewed source import failed", e)
	}
	return m, r
}

func TestReviewedStatisticSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_REVIEWED_STATISTIC") != "owned-selected-stdin" {
		t.Skip("requires reviewed original source stdin; owned encrypted stores only")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, pluginImportLimit+1))
	if e != nil || len(raw) > pluginImportLimit {
		t.Fatal("source read failed")
	}
	source, headers, e := validateReviewedStatistic(raw)
	if e != nil {
		t.Fatal("source input changed or failed validation", e)
	}
	ctx := context.Background()
	t.Run("all-2133-raw-rows-cold-selection-and-native-overlay", func(t *testing.T) {
		s, p, settings := statisticReviewedOwned(t, source)
		defer func() { s.Close() }()
		m, receipt := statisticReviewedImport(t, p, raw)
		config, e := p.Config(ctx, "SiteStatistic")
		if e != nil || !bytes.Equal(config.Value, []byte(source.Config)) {
			t.Fatal("source configuration bytes changed", e)
		}
		in, e := p.Installation(ctx, "SiteStatistic")
		if e != nil || in.DesiredEnabled {
			t.Fatal("import activated original tasks", e)
		}
		var runs, kv int
		if s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&runs) != nil || runs != 0 || s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE kind='data' AND instance_id='SiteStatistic'`).Scan(&kv) != nil || kv != 0 {
			t.Fatal("shared account history was treated as plugin KV or executed work")
		}
		tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			t.Fatal(e)
		}
		proof, e := p.sourceAccountProofTx(ctx, tx)
		if e != nil || proof == nil || sourceAccountHeadersTx(ctx, tx, proof.Binding) != nil {
			t.Fatal("source account proof unavailable", e)
		}
		for i, h := range headers {
			var encrypted []byte
			if e = tx.QueryRow(`SELECT sealed FROM source_site_account_rows WHERE source_fingerprint=? AND original_id=?`, receipt.SourceFingerprint, h.ID).Scan(&encrypted); e != nil {
				t.Fatal("original account row missing", e)
			}
			got, e := p.unseal(sourceAccountKind, receipt.SourceFingerprint, strconv.FormatInt(h.ID, 10), encrypted)
			if e != nil || !bytes.Equal(got, []byte(source.AccountRows[i])) {
				t.Fatal("original account row bytes differ", e)
			}
		}
		ids, e := sourceAccountLatestIDsTx(ctx, tx, receipt.SourceFingerprint)
		if e != nil || !reflect.DeepEqual(ids, source.LatestIDs) {
			t.Fatal("latest selection differs from original SQLite query", e)
		}
		tx.Rollback()
		dir := filepath.Dir(s.lock.Name())
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
		s, e = OpenStore(dir)
		if e != nil {
			t.Fatal(e)
		}
		p, e = NewPluginStore(ctx, s)
		if e != nil {
			t.Fatal(e)
		}
		a := &App{Store: s, pluginStore: p, config: settings}
		view, e := a.siteStatisticsSnapshot(ctx)
		if e != nil || len(view.Latest) != len(source.LatestIDs) {
			t.Fatal("cold source statistics unavailable", e)
		}
		for i, id := range source.LatestIDs {
			var row sourceAccountRow
			json.Unmarshal([]byte(source.AccountRows[id-1]), &row)
			got := view.Latest[i]
			if got.Name != accountString(row.Name) || !reflect.DeepEqual(got.Username, row.Username) || !reflect.DeepEqual(got.UserLevel, row.UserLevel) || got.UpdatedDay != *row.Day || !reflect.DeepEqual(got.Upload, row.Upload) || !reflect.DeepEqual(got.Download, row.Download) || !reflect.DeepEqual(got.SeedingSize, row.SeedingSize) {
				t.Fatal("cold statistics changed original values or ordering")
			}
		}
		if _, e := statistic.Select(view); e != nil {
			t.Fatal("source data could not reach original chart selection", e)
		}
		if _, _, e := p.SaveConfigAndQueue(ctx, "SiteStatistic", config.Revision, config.Value); !pluginHandoffPending(e) {
			t.Fatal("source save bypassed execution handoff", e)
		}
		// Apply a later owned observation to the first source row on this day.
		site := settings.Sites[0]
		var original statistic.Data
		for _, data := range view.Latest {
			if data.SiteID == site.ID {
				original = data
				break
			}
		}
		if original.UpdatedDay == "" {
			t.Fatal("source site lost its binding")
		}
		up := int64(42)
		at, _ := time.ParseInLocation("2006-01-02 15:04:05", original.UpdatedDay+" 23:59:59", shanghai())
		native := siteSnapshot{SiteID: site.ID, Name: "Owned later observation", Domain: siteStatisticDomain(site), Day: original.UpdatedDay, ConfigKey: a.managedSiteKey(site), Stats: &sites.UserStats{Uploaded: &up, CapturedAt: at.UTC().Format(time.RFC3339Nano)}}
		if e = s.Put(ctx, "site_stats_latest", site.ID, native); e != nil {
			t.Fatal(e)
		}
		if e = s.Put(ctx, "site_stats_history", site.ID+":"+native.Day, native); e != nil {
			t.Fatal(e)
		}
		var changedConfig map[string]any
		json.Unmarshal(config.Value, &changedConfig)
		changedConfig["enabled"], changedConfig["dashboard_type"] = false, "all"
		updated, e := p.SaveConfig(ctx, "SiteStatistic", config.Revision, changedConfig)
		if e != nil {
			t.Fatal("owned later configuration rejected", e)
		}
		again, e := p.ImportReviewedSiteStatistic(ctx, m.ID, raw)
		if e != nil || again.Digest != receipt.Digest || again.ImportedAt != receipt.ImportedAt {
			t.Fatal("source replay changed its receipt", e)
		}
		stored, e := p.Config(ctx, "SiteStatistic")
		if e != nil || stored.Revision != updated.Revision || !bytes.Equal(stored.Value, updated.Value) {
			t.Fatal("source replay overwrote later configuration", e)
		}
		view, e = a.siteStatisticsSnapshot(ctx)
		if e != nil {
			t.Fatal(e)
		}
		found := 0
		for _, data := range view.Latest {
			if data.SiteID == site.ID {
				found++
				if data.Name != native.Name || data.Upload == nil || *data.Upload != 42 {
					t.Fatal("source replay hid a newer core observation")
				}
			}
		}
		if found != 1 {
			t.Fatal("same-day native collection duplicated source rows")
		}
		ownedStoppedPluginSourceHandoff(t, p, "SiteStatistic")
		if e = p.requirePluginExecutionHandoff(ctx, "SiteStatistic"); e != nil {
			t.Fatal("owned stopped-source handoff did not release reviewed statistics", e)
		}
		if _, e = s.DB.Exec(`DELETE FROM records WHERE kind=? AND id='SiteStatistic'`, pluginExecutionHandoffKind); e != nil {
			t.Fatal(e)
		}
		// The archive remains independent of plugin lifecycle.
		if e = p.Uninstall(ctx, "SiteStatistic"); e != nil {
			t.Fatal(e)
		}
		if e = p.Install(ctx, in); e != nil {
			t.Fatal("retained source namespace was lost", e)
		}
		if e = p.requirePluginExecutionHandoff(ctx, "SiteStatistic"); !pluginHandoffPending(e) {
			t.Fatal("retained reinstall promoted source to native execution", e)
		}
		if e = p.Reset(ctx, "SiteStatistic"); e != nil {
			t.Fatal(e)
		}
		if e = p.requirePluginExecutionHandoff(ctx, "SiteStatistic"); e != nil {
			t.Fatal("explicit reset did not restore native execution", e)
		}
		view, e = a.siteStatisticsSnapshot(ctx)
		if e != nil || len(view.Latest) != len(source.LatestIDs) {
			t.Fatal("plugin reset removed shared core account history", e)
		}
	})
	for _, table := range []string{"source_site_account_rows", "records", "plugin_migration_receipts"} {
		t.Run("rollback-"+table, func(t *testing.T) {
			s, p, _ := statisticReviewedOwned(t, source)
			condition := ""
			if table == "records" {
				condition = " WHEN NEW.kind='" + sourceAccountKind + "'"
			}
			if _, e := s.DB.Exec(`CREATE TRIGGER owned_late_failure BEFORE INSERT ON ` + table + condition + ` BEGIN SELECT RAISE(ABORT,'owned failure'); END`); e != nil {
				t.Fatal(e)
			}
			m, e := p.BeginMigration(ctx, reviewedStatisticFingerprint(), "SiteStatistic", "SiteStatistic")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.ImportReviewedSiteStatistic(ctx, m.ID, raw); e == nil {
				t.Fatal("late failure committed partial source history")
			}
			var rows int
			if s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM source_site_account_rows)+(SELECT COUNT(*) FROM plugin_installations)+(SELECT COUNT(*) FROM plugin_values)+(SELECT COUNT(*) FROM plugin_migration_receipts)`).Scan(&rows) != nil || rows != 0 {
				t.Fatal("account archive or plugin escaped rollback")
			}
		})
	}
	for _, corruption := range []string{"missing-row", "changed-index", "changed-body", "missing-root", "missing-receipt"} {
		t.Run(corruption, func(t *testing.T) {
			s, p, settings := statisticReviewedOwned(t, source)
			_, receipt := statisticReviewedImport(t, p, raw)
			var e error
			switch corruption {
			case "missing-row":
				_, e = s.DB.Exec(`DELETE FROM source_site_account_rows WHERE original_id=1`)
			case "changed-index":
				_, e = s.DB.Exec(`UPDATE source_site_account_rows SET updated_day='2099-01-01' WHERE original_id=1`)
			case "changed-body":
				_, e = s.DB.Exec(`UPDATE source_site_account_rows SET sealed=zeroblob(length(sealed)) WHERE original_id=?`, source.LatestIDs[0])
			case "missing-root":
				_, e = s.DB.Exec(`DELETE FROM records WHERE kind=?`, sourceAccountKind)
			case "missing-receipt":
				_, e = s.DB.Exec(`DELETE FROM plugin_migration_receipts WHERE source_fingerprint=?`, receipt.SourceFingerprint)
			}
			if e != nil {
				t.Fatal(e)
			}
			a := &App{Store: s, pluginStore: p, config: settings}
			if _, e = a.siteStatisticsSnapshot(ctx); pluginErrorCodeInternal(e) != "SITE_ACCOUNT_HISTORY_INVALID" {
				t.Fatal("damaged source history became an empty or partial success", e)
			}
		})
	}
	t.Run("actual-Go-SDK-config-page-and-dashboard-read-only", func(t *testing.T) {
		a := newTestApp(t)
		useBuiltInPluginProcess(t, a)
		refs := []reviewedIYUUSite{}
		for _, site := range source.Sites {
			refs = append(refs, reviewedIYUUSite{OriginalID: site.OriginalID, URL: site.URL})
		}
		settings := reviewedIYUUOwnedTargetSettings(t, a.Store, reviewedIYUUSnapshot{Sites: refs})
		a.cfgMu.Lock()
		a.config = settings
		a.cfgMu.Unlock()
		cookie := setupTestAdmin(t, a)
		statisticReviewedImport(t, a.pluginStore, raw)
		stored, e := a.pluginStore.Config(ctx, "SiteStatistic")
		if e != nil {
			t.Fatal(e)
		}
		for _, path := range []string{"form", "page"} {
			response := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/"+path, "", cookie, "")
			if response.Code != 200 {
				t.Fatalf("actual source SDK %s status=%d", path, response.Code)
			}
			if path == "page" && !strings.Contains(response.Body.String(), "VTable") || path == "dashboard" && !strings.Contains(response.Body.String(), "VApexChart") {
				t.Fatal("actual source SDK omitted the original statistics components")
			}
		}
		after, e := a.pluginStore.Config(ctx, "SiteStatistic")
		if e != nil || after.Revision != stored.Revision || !bytes.Equal(after.Value, stored.Value) {
			t.Fatal("reading source views wrote plugin configuration", e)
		}
		var count int
		if a.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&count) != nil || count != 0 {
			t.Fatal("reading source views scheduled migrated work")
		}
		staged := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/dashboard", "", cookie, "")
		if staged.Code != 400 || !strings.Contains(staged.Body.String(), "PLUGIN_MIGRATION_STAGED") {
			t.Fatal("staged source dashboard bypassed the original enable boundary")
		}
		body := reviewedIYUUJSON(map[string]any{"revision": stored.Revision, "config": json.RawMessage(stored.Value)})
		blocked := callTest(t, a.Handler(), "PUT", "/api/plugins/SiteStatistic/config", string(body), cookie, "")
		if blocked.Code != 400 || !strings.Contains(blocked.Body.String(), "PLUGIN_HANDOFF_REQUIRED") {
			t.Fatal("actual source HTTP enable bypassed execution handoff")
		}
		ownedStoppedPluginSourceHandoff(t, a.pluginStore, "SiteStatistic")
		accepted := callTest(t, a.Handler(), "PUT", "/api/plugins/SiteStatistic/config", string(body), cookie, "")
		if accepted.Code != 200 {
			t.Fatalf("owned source enable status=%d", accepted.Code)
		}
		statisticExecute(t, a)
		beforeDashboard, e := a.pluginStore.Config(ctx, "SiteStatistic")
		if e != nil {
			t.Fatal(e)
		}
		dashboard := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/dashboard", "", cookie, "")
		if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), "VApexChart") {
			t.Fatalf("owned enabled source dashboard status=%d", dashboard.Code)
		}
		afterDashboard, e := a.pluginStore.Config(ctx, "SiteStatistic")
		if e != nil || beforeDashboard.Revision != afterDashboard.Revision || !bytes.Equal(beforeDashboard.Value, afterDashboard.Value) {
			t.Fatal("dashboard read wrote source configuration", e)
		}
		if a.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM jobs)+(SELECT COUNT(*) FROM plugin_schedules WHERE active=1)+(SELECT COUNT(*) FROM plugin_event_subscriptions WHERE active=1)`).Scan(&count) != nil || count != 0 {
			t.Fatal("source init replayed refresh, schedules or notifications")
		}
	})
	t.Run("offline-CLI", func(t *testing.T) { statisticReviewedCLI(t, source, raw) })
}

func statisticReviewedCLI(t *testing.T, source reviewedStatisticSnapshot, raw []byte) {
	t.Helper()
	binary := os.Getenv("LIGHT_MEDIA_STATISTIC_CLI")
	if !filepath.IsAbs(binary) {
		t.Fatal("owned statistic migration CLI required")
	}
	s, _, _ := statisticReviewedOwned(t, source)
	dir := filepath.Dir(s.lock.Name())
	invoke := func(want bool, extra ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(binary, extra...)
		cmd.Stdin = bytes.NewReader(raw)
		out, e := cmd.CombinedOutput()
		if (e == nil) != want {
			t.Fatal("offline statistic CLI returned wrong outcome", e)
		}
		if !want {
			return nil
		}
		var value map[string]any
		if json.Unmarshal(out, &value) != nil {
			t.Fatal("offline statistic CLI did not return bounded JSON")
		}
		return value
	}
	invoke(true, "--validate")
	invoke(false, "--target-data-dir", dir, "--apply")
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	before := reviewedIYUUFileSnapshot(t, dir)
	if value := invoke(true, "--target-data-dir", dir); value["state"] != "ready" || value["writes"] != float64(0) {
		t.Fatal("read-only statistic target was not ready")
	}
	if !reflect.DeepEqual(before, reviewedIYUUFileSnapshot(t, dir)) {
		t.Fatal("read-only statistic CLI changed bytes or file modes")
	}
	if value := invoke(true, "--target-data-dir", dir, "--apply"); value["target_account_rows"] != float64(2133) || value["desired_enabled"] != false || value["scheduled"] != false {
		t.Fatal("statistic CLI lost history or activated work")
	}
	if value := invoke(true, "--target-data-dir", dir, "--apply"); value["replayed"] != true || value["target_account_rows"] != float64(2133) {
		t.Fatal("statistic CLI replay duplicated source data")
	}
	handoff := os.Getenv("LIGHT_MEDIA_HANDOFF_CLI")
	if !filepath.IsAbs(handoff) {
		t.Fatal("owned execution handoff CLI required")
	}
	command := exec.Command(handoff, "--instance-id", "SiteStatistic", "--target-data-dir", dir, "--apply")
	command.Stdin = bytes.NewReader(reviewedIYUUJSON(ownedStoppedPluginSourceObservation()))
	output, e := command.CombinedOutput()
	var delivered map[string]any
	if e != nil || json.Unmarshal(output, &delivered) != nil || delivered["state"] != "accepted" || delivered["executed"] != false {
		t.Fatal("owned stopped-source CLI handoff failed", e)
	}
	cold, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	p, e := NewPluginStore(context.Background(), cold)
	if e != nil || p.requirePluginExecutionHandoff(context.Background(), "SiteStatistic") != nil {
		t.Fatal("offline statistic handoff did not survive cold reopen", e)
	}
}
