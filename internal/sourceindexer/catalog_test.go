package sourceindexer

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCompiledStatefulSequence(t *testing.T) {
	b, e := os.ReadFile("../../testdata/plugins/site-indexer-context/original-stateful.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct{ Steps []map[string]any }
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if d.Decode(&f) != nil || len(f.Steps) != 27 {
		t.Fatal("fixture invalid")
	}
	var r *Registry
	var added map[string]any
	records := []map[string]any{}
	appendDB := func(v any) {
		row := map[string]any{"id": nil, "name": nil, "domain": nil, "url": nil, "pri": json.Number("1"), "ua": nil, "is_active": true}
		for k, v := range v.(map[string]any) {
			row[k] = v
		}
		records = append(records, row)
	}
	project := func(v map[string]any) any {
		if v == nil {
			return nil
		}
		out := map[string]any{}
		for _, k := range []string{"id", "name", "domain", "schema", "_scheme", "url", "url_bak", "ext_domain", "ext_domains", "is_active", "ua", "public", "pri"} {
			out[k] = v[k]
		}
		return out
	}
	for _, step := range f.Steps {
		op := step["operation"].(string)
		var got any
		want, compare := step["observed_return"]
		switch op {
		case "inner.constructor":
			m := step["fixture_catalog"].(map[string]any)["indexers"].(map[string]any)
			r = New([]map[string]any{m["template"].(map[string]any), m["public"].(map[string]any)}, "owned-site-alias-probe/1.0")
		case "inner.get_indexsites":
			got = r.Sites()
			want = step["observed_projection"]
			compare = true
		case "fixture.db_records.extend":
			for _, v := range step["records"].([]any) {
				appendDB(v)
			}
		case "fixture.db_records.append":
			appendDB(step["record"])
		case "fixture.db_record.update":
			for _, row := range records {
				if reflect.DeepEqual(row["id"], step["id"]) {
					for k, v := range step["changes"].(map[string]any) {
						row[k] = v
					}
				}
			}
		case "inner.get_indexer":
			key := step["input_domain"].(string)
			var db map[string]any
			for _, row := range records {
				if row["domain"] == key {
					db = row
					break
				}
			}
			got = project(r.Get(key, db))
		case "inner.get_indexers":
			active := []map[string]any{}
			for _, row := range records {
				if truth(row["is_active"]) {
					active = append(active, row)
				}
			}
			items := []any{}
			for _, m := range r.List(active) {
				items = append(items, project(m))
			}
			got = items
		case "inner.add_indexer":
			input := step["input_indexer"].(map[string]any)
			// Actual probe lines 201/202/207/209 reuse custom_template;
			// JSON serializes it repeatedly and cannot encode this reference.
			switch step["sequence"].(json.Number).String() {
			case "18":
				added = input
			case "23", "25":
				input = added
			}
			got = r.Add(step["input_domain"].(string), input)
		default:
			t.Fatal("unknown operation", op)
		}
		if compare {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatalf("step %s %s differs\n%s\n%s", step["sequence"], op, a, b)
			}
		}
	}
}

func TestActualSelectedCatalogSchemaAndAliases(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugins/site-indexer-context/original-selected-catalog.json")
	if e != nil {
		t.Fatal(e)
	}
	var actual struct {
		Helper string `json:"helper_sha256"`
		SHA    string `json:"allowed_projection_sha256"`
		Rows   []struct {
			ID             int64  `json:"source_site_id"`
			Key            string `json:"source_db_domain"`
			Found          bool   `json:"found"`
			Schema, Domain string
			Ext            []string `json:"ext_domains"`
		}
	}
	if json.Unmarshal(raw, &actual) != nil || len(actual.Rows) != 17 || actual.Helper != "8a14c1668778305b9b40bdd5456e6a95afe6ef101b11db02750fbcd4e7ae0b64" || actual.SHA != "88ae4dc358641648fa61fc3e95f42be0df94aa2b13d77c8313fcbd6d0c026f39" {
		t.Fatal("invalid reviewed catalog")
	}
	templates := []map[string]any{}
	for _, row := range actual.Rows {
		exts := []any{}
		for _, x := range row.Ext {
			exts = append(exts, x)
		}
		templates = append(templates, map[string]any{"domain": row.Domain, "schema": row.Schema, "ext_domains": exts})
	}
	r := New(templates, "owned-global-agent")
	counts := map[string]int{}
	for _, row := range actual.Rows {
		t.Run(row.Key, func(t *testing.T) {
			counts[row.Schema]++
			cold := r.Get(row.Key, nil)
			if !row.Found || cold == nil || cold["schema"] != row.Schema || cold["domain"] != row.Domain {
				t.Fatal("actual cold alias/schema not found")
			}
			db := map[string]any{"id": row.ID, "domain": row.Key, "name": "owned", "url": "https://configured.fixture.invalid/custom/path/?q=1", "ua": "owned-row-agent", "is_active": false, "pri": json.Number("7")}
			merged := r.Get(row.Key, db)
			if merged["schema"] != row.Schema || merged["domain"] != "https://configured.fixture.invalid/" || merged["url"] != db["url"] || merged["ua"] != db["ua"] || merged["is_active"] != false {
				t.Fatal("source schema or configured URL merge lost")
			}
			// Direct DB lookup is a copy; it cannot overwrite the original template.
			if !reflect.DeepEqual(r.Get(row.Key, nil), cold) {
				t.Fatal("direct lookup changed shared template")
			}
			if r.Get("www."+row.Key, db) != nil || r.Get("https://"+row.Key+"/", db) != nil || r.Get(row.Key+":443", db) != nil {
				t.Fatal("query acquired an unproven fallback")
			}
		})
	}
	if !reflect.DeepEqual(counts, map[string]int{"MTorrent": 1, "RousiPro": 1, "NexusHhanclub": 1, "NexusPhp": 14}) {
		t.Fatal(counts)
	}
}

