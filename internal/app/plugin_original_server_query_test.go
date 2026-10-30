package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestOriginalPluginCenterQueryParamsSource38(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-server-20261030/source-query-decoding.json")
	var source struct {
		Cases []struct {
			RawHex  string `json:"raw_hex"`
			Value   string `json:"value"`
			Present bool   `json:"present"`
		} `json:"cases"`
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cases) != 38 {
		t.Fatal("actual fixed QueryParams38 unavailable", err)
	}
	for _, row := range source.Cases {
		raw, err := hex.DecodeString(row.RawHex)
		if err != nil {
			t.Fatal(err)
		}
		value, present := originalPluginServerQuery(string(raw), "plugin_ids")
		if value != row.Value || present != row.Present {
			t.Fatalf("raw query %s: (%q,%v), source (%q,%v)", row.RawHex, value, present, row.Value, row.Present)
		}
	}
}

type originalCenterBoundaryRequest struct {
	Version, Method, Route string
	Raw                    *string
	PeerBody               string   `json:"peer_body"`
	PeerStatus             int      `json:"peer_status"`
	InstalledIDs           []string `json:"installed_ids"`
	Share                  bool
	ContentType            string `json:"content_type"`
}

// Full source-selected116 inputs cross actual TCP/App.Handler/SQLite/owned HTTP,
// including ServeMux, HEAD network semantics, empty307 bodies and raw queries.
func TestOriginalPluginCenterQuerySlashSource116(t *testing.T) {
	originalCenterTestEnvironment(t)
	var source struct {
		SHA          string `json:"source_sha"`
		Observations []struct {
			Case, Version   string
			Status          int
			BodyHex         string  `json:"body_hex"`
			ContentType     *string `json:"content_type"`
			Location, Allow *string
			Wire            []json.RawMessage
			Scope           struct {
				Path       string
				RawPathHex string `json:"raw_path_hex"`
				QueryHex   string `json:"query_hex"`
			}
			Request originalCenterBoundaryRequest
		}
	}
	raw, err := os.ReadFile("../../testdata/plugin-server-20261030/source-query-slash.json")
	if err != nil || json.Unmarshal(raw, &source) != nil || source.SHA != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Observations) != 116 {
		t.Fatal("fixed actual source116 unavailable", err)
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	var mu sync.Mutex
	body, status := "{}", 200
	calls := []map[string]any{}
	uids := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload any
		if r.Method == http.MethodPost {
			d := json.NewDecoder(r.Body)
			d.UseNumber()
			if err := d.Decode(&payload); err != nil {
				t.Error(err)
			}
		}
		var contentType any
		if r.Header.Get("Content-Type") != "" {
			contentType = r.Header.Get("Content-Type")
		}
		uid := r.Header.Get("X-MoviePilot-User-Uid")
		row := map[string]any{"method": r.Method, "path": r.URL.RequestURI(), "body": payload, "uid_present": uid != "", "uid_length": len(uid), "user_agent_equal": r.Header.Get("User-Agent") == originalPluginServerUserAgent(), "content_type": contentType}
		mu.Lock()
		calls = append(calls, row)
		uids = append(uids, uid)
		reply, code := body, status
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(reply))
	}))
	defer peer.Close()
	var actualPath, actualQuery, actualRawPath string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		actualPath, actualQuery, actualRawPath = r.URL.Path, r.URL.RawQuery, r.URL.EscapedPath()
		mu.Unlock()
		a.Handler().ServeHTTP(w, r)
	}))
	defer host.Close()
	client := host.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	out := []map[string]any{}
	for _, row := range source.Observations {
		t.Run(row.Version+"/"+row.Case, func(t *testing.T) {
			c := row.Request
			originalCenterSetList(t, a, c.InstalledIDs)
			settings := a.Settings()
			settings.PluginServerURL = originalServerURLPointer(peer.URL + "/")
			settings.PluginStatisticShare = &c.Share
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			body, status = c.PeerBody, c.PeerStatus
			before := len(calls)
			mu.Unlock()
			payload := ""
			if c.Raw != nil {
				payload = *c.Raw
			}
			r, err := http.NewRequest(c.Method, host.URL+"/api/"+c.Version+"/plugin/"+c.Route, strings.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			r.Host = "owned-asgi"
			r.AddCookie(cookie)
			r.Header.Set("Content-Type", c.ContentType)
			response, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			gotBody, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			gotCalls := append([]map[string]any{}, calls[before:]...)
			gotPath, gotQuery, gotRawPath := actualPath, actualQuery, actualRawPath
			mu.Unlock()
			if gotPath != row.Scope.Path || hex.EncodeToString([]byte(gotQuery)) != row.Scope.QueryHex || hex.EncodeToString([]byte(gotRawPath)) != row.Scope.RawPathHex {
				t.Fatal("input raw path/query changed before real handler")
			}
			actualPeer, _ := json.Marshal(gotCalls)
			wantPeer, _ := json.Marshal(row.Wire)
			if !reflect.DeepEqual(originalCenterComparableJSON(t, actualPeer), originalCenterComparableJSON(t, wantPeer)) {
				t.Errorf("peer wire differs: %s vs %s", actualPeer, wantPeer)
			}
			if response.StatusCode != row.Status {
				t.Errorf("status %d source %d", response.StatusCode, row.Status)
			}
			for name, value := range map[string]*string{"Content-Type": row.ContentType, "Location": row.Location, "Allow": row.Allow} {
				want := ""
				if value != nil {
					want = *value
				}
				if value == nil {
					if _, exists := response.Header[http.CanonicalHeaderKey(name)]; exists {
						t.Errorf("%s must be absent, not an empty header", name)
					}
				}
				if response.Header.Get(name) != want {
					t.Errorf("%s %q source %q", name, response.Header.Get(name), want)
				}
			}
			wantBody, err := hex.DecodeString(row.BodyHex)
			if err != nil {
				t.Fatal(err)
			}
			if c.Method == http.MethodHead || len(wantBody) == 0 || row.ContentType == nil || *row.ContentType != "application/json" {
				if string(gotBody) != string(wantBody) {
					t.Errorf("exact body differs: %q vs %q", gotBody, wantBody)
				}
			} else if !reflect.DeepEqual(originalCenterComparableJSON(t, gotBody), originalCenterComparableJSON(t, wantBody)) {
				t.Errorf("full JSON body differs: %s vs %s", gotBody, wantBody)
			}
			out = append(out, map[string]any{"case": row.Case, "version": row.Version, "status": response.StatusCode, "body_hex": hex.EncodeToString(gotBody), "content_type": response.Header.Get("Content-Type"), "location": response.Header.Get("Location"), "allow": response.Header.Get("Allow"), "wire": gotCalls, "scope": map[string]any{"path": gotPath, "raw_path_hex": hex.EncodeToString([]byte(gotRawPath)), "query_hex": hex.EncodeToString([]byte(gotQuery))}})
		})
	}
	mu.Lock()
	count := len(calls)
	uidEqual := len(uids) > 0
	for _, uid := range uids {
		uidEqual = uidEqual && uid != "" && uid == uids[0]
	}
	mu.Unlock()
	if count != 52 || !uidEqual {
		t.Fatal("source peer count or stable UID differs", count)
	}
	if path := os.Getenv("OWNED_USAGE_QUERY_OBSERVATIONS"); path != "" {
		raw, _ := json.MarshalIndent(map[string]any{"observations": out, "peer_calls": count, "uid_present_equal": uidEqual}, "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Authentication/origin are enforced before redirect; no peer side effects.
	for _, v := range []string{"v1", "v2"} {
		for _, route := range []string{"statistic", "rating", "rating/Owned"} {
			path := "/api/" + v + "/plugin/" + route + "///?plugin_ids=%GG"
			for _, sample := range []struct {
				auth   bool
				origin string
				want   int
			}{{false, "", 401}, {true, "https://foreign.invalid", 403}} {
				r, _ := http.NewRequest("POST", host.URL+path, strings.NewReader(`{"rating":4.1}`))
				r.Host = "owned-asgi"
				if sample.auth {
					r.AddCookie(cookie)
				}
				r.Header.Set("Origin", sample.origin)
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != sample.want {
					t.Fatal("slash authority weakened", response.StatusCode)
				}
			}
		}
		for _, root := range []string{"rating", "statistic"} {
			for _, method := range []string{"PUT", "DELETE"} {
				for _, suffix := range []string{"", "/", "//", "///"} {
					r := httptest.NewRequest(method, "/api/"+v+"/plugin/"+root+suffix, nil)
					r.AddCookie(cookie)
					if a.serveOriginalPluginServerAPI(httptest.NewRecorder(), r) {
						t.Fatal("source rating adapter captured generic config/uninstall")
					}
				}
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != count {
		t.Fatal("denied slash requests reached peer")
	}
}
