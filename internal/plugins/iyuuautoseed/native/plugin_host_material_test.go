package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

type pluginCookieOracle struct {
	CookieCases []struct {
		Header string      `json:"header"`
		Pairs  [][2]string `json:"pairs"`
		Error  bool        `json:"error"`
	} `json:"cookie_cases"`
	HostnameCases []struct {
		URL      string  `json:"url"`
		Hostname *string `json:"hostname"`
	} `json:"hostname_cases"`
	SyncCases []struct {
		Version  string           `json:"version"`
		Mode     string           `json:"mode"`
		URL      string           `json:"url"`
		Cookie   string           `json:"cookie"`
		Existing []map[string]any `json:"existing"`
		Accepted bool             `json:"accepted"`
		Calls    []struct {
			Method    string                     `json:"method"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		} `json:"calls"`
	} `json:"sync_cases"`
}

func readPluginCookieOracle(t *testing.T) pluginCookieOracle {
	t.Helper()
	raw, err := os.ReadFile("../../../../testdata/plugin-host-downloader-20261031/cookie-source-oracle.json")
	var out pluginCookieOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || len(out.SyncCases) < 10 {
		t.Fatal("fixed-source cookie oracle unavailable", err)
	}
	return out
}
func TestPluginHostCookieParserAndHostnameFixedSource(t *testing.T) {
	oracle := readPluginCookieOracle(t)
	for i, scenario := range oracle.CookieCases {
		pairs, err := pluginHostCookiePairs(scenario.Header)
		if (err != nil) != scenario.Error || !scenario.Error && len(pairs) != len(scenario.Pairs) || len(pairs) > 0 && !reflect.DeepEqual(pairs, scenario.Pairs) {
			t.Fatal("CPython3.12.13 SimpleCookie changed at source case", i, pairs, err)
		}
	}
	for i, scenario := range oracle.HostnameCases {
		hostname, valid := pluginCookieHostname(scenario.URL)
		want := ""
		if scenario.Hostname != nil {
			want = *scenario.Hostname
		}
		if valid != (scenario.Hostname != nil && want != "") || hostname != want {
			t.Fatal("source urlparse.hostname changed", i, hostname, valid, want)
		}
	}
}

func TestPluginHostTransmissionStringActualVendorOracle(t *testing.T) {
	var oracle struct {
		Version string `json:"version"`
		Cases   []struct {
			Content  json.RawMessage `json:"content"`
			Accepted bool            `json:"accepted"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-host-downloader-20261031/transmission-vendor-add-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Version != "7.0.11" || len(oracle.Cases) != 16 {
		t.Fatal("actual deployed TR library oracle unavailable", err)
	}
	for _, test := range oracle.Cases {
		var content string
		if json.Unmarshal(test.Content, &content) != nil {
			continue
		}
		if got := pluginHostTransmissionStringValid(content); got != test.Accepted {
			t.Errorf("source parser accepts=%v target=%v content=%q", test.Accepted, got, content)
		}
	}
}
func TestPluginHostURLCookieSynchronizationFixedSource(t *testing.T) {
	oracle := readPluginCookieOracle(t)
	for i, scenario := range oracle.SyncCases {
		t.Run(scenario.Version+"/"+scenario.Mode+"/"+string(rune('a'+i)), func(t *testing.T) {
			calls := []string{}
			var cookieSet []map[string]any
			var addCookie *string
			var addURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					calls = append(calls, "app_web_api_version")
					if scenario.Mode == "version-failure" {
						http.Error(w, "owned failure", 500)
						return
					}
					_, _ = io.WriteString(w, scenario.Version)
				case "/api/v2/app/cookies":
					calls = append(calls, "app_cookies")
					if scenario.Mode == "read-failure" {
						http.Error(w, "owned failure", 500)
						return
					}
					_ = json.NewEncoder(w).Encode(scenario.Existing)
				case "/api/v2/app/setCookies":
					calls = append(calls, "app_set_cookies")
					_ = r.ParseForm()
					if json.Unmarshal([]byte(r.Form.Get("cookies")), &cookieSet) != nil {
						t.Error("cookie synchronization is not JSON list")
					}
					if scenario.Mode == "write-failure" {
						http.Error(w, "owned failure", 500)
					}
				case "/api/v2/torrents/add":
					calls = append(calls, "torrents_add")
					if r.ParseMultipartForm(1<<20) != nil {
						t.Error("invalid native add multipart")
					}
					addURL = r.FormValue("urls")
					if values, exists := r.MultipartForm.Value["cookie"]; exists {
						value := values[0]
						addCookie = &value
					}
					_, _ = io.WriteString(w, "Ok.")
				default:
					t.Error("URL/cookie adapter dispatched unrecognized API")
					http.Error(w, "unexpected", 400)
				}
			}))
			defer server.Close()
			client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			save := "/owned/downloads"
			out, err := client.PluginHostAddMaterial(context.Background(), IYUUAddSpec{SavePath: &save, Tags: []string{"owned"}, IgnoreCategoryCheck: true}, true, nil, nil, &scenario.URL, &scenario.Cookie)
			if err != nil || out.Accepted != scenario.Accepted {
				t.Fatal("source URL/cookie add outcome changed", err, out)
			}
			expectedCalls := []string{}
			var expectedCookie *string
			var expectedSet []map[string]any
			for _, call := range scenario.Calls {
				expectedCalls = append(expectedCalls, call.Method)
				if call.Method == "torrents_add" {
					if string(call.Arguments["cookie"]) != "null" {
						var value string
						_ = json.Unmarshal(call.Arguments["cookie"], &value)
						expectedCookie = &value
					}
				}
				if call.Method == "app_set_cookies" {
					_ = json.Unmarshal(call.Arguments["cookies"], &expectedSet)
				}
			}
			if !reflect.DeepEqual(calls, expectedCalls) || !reflect.DeepEqual(addCookie, expectedCookie) || !reflect.DeepEqual(cookieSet, expectedSet) || len(calls) > 0 && calls[len(calls)-1] == "torrents_add" && addURL != scenario.URL {
				t.Fatal("source dispatch order, raw URL, cookie fallback or merge changed", calls, expectedCalls)
			}
		})
	}
}
func TestPluginHostCookieGlobalWritePreservesMutationProof(t *testing.T) {
	revoked := false
	writes, adds := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.3")
		case "/api/v2/app/cookies":
			_, _ = io.WriteString(w, `[]`)
		case "/api/v2/app/setCookies":
			writes++
			revoked = true
		case "/api/v2/torrents/add":
			adds++
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{Guard: func(context.Context) error {
		if revoked {
			return io.ErrClosedPipe
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	address, cookie := "https://tracker.invalid/file?passkey=OWNED", "uid=OWNED_COOKIE"
	out, err := client.PluginHostAddMaterial(context.Background(), IYUUAddSpec{IgnoreCategoryCheck: true}, false, nil, nil, &address, &cookie)
	if err == nil || MutationNotDispatched(err) || out.Outcome != "unknown" || writes != 1 || adds != 0 || strings.Contains(err.Error(), "OWNED_COOKIE") {
		t.Fatal("cookie mutation proof was reset or add dispatched after revocation", err, out, writes, adds)
	}
}
