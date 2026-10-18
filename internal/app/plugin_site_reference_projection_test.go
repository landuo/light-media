package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginSiteSwitchActualNumericFormAndCorePeerNamespaces(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	statisticInstall(t, a, cookie)
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, DashboardType: "total"})
	statisticExecute(t, a)
	one, two := identityRequire(t, a, "s1"), identityRequire(t, a, "s2")
	form := callTest(t, a.Handler(), "GET", "/api/plugins/MergeSiteSwitch/form", "", cookie, "")
	var data struct {
		Data struct {
			Model builtin.SiteControls
			Conf  json.RawMessage
		}
	}
	if json.Unmarshal(form.Body.Bytes(), &data) != nil || !reflect.DeepEqual(data.Data.Model.References("enable_sites"), []builtin.SiteReference{{Native: one.SourceID}}) {
		t.Fatal("numeric source model missing", form.Body.String())
	}
	var nodes any
	decoder := json.NewDecoder(bytes.NewReader(data.Data.Conf))
	decoder.UseNumber()
	if decoder.Decode(&nodes) != nil {
		t.Fatal("form JSON")
	}
	seen := 0
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			if v["title"] == a.Settings().Sites[0].Name || v["title"] == a.Settings().Sites[1].Name {
				if _, ok := v["value"].(json.Number); !ok {
					t.Fatal("form exposed opaque/string identity", v)
				}
				seen++
			}
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(nodes)
	if seen < 2 {
		t.Fatal("numeric options absent")
	}
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{two.SourceID}, "search_follow_enable_sites": true, "rss_sites": []int64{one.SourceID}, "statistic_follow_enable_sites": true})
	siteSwitchExecute(t, a, true)
	statisticExecute(t, a)
	if a.Settings().Sites[0].Enabled || !a.Settings().Sites[1].Enabled || !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s2"}) || !reflect.DeepEqual(a.Settings().RSSSites, []string{"s1"}) {
		t.Fatal("native core namespace changed", a.Settings().IndexerSites, a.Settings().RSSSites)
	}
	peer, err := a.pluginStore.Config(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	var cfg builtin.SiteStatisticConfiguration
	if json.Unmarshal(peer.Value, &cfg) != nil || !reflect.DeepEqual(cfg.StatisticSites, []builtin.SiteReference{{Native: two.SourceID}}) {
		t.Fatal("peer received opaque instead of source numeric", string(peer.Value))
	}
	var receipts int
	if a.Store.DB.QueryRow(`SELECT COUNT(*) FROM native_site_reference_transitions`).Scan(&receipts) != nil || receipts != 0 {
		t.Fatal("numeric input manufactured legacy conversion receipts", receipts)
	}
}

func TestPluginSiteSwitchTypedDeleteDuplicateAndSameOriginReadd(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	statisticInstall(t, a, cookie)
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true})
	statisticExecute(t, a)
	oldSite := a.Settings().Sites[0]
	old := identityRequire(t, a, oldSite.ID)
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{old.SourceID}, "statistic_follow_enable_sites": true})
	siteSwitchExecute(t, a, true)
	statisticExecute(t, a)
	text := builtin.SiteReference{Custom: fmt.Sprint(old.SourceID)}
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, StatisticSites: []builtin.SiteReference{{Native: old.SourceID}, {Native: old.SourceID}, text}})
	statisticExecute(t, a)
	if identityHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+old.SiteID, nil) != 200 {
		t.Fatal("delete")
	}
	statisticExecute(t, a)
	stored, _ := a.pluginStore.Config(ctx, "SiteStatistic")
	var cfg builtin.SiteStatisticConfiguration
	if json.Unmarshal(stored.Value, &cfg) != nil || !reflect.DeepEqual(cfg.StatisticSites, []builtin.SiteReference{{Native: old.SourceID}, text}) {
		t.Fatal("source first list.remove or numeric/string identity lost", string(stored.Value))
	}
	oldSite.ID = ""
	if identityHTTP(t, a, cookie, "POST", "/api/sites/manage", oldSite) != 200 {
		t.Fatal("readd")
	}
	var added SiteConfig
	for _, s := range a.Settings().Sites {
		if s.ID != "s2" {
			added = s
		}
	}
	newID := identityRequire(t, a, added.ID)
	if newID.SourceID <= old.SourceID || newID.SiteID == old.SiteID {
		t.Fatal("deleted numeric identity revived")
	}
	statisticExecute(t, a)
	stored, _ = a.pluginStore.Config(ctx, "SiteStatistic")
	if json.Unmarshal(stored.Value, &cfg) != nil || !reflect.DeepEqual(cfg.StatisticSites, []builtin.SiteReference{{Native: old.SourceID}, text, {Native: newID.SourceID}}) {
		t.Fatal("new site rebound historical selections", string(stored.Value))
	}
	if len(siteDeletedTestEvents(t, a)) != 1 {
		t.Fatal("original native deletion event regressed")
	}
}