func TestActualAccountContextMissingFieldsRemainAbsent(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugins/site-indexer-context/original-account-context.json")
	if e != nil {
		t.Fatal(e)
	}
	var actual struct {
		Rows []struct {
			Key    string `json:"source_db_domain"`
			Fields map[string]struct {
				Present bool
				Type    string
				SHA     *string `json:"sha256"`
			}
			Values map[string]any `json:"allowed_values"`
		}
	}
	if json.Unmarshal(raw, &actual) != nil || len(actual.Rows) != 17 {
		t.Fatal("invalid account projection")
	}
	for _, row := range actual.Rows {
		for _, field := range []string{"api_url", "_scheme", "_schema", "url", "url_bak", "base_url", "api_base_url", "headers", "request_headers", "api_headers", "search_headers", "download_headers"} {
			v, ok := row.Fields[field]
			if !ok || v.Present || v.Type != "missing" || v.SHA != nil {
				t.Fatal("reviewed field is not absent", row.Key, field)
			}
		}
		if row.Values["public"] != false {
			t.Fatal("unreviewed public account variant")
		}
	}
}

func TestRegistryConcurrentOperationsAndDetachedSnapshots(t *testing.T) {
	r := New([]map[string]any{{"domain": "https://canonical.invalid", "schema": "NexusPhp", "ext_domains": []any{"https://alias.invalid"}}}, "owned-global")
	input := map[string]any{"domain": "https://added.invalid", "schema": "AddedSchema", "note": map[string]any{"key": []any{"original"}}}
	if !r.Add("added.invalid", input) || !r.Add("another.invalid", input) {
		t.Fatal("add failed")
	}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				got := r.List([]map[string]any{
					{"domain": "canonical.invalid", "id": int64(1), "url": "https://first.invalid/path/"},
					{"domain": "alias.invalid", "id": int64(2), "url": "https://second.invalid/path/"},
				})
				if len(got) != 2 || got[0]["id"] != int64(2) || got[1]["id"] != int64(2) {
					t.Error("list did not preserve shared alias merge")
					return
				}
				got[0]["id"] = "caller-only"
				one := r.Get("added.invalid", nil)
				one["note"].(map[string]any)["key"].([]any)[0] = "caller-only"
				if r.Get("another.invalid", nil)["note"].(map[string]any)["key"].([]any)[0] != "original" {
					t.Error("returned nested snapshot mutated registry")
					return
				}
				if len(r.Sites()) != 4 {
					t.Error("registry key set changed")
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestInstalledURLFunctions(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugins/site-indexer-context/original-url-functions.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		SHA    string `json:"source_sha256"`
		Python string
		Cases  []struct{ Input, Scheme, Netloc, Domain string }
	}
	if json.Unmarshal(raw, &f) != nil || f.SHA != "b21856bf4b309d20fe9d5a68538fbb0ddc7a2c23bbde424e5700caf57587297a" || f.Python != "3.12.13" || len(f.Cases) != 53 {
		t.Fatal("invalid source URL oracle")
	}
	for i, c := range f.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			scheme, host := netloc(c.Input)
			if scheme != c.Scheme || host != c.Netloc || Domain(c.Input) != c.Domain {
				t.Errorf("source URL projection differs: got %q %q %q want %q %q %q", scheme, host, Domain(c.Input), c.Scheme, c.Netloc, c.Domain)
			}
		})
	}
}
