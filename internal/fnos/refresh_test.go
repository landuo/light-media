package fnos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestServiceRefreshPagedSourceCompletesBeforeAnyWrite(t *testing.T) {
	requests := []string{}
	read, committed := 0, false
	adapter := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if !committed || read != 27 {
			t.Error("refresh began before all pages and the durable boundary")
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		fmt.Fprint(w, `{"code":0,"data":true}`)
	})
	result, err := adapter.RefreshLibraryBatches(context.Background(), func(ctx context.Context, libraries []ServiceLibrary) ([]string, error) {
		read++
		if read == 27 {
			return nil, nil
		}
		paths := make([]string, 200)
		for i := range paths {
			paths[i] = fmt.Sprintf("/vol2/media/page-%d/file-%d.mkv", read, i)
		}
		return paths, nil
	}, func() error {
		committed = true
		return nil
	})
	if err != nil || result.AllLibraries || !reflect.DeepEqual(result.LibraryGUIDs, []string{serviceLibGUID}) || !reflect.DeepEqual(requests, []string{"GET /v/api/v1/task/running", "POST /v/api/v1/mdb/scan/" + serviceLibGUID}) {
		t.Fatal("paged refresh truncated, duplicated or scanned early", result, requests, err)
	}
}

func TestServiceRefreshLateSourceAndBoundaryFailuresPreventRemoteWrites(t *testing.T) {
	for _, failure := range []string{"page", "invalid_path", "boundary"} {
		t.Run(failure, func(t *testing.T) {
			adapter := testService(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("failed source/boundary reached a scan endpoint", r.URL.Path)
			})
			reads, boundaries := 0, 0
			injected := errors.New("injected persistence failure")
			_, err := adapter.RefreshLibraryBatches(context.Background(), func(context.Context, []ServiceLibrary) ([]string, error) {
				reads++
				if reads == 1 {
					return []string{"/vol2/unmatched/first.mkv"}, nil
				}
				if failure == "page" {
					return nil, injected
				}
				if failure == "invalid_path" {
					return []string{"relative"}, nil
				}
				return nil, nil
			}, func() error {
				boundaries++
				return injected
			})
			if failure == "invalid_path" {
				serviceErrorCode(t, err, "invalid_input")
			} else if !errors.Is(err, injected) {
				t.Fatal("source/boundary error was hidden", err)
			}
			if reads != 2 || (failure != "boundary" && boundaries != 0) || (failure == "boundary" && boundaries != 1) {
				t.Fatal("unmatched fallback skipped source validation", reads, boundaries)
			}
		})
	}
}

func TestServiceRefreshDeduplicatesLibraryAndUsesEmptySignedPost(t *testing.T) {
	requests := []string{}
	adapter := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 || r.Header.Get("Content-Type") != "application/json" {
				t.Error("original empty refresh POST body changed")
			}
		}
		fmt.Fprint(w, `{"code":0,"data":true}`)
	})
	result, err := adapter.RefreshLibrariesByPaths(context.Background(), []string{"/vol2/media/电影/a.mp4", "/vol2/media/电影/b.mp4", "/vol2/media/电视剧/s01/a.mkv"})
	if err != nil || result.AllLibraries || !reflect.DeepEqual(result.LibraryGUIDs, []string{serviceLibGUID}) || !reflect.DeepEqual(requests, []string{"GET /v/api/v1/task/running", "POST /v/api/v1/mdb/scan/" + serviceLibGUID}) {
		t.Fatal("library refresh did not deduplicate or follow original protocol", result, requests, err)
	}
}

func TestServiceRefreshUnmatchedPathUsesOriginalScanAllFallback(t *testing.T) {
	requests := []string{}
	adapter := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		fmt.Fprint(w, `{"code":0,"data":true}`)
	})
	result, err := adapter.RefreshLibrariesByPaths(context.Background(), []string{"/vol2/other/file.mp4"})
	if err != nil || !result.AllLibraries || !reflect.DeepEqual(requests, []string{"GET /v/api/v1/task/running", "POST /v/api/v1/mdb/scanall"}) {
		t.Fatal("unmatched path fallback differs", result, requests, err)
	}
}

