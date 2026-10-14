package main

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/fnos"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func scratch(t *testing.T) {
	t.Helper()
	old, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e = os.Chdir(dir); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir("work", 0700); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Chdir(old) })
}
func TestSyntheticLifecycle(t *testing.T) {
	scratch(t)
	created, scanned, deleted := false, false, false
	var wire []string
	old := library{GUID: "existing", Name: "Existing library", Category: "Movie", Dirs: []string{"/vol2/1000/media/movies"}}
	own := library{GUID: "synthetic-owned-guid", Name: prefix + syntheticID, Category: "Mix", Dirs: []string{parentRoot + "/" + prefix + syntheticID}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire = append(wire, r.Method+" "+r.URL.Path)
		var b json.RawMessage
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&b)
		}
		sig, _ := url.ParseQuery(r.Header.Get("authx"))
		want := fnos.Signature(r.URL.Path, string(b), sig.Get("nonce"), sig.Get("timestamp"))
		if want != r.Header.Get("authx") {
			t.Errorf("signature mismatch for %s", r.URL.Path)
		}
		var data any
		switch r.URL.Path {
		case "/v/api/v1/login":
			data = map[string]string{"token": "synthetic-token"}
		case "/v/api/v1/user/info":
			data = map[string]int{"is_admin": 1}
		case "/v/api/v1/mdb/list":
			v := []library{old}
			if created && !deleted {
				v = append(v, own)
			}
			data = v
		case "/v/api/v1/mdb/create":
			if r.Method != "PUT" {
				t.Fatal("wrong create method")
			}
			var v map[string]any
			json.Unmarshal(b, &v)
			if v["category"] != "Mix" || v["skip_filesize"] != float64(0) || v["prefer_local_nfo"] != float64(1) {
				t.Errorf("wrong create options")
			}
			created = true
			data = nil
		case "/v/api/v1/mdb/synthetic-owned-guid":
			if r.Method == "DELETE" {
				deleted = true
				data = nil
			} else {
				data = map[string]any{"guid": own.GUID, "name": own.Name, "category": own.Category, "dir_list": own.Dirs, "prefer_local_nfo": 1, "skip_filesize": 0}
			}
		case "/v/api/v1/mdb/scan/synthetic-owned-guid":
			if r.Method != "POST" {
				t.Fatal("wrong scan method")
			}
			scanned = true
			data = nil
		case "/v/api/v1/item/list":
			var v map[string]any
			json.Unmarshal(b, &v)
			if v["ancestor_guid"] != own.GUID {
				t.Fatal("wrong ancestor")
			}
			data = map[string]any{"list": []any{map[string]string{"guid": "synthetic-item-guid"}}}
		case "/v/api/v1/item/synthetic-item-guid":
			data = map[string]any{"guid": "synthetic-item-guid", "title": "synthetic-password https://invalid.example/?secret=test\nInjected", "type": "Movie", "trim_id": "synthetic-only-id", "overview": "synthetic-token"}
		case "/v/api/v1/person/list/synthetic-item-guid":
			data = map[string]any{"list": []any{map[string]any{"name": "Synthetic Actor", "role": "synthetic-password", "job": "Actor", "profile_path": "https://invalid.example/avatar", "order": 1}}}
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer srv.Close()
	c := config{Host: srv.URL, Username: "synthetic-user", Password: "synthetic-password", ExperimentID: syntheticID, FilesPrepared: true}
	dry := run(context.Background(), c, "prepare", false)
	if dry.Status != "dry_run" || len(wire) != 0 {
		t.Fatal(dry)
	}
	for _, act := range []string{"prepare", "diagnose", "scan", "observe", "cleanup"} {
		out := run(context.Background(), c, act, true)
		if out.Status == "failed" {
			t.Fatalf("%s: %+v", act, out)
		}
		if !out.BaselineUnchanged {
			t.Fatal("baseline changed")
		}
		b, _ := json.Marshal(out)
		for _, secret := range []string{c.Username, c.Password, "synthetic-token", "https://invalid.example"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("secret leaked: %s", act)
			}
		}
		if act == "diagnose" && (len(out.Candidates) != 1 || out.Candidates[0].GUID != own.GUID) {
			t.Fatal("diagnose leaked baseline or missed candidate")
		}
		if act == "observe" && len(out.Items) != 1 {
			t.Fatal("missing item")
		}
	}
	if !created || !scanned || !deleted {
		t.Fatal("incomplete")
	}
	for _, w := range wire {
		if strings.Contains(w, "scanall") || strings.Contains(w, "/mdb/existing") || strings.Contains(w, "/item/existing") {
			t.Fatalf("forbidden request %s", w)
		}
	}
	r, e := loadReceipt(syntheticID, srv.URL+"/v")
	if e != nil || r.Phase != "cleaned" {
		t.Fatalf("receipt %+v %v", r, e)
	}
}
func TestSyntheticRefuseCrossOriginAndUnknownSuccess(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect followed with credentials") }))
	defer dest.Close()
	for _, mode := range []string{"redirect", "null", "empty", "business", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, dest.URL, 302)
				case "null":
					fmt.Fprint(w, `{"code":0,"data":null}`)
				case "empty":
					fmt.Fprint(w, `{"data":{}}`)
				case "business":
					fmt.Fprint(w, `{"code":403,"data":{},"msg":"secret"}`)
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", 2*1024*1024+2))
				}
			}))
			defer srv.Close()
			a := api{host: srv.URL, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
			var v map[string]any
			if e := a.call(context.Background(), "GET", "/test", nil, &v); e == nil {
				t.Fatal("unknown response accepted")
			}
		})
	}
}
func TestSyntheticReceiptRefusesTraversalAndScope(t *testing.T) {
	scratch(t)
	if _, e := loadReceipt("../../oops", "x"); e == nil {
		t.Fatal("traversal")
	}
	r, e := newReceipt(syntheticID, "https://synthetic.invalid/v", []library{{GUID: "old", Name: "Old", Category: "Movie", Dirs: []string{"/safe"}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = saveReceipt(r); e != nil {
		t.Fatal(e)
	}
	if _, e = loadReceipt(syntheticID, "https://other.invalid/v"); e == nil {
		t.Fatal("host mismatch")
	}
	if unchanged(r, []library{{GUID: "old", Name: "Changed", Category: "Movie", Dirs: []string{"/safe"}}}) {
		t.Fatal("changed library accepted")
	}
}

func TestSyntheticRecursiveSeasonEpisode(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var data any
		switch r.URL.Path {
		case "/v/api/v1/item/list":
			data = map[string]any{"list": []any{map[string]string{"guid": "tv-own", "type": "TV"}}}
		case "/v/api/v1/item/tv-own":
			data = map[string]any{"guid": "tv-own", "type": "TV", "title": "Synthetic & <TV>", "year": 2025, "posters": "/local/opaque", "poster_width": 32, "poster_height": 48}
		case "/v/api/v1/season/list/tv-own":
			data = []any{map[string]string{"guid": "season-own", "type": "Season"}}
		case "/v/api/v1/item/season-own":
			data = map[string]any{"guid": "season-own", "type": "Season", "title": "Synthetic Season", "season_number": 1}
		case "/v/api/v1/episode/list/season-own":
			data = []any{map[string]string{"guid": "episode-own", "type": "Episode"}}
		case "/v/api/v1/item/episode-own":
			data = map[string]any{"guid": "episode-own", "type": "Episode", "title": "Synthetic & <Episode>", "season_number": 1, "episode_number": 2}
		case "/v/api/v1/person/list/tv-own", "/v/api/v1/person/list/season-own", "/v/api/v1/person/list/episode-own":
			data = map[string]any{"list": []any{map[string]any{"name": "Synthetic Actor", "job": "Actor", "role": "Synthetic Role"}}}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer srv.Close()
	a := api{host: srv.URL + "/v", client: srv.Client()}
	v, e := a.observe(context.Background(), receipt{OwnedGUID: "test-library"})
	if e != nil {
		t.Fatal(e)
	}
	if len(v) != 3 || v[2]["season_number"] != float64(1) || v[2]["episode_number"] != float64(2) || v[2]["title"] != "Synthetic & <Episode>" {
		t.Fatalf("wrong tree: %+v", v)
	}
	if v[0]["poster_width"] != float64(32) || v[0]["image_bytes_verified"] != false {
		t.Fatal("image evidence incorrectly claimed")
	}
	if a.requests != 9 {
		t.Fatalf("wrong bounded request count %d", a.requests)
	}
}
