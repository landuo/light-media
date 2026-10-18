package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func configOracle(t *testing.T) map[string]any {
	t.Helper()
	raw, e := os.ReadFile("reference/plugin-config-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	return v.(map[string]any)
}
func equivalent(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		ar, aok := numberRat(x)
		br, bok := numberRat(y)
		return aok && bok && ar.Cmp(br) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !equivalent(v, y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			n, ok := y[k]
			if !ok || !equivalent(v, n) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
func assertJSON(t *testing.T, got, want any) {
	t.Helper()
	v, e := cloneJSON(got)
	if e != nil {
		t.Fatal(e)
	}
	if !equivalent(v, want) {
		g, _ := json.Marshal(v)
		w, _ := json.Marshal(want)
		t.Fatalf("got %s\nwant%s", g, w)
	}
}
func TestInstalledConfigOracle(t *testing.T) {
	doc := configOracle(t)
	cases := doc["configs"].([]any)
	if len(cases) != 19 {
		t.Fatal("case count")
	}
	for _, raw := range cases {
		item := raw.(map[string]any)
		t.Run(item["name"].(string), func(t *testing.T) {
			var input map[string]any
			if item["input"] != nil {
				input = item["input"].(map[string]any)
			}
			want := item["result"].(map[string]any)
			c := classConfig()
			r, e := c.Init(input, item["site_ids"].([]any))
			if item["name"] == "config-18" {
				if e == nil {
					t.Fatal("nonfinite source branch must explicitly fail")
				}
				return
			}
			if exception, ok := want["exception"]; ok {
				var f *SourceFault
				if !errors.As(e, &f) || f.Class != exception {
					t.Fatalf("got %v want %v", e, exception)
				}
				assertJSON(t, r.ConfigWrites, want["writes"])
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			jobs := []any{}
			for _, j := range r.Jobs {
				var minutes, delay any
				if j.Kind == "interval" {
					minutes = json.Number("3")
				} else {
					delay = json.Number("3")
				}
				jobs = append(jobs, map[string]any{"function": j.ID, "kind": j.Kind, "minutes": minutes, "date_delay": delay})
			}
			assertJSON(t, map[string]any{"state": c.State(), "writes": r.ConfigWrites, "jobs": jobs, "create_helper": r.CreateHelper}, want)
		})
	}
}
func TestInstalledFormOracle(t *testing.T) {
	doc := configOracle(t)
	for _, raw := range doc["forms"].([]any) {
		item := raw.(map[string]any)
		t.Run(item["name"].(string), func(t *testing.T) {
			inventory := FormInventory{}
			for _, raw := range item["sites"].([]any) {
				site := raw.(map[string]any)
				inventory.Sites = append(inventory.Sites, SiteOption{ID: site["id"], Name: site["name"]})
			}
			for _, name := range item["downloaders"].([]any) {
				inventory.Downloaders = append(inventory.Downloaders, name.(string))
			}
			if custom, ok := item["custom"].(map[string]any); ok && truth(custom["enabled"]) {
				for _, raw := range custom["sites"].([]any) {
					site := raw.(map[string]any)
					inventory.CustomSites = append(inventory.CustomSites, SiteOption{ID: site["id"], Name: site["name"]})
				}
			}
			form, defaults, e := Form(inventory)
			if e != nil {
				t.Fatal(e)
			}
			assertJSON(t, form, item["form"])
			assertJSON(t, defaults, item["defaults"])
			if Commands() != nil || Routes() != nil || Page() != nil {
				t.Fatal("source empty hooks changed")
			}
		})
	}
}
func TestInstalledCacheOracle(t *testing.T) {
	for i, raw := range configOracle(t)["caches"].([]any) {
		item := raw.(map[string]any)
		want := item["result"].(map[string]any)
		input := item["input"].([]any)
		var got []any
		var e error
		if item["append"] == nil {
			got, e = TrimCache(input, 10000)
		} else {
			got, e = AppendCache(input, item["append"])
		}
		if x, ok := want["exception"]; ok {
			var f *SourceFault
			if !errors.As(e, &f) || f.Class != x {
				t.Fatalf("case%d: %v", i, e)
			}
		} else {
			if e != nil {
				t.Fatal(e)
			}
			assertJSON(t, map[string]any{"value": got}, want)
		}
	}
	values := []any{}
	for i := 0; i < 10003; i++ {
		values = append(values, json.Number(itoa(i+1)))
	}
	got, e := TrimCache(values, 10000)
	if e != nil || len(got) != 10000 || got[0] != json.Number("4") {
		t.Fatal("limit", e)
	}
}
func TestInstalledSiteDeleteOracle(t *testing.T) {
	for i, raw := range configOracle(t)["deletions"].([]any) {
		item := raw.(map[string]any)
		want := item["result"].(map[string]any)
		base := map[string]any{"enabled": true, "cron": "0 */2 * * *", "token": "synthetic-token", "downloaders": []any{"qb"}, "sites": []any{}, "size": "1.5"}
		c := classConfig()
		if _, e := c.Init(base, nil); e != nil {
			t.Fatal(e)
		}
		c.values["sites"] = item["sites"]
		changed, e := c.SiteDeleted(item["deleted"])
		if x, ok := want["exception"]; ok {
			var f *SourceFault
			if !errors.As(e, &f) || f.Class != x {
				t.Fatalf("case%d %v", i, e)
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		writes := []any{}
		if changed {
			m, e := c.Snapshot()
			if e != nil {
				t.Fatal(e)
			}
			writes = append(writes, m)
		}
		assertJSON(t, map[string]any{"writes": writes, "state": c.State()}, want)
	}
}