func TestServiceRefreshRejectedSingleScanFallsBackButFalseSuccessFails(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			requests := []string{}
			adapter := testService(t, func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.URL.Path == "/v/api/v1/mdb/scanall" {
					fmt.Fprintf(w, `{"code":0,"data":%t}`, accepted)
				} else if r.URL.Path == "/v/api/v1/task/running" {
					fmt.Fprint(w, `{"code":0,"data":null}`)
				} else {
					fmt.Fprint(w, `{"code":0,"data":false}`)
				}
			})
			result, err := adapter.RefreshLibrariesByPaths(context.Background(), []string{"/vol2/media/movie/file.mp4"})
			if accepted && (err != nil || !result.AllLibraries) {
				t.Fatal("rejected single-library scan did not fall back", result, err)
			}
			if !accepted {
				serviceErrorCode(t, err, "scan_rejected")
			}
			if !reflect.DeepEqual(requests, []string{"/v/api/v1/task/running", "/v/api/v1/mdb/scan/" + serviceLibGUID, "/v/api/v1/task/running", "/v/api/v1/mdb/scanall"}) {
				t.Fatal("fallback preflight sequence changed", requests)
			}
		})
	}
}

func TestServiceRefreshRejectsInvalidInputsWithoutNetwork(t *testing.T) {
	adapter := testService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid path reached an endpoint")
	})
	for _, paths := range [][]string{nil, {"relative"}, {"/"}, {"/vol2/media/../outside/file"}, {"/vol2/media/file\x00"}} {
		_, err := adapter.RefreshLibrariesByPaths(context.Background(), paths)
		serviceErrorCode(t, err, "invalid_input")
	}
}

func TestServiceRefreshDataTruthinessMatchesOriginalResponse(t *testing.T) {
	for _, raw := range []string{`true`, `"ok"`, `1`, `1e999`, `[false]`, `{"accepted":false}`} {
		if !serviceTruthyData(json.RawMessage(raw)) {
			t.Fatal("original truthy result rejected", raw)
		}
	}
	for _, raw := range []string{`false`, `""`, `0`, `-0.0e99`, `[]`, `{}`, `null`} {
		if serviceTruthyData(json.RawMessage(raw)) {
			t.Fatal("original falsy result reported accepted", raw)
		}
	}
}

func TestServiceRefreshUsesOriginalServerLibraryOrderAndRequiresAdmin(t *testing.T) {
	for _, admin := range []int{0, 1} {
		t.Run(fmt.Sprint(admin), func(t *testing.T) {
			scans := 0
			adapter, err := newService(Config{Host: "https://nas.example:8080/v", Username: "synthetic-user", Password: "synthetic-password"}, serviceTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				switch r.URL.Path {
				case "/v/api/v1/login":
					body = `{"code":0,"data":{"token":"synthetic-session-token"}}`
				case "/v/api/v1/user/info":
					body = fmt.Sprintf(`{"code":0,"data":{"is_admin":%d}}`, admin)
				case "/v/api/v1/mdb/list":
					body = fmt.Sprintf(`{"code":0,"data":[{"guid":%q,"name":"Z parent first","category":"Mix","dir_list":["/media/"]},{"guid":%q,"name":"A child second","category":"Mix","dir_list":["/media/movies/"]}]}`, serviceLibGUID, serviceTVGUID)
				case "/v/api/v1/task/running":
					body = `{"code":0,"data":null}`
				case "/v/api/v1/mdb/scan/" + serviceLibGUID:
					scans++
					body = `{"code":0,"data":true}`
				default:
					t.Error("unexpected or reordered library scan", r.URL.Path)
					body = `{"code":-1}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.RefreshLibrariesByPaths(context.Background(), []string{"/media/movies/a.mkv"})
			if admin == 0 {
				serviceErrorCode(t, err, "unauthorized")
				if scans != 0 {
					t.Fatal("non-admin requested a refresh")
				}
			} else if err != nil || scans != 1 || !reflect.DeepEqual(result.LibraryGUIDs, []string{serviceLibGUID}) {
				t.Fatal("refresh used a different overlap strategy from original", result, scans, err)
			}
		})
	}
}
