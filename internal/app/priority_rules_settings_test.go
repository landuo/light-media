package app

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/priorityrules"
)

func putRules(t *testing.T, a *App, cookie *http.Cookie, section string, revision int64, value any, status int) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"revision": revision, "value": value})
	w := callTest(t, a.Handler(), "PUT", "/api/settings/rules/"+section, string(raw), cookie, "")
	if w.Code != status {
		t.Fatalf("%s save: status %d want %d: %s", section, w.Code, status, w.Body.String())
	}
}
func TestPriorityRuleSectionCASDoesNotLoseUnrelatedSettingsOrRevisions(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	before := a.Settings()
	groups := []priorityrules.Group{{Name: "合成规则组", RuleString: priorityText("WEBDL > BLURAY")}}
	custom := []priorityrules.Definition{{"id": "Synthetic", "name": "合成规则", "include": "private-rule-pattern-not-for-logs"}}
	putRules(t, a, cookie, "custom", 0, custom, 200)
	putRules(t, a, cookie, "groups", 0, groups, 200)
	putRules(t, a, cookie, "priority", 0, []string{"site", "torrent"}, 200)
	putRules(t, a, cookie, "custom", 0, []priorityrules.Definition{}, 409)
	config := a.Settings().PriorityRules
	if !reflect.DeepEqual(config.CustomRules, custom) || !reflect.DeepEqual(config.Groups, groups) || !reflect.DeepEqual(config.TorrentPriority, []string{"site", "torrent"}) {
		t.Fatal("independent section save lost data")
	}
	// A stale system form explicitly containing old rule data cannot overwrite
	// the dedicated rule save, while its own requested setting still saves.
	before.PriorityRules = &priorityrules.Configuration{Groups: []priorityrules.Group{{Name: "stale"}}}
	hours := 12
	before.SiteDataRefreshHours = &hours
	raw, _ := json.Marshal(before)
	w := callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if w.Code != 200 || *a.Settings().SiteDataRefreshHours != 12 || !reflect.DeepEqual(a.Settings().PriorityRules, config) {
		t.Fatal("stale whole settings rolled rules back", w.Code)
	}
	w = callTest(t, a.Handler(), "GET", "/api/settings/rules", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "华语电影") {
		t.Fatal("original category choices unavailable", w.Code)
	}
	for _, section := range []string{"custom", "groups", "priority"} {
		rev, err := ruleRevision(ctx, a.Store.DB, section)
		if err != nil || rev != 1 {
			t.Fatal("independent revision changed", section, rev, err)
		}
	}
	// A trusted migration/internal write goes through the same revision boundary.
	c := a.Settings()
	c.PriorityRules.Groups[0].Name = "迁移后的名称"
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	putRules(t, a, cookie, "groups", 1, groups, 409)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rev, err := ruleRevision(ctx, reopened.Store.DB, "groups")
	if err != nil || rev != 2 || reopened.Settings().PriorityRules.Groups[0].Name != "迁移后的名称" {
		t.Fatal("CAS/configuration did not survive actual reopen", rev, err)
	}
}
func TestPriorityRuleSaveRollsBackConfigurationAndRevisionAndRejectsUnauthorized(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	putRules(t, a, nil, "priority", 0, []string{"site"}, 401)
	raw := `{"revision":0,"value":["site"]}`
	if w := callTest(t, a.Handler(), "PUT", "/api/settings/rules/priority", raw, cookie, "https://unrelated.invalid"); w.Code != 403 {
		t.Fatal("cross-origin rule save accepted", w.Code)
	}
	for section, value := range map[string]any{"custom": []map[string]string{{"id": "A", "name": "same"}, {"id": "B", "name": "same"}}, "groups": []map[string]string{{"name": "same"}, {"name": "same"}}, "priority": []string{"unknown"}} {
		putRules(t, a, cookie, section, 0, value, 400)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_rule_settings BEFORE INSERT ON records WHEN NEW.kind='config' AND NEW.id='main' BEGIN SELECT RAISE(ABORT,'synthetic persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	putRules(t, a, cookie, "priority", 0, []string{"site"}, 500)
	rev, err := ruleRevision(ctx, a.Store.DB, "priority")
	if err != nil || rev != 0 || a.Settings().PriorityRules != nil {
		t.Fatal("failed persistence published a revision or configuration", rev, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER fail_rule_settings`); err != nil {
		t.Fatal(err)
	}
	putRules(t, a, cookie, "priority", 0, []string{"site"}, 200)
	putRules(t, a, cookie, "priority", 1, []string{}, 200)
	if value := a.Settings().PriorityRules.TorrentPriority; value == nil || len(value) != 0 {
		t.Fatal("explicit empty selection not preserved")
	}
}
