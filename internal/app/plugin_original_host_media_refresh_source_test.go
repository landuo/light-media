package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHostMediaRefreshSourceSemanticsActualSDK(t *testing.T) {
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case          string    `json:"case"`
			Paths         []*string `json:"paths"`
			Directories   []string  `json:"directories"`
			Authenticated bool      `json:"authenticated"`
			Admin         bool      `json:"admin"`
			Partial       bool      `json:"partial_result"`
			All           bool      `json:"all_result"`
			Root          bool      `json:"root"`
			Result        *bool     `json:"result"`
			Trace         []string  `json:"trace"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/media-refresh-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 20 {
		t.Fatal("fixed TrimeMedia source oracle", err)
	}
	a, _, object := ownedHostServicesFixture(t)
	const guid = "11111111111111111111111111111111"
	folder := t.TempDir()
	library := filepath.Join(folder, "library")
	if err := os.MkdirAll(filepath.Join(library, "nested"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(library, "nested"), filepath.Join(folder, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(folder, "loop"), filepath.Join(folder, "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, `lib\name`), 0750); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(cwd, folder)
	if err != nil {
		t.Fatal(err)
	}
	resolveToken := func(value string) string {
		value = strings.ReplaceAll(value, "$relative_root", relativeRoot)
		return strings.ReplaceAll(value, "$root", folder)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			var mu sync.Mutex
			trace := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v/api/v1/sys/version":
					fmt.Fprint(w, `{"code":0,"data":{"version":"1.0.0","mediasrvVersion":"1.0.0"}}`)
				case "/v/api/v1/login":
					if sample.Authenticated {
						fmt.Fprint(w, `{"code":0,"data":{"token":"owned-session"}}`)
					} else {
						fmt.Fprint(w, `{"code":1,"data":null}`)
					}
				case "/v/api/v1/user/info":
					admin := 0
					if sample.Admin {
						admin = 1
					}
					fmt.Fprintf(w, `{"code":0,"data":{"is_admin":%d}}`, admin)
				case "/v/api/v1/mdb/list":
					directories := make([]string, len(sample.Directories))
					for index, directory := range sample.Directories {
						directories[index] = resolveToken(directory)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": []any{map[string]any{"guid": guid, "name": "Owned", "category": "Mix", "dir_list": directories}}})
				case "/v/api/v1/task/running":
					mu.Lock()
					trace = append(trace, "preflight")
					mu.Unlock()
					fmt.Fprint(w, `{"code":0,"data":false}`)
				case "/v/api/v1/mdb/scan/" + guid, "/v/api/v1/mdb/scanall":
					body, _ := io.ReadAll(r.Body)
					if r.Method != "POST" || len(body) != 0 || r.Header.Get("Content-Type") != "application/json" {
						t.Error("fixed fnapi.request empty signed POST", r.Method, string(body), r.Header.Get("Content-Type"))
					}
					mu.Lock()
					kind := "scan:owned-guid"
					accepted := sample.Partial
					if strings.HasSuffix(r.URL.Path, "scanall") {
						kind = "scanall"
						accepted = sample.All
					}
					trace = append(trace, kind)
					mu.Unlock()
					fmt.Fprintf(w, `{"code":0,"data":%t}`, accepted)
				default:
					t.Error("unexpected media endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			config := a.Settings()
			config.FNOS = FNConfig{Host: server.URL, Username: "owned-user", Password: "owned-synthetic-password", Enabled: true}
			if err := a.saveSettings(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			request := plugins.MediaRefreshRequest{Name: "fnos", Root: sample.Root}
			if !sample.Root {
				request.Items = []plugins.MediaRefreshItem{}
				for _, path := range sample.Paths {
					item := plugins.MediaRefreshItem{}
					if path != nil {
						value := resolveToken(*path)
						item.TargetPath = &value
					}
					request.Items = append(request.Items, item)
				}
			}
			observed, err := ownedHostServicesCall(t, object, plugins.MediaServersRefreshCapability, request)
			if err != nil {
				t.Fatal("actual SDK source refresh", err)
			}
			var result plugins.MediaRefreshResult
			if json.Unmarshal(observed, &result) != nil {
				t.Fatal(string(observed))
			}
			if !reflect.DeepEqual(result.Result, sample.Result) || result.Accepted != (sample.Result != nil && *sample.Result) {
				t.Fatal("fixed source None/False/True", sample.Result, string(observed))
			}
			mu.Lock()
			actual := append([]string{}, trace...)
			mu.Unlock()
			if !reflect.DeepEqual(actual, sample.Trace) {
				t.Fatal("fixed source operation order and side effects", sample.Trace, actual)
			}
		})
	}
}
