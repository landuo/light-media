package fnos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const serviceLibGUID = "11111111111111111111111111111111"
const serviceMovieGUID = "22222222222222222222222222222222"
const serviceTVGUID = "33333333333333333333333333333333"
const serviceSeasonGUID = "44444444444444444444444444444444"
const serviceEpisodeGUID = "55555555555555555555555555555555"

type serviceTransport func(*http.Request) (*http.Response, error)

func (f serviceTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testService(t *testing.T, action http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		request.Body = io.NopCloser(strings.NewReader(string(body)))
		signed := string(body)
		if request.URL.Query().Has("q") {
			signed = "q=" + request.URL.Query().Get("q")
		}
		headers, err := url.ParseQuery(request.Header.Get("authx"))
		if err != nil {
			t.Error(err)
		}
		if request.Header.Get("authx") != Signature(request.URL.Path, signed, headers.Get("nonce"), headers.Get("timestamp")) {
			t.Errorf("invalid signature for %s", request.URL.Path)
		}
		if len(headers.Get("nonce")) != 6 {
			t.Error("invalid nonce")
		}
		switch request.URL.Path {
		case "/v/api/v1/login":
			if request.Method != "POST" || !strings.Contains(string(body), `"password":"synthetic-password"`) {
				t.Error("incorrect login")
			}
			fmt.Fprint(writer, `{"code":0,"data":{"token":"synthetic-session-token"}}`)
		case "/v/api/v1/user/info":
			if request.Header.Get("Authorization") != "synthetic-session-token" {
				t.Error("missing in-memory authentication")
			}
			fmt.Fprint(writer, `{"code":0,"data":{"is_admin":1}}`)
		case "/v/api/v1/mdb/list":
			fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"name":"影视库","category":"Mix","dir_list":["/vol2/media/"]}]}`, serviceLibGUID)
		default:
			action(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	adapter, err := newService(Config{Host: "https://nas.example:8080/v", Username: "synthetic-user", Password: "synthetic-password"}, serviceTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "nas.example:8080" {
			t.Error("request left configured HTTPS origin")
		}
		clone := request.Clone(request.Context())
		copy := *request.URL
		clone.URL = &copy
		clone.URL.Host, clone.URL.Scheme = target.Host, target.Scheme
		return server.Client().Transport.RoundTrip(clone)
	}))
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func serviceErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var result *ServiceError
	if !errors.As(err, &result) || result.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
	if strings.Contains(fmt.Sprintf("%v %#v", err, err), "synthetic-password") || strings.Contains(fmt.Sprint(err), "https://") {
		t.Fatal("error leaked credential or address")
	}
}

func TestServiceLibrariesAndScopedScan(t *testing.T) {
	var scans int
	adapter := testService(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "POST" || request.URL.Path != "/v/api/v1/mdb/scan/"+serviceLibGUID {
			t.Errorf("unexpected writer: %s %s", request.Method, request.URL.Path)
		}
		var payload struct {
			Dirs []string `json:"dir_list"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || len(payload.Dirs) != 1 || payload.Dirs[0] != "/vol2/media/电影/作品 (2026)" {
			t.Error("scan not restricted to exact new directory")
		}
		scans++
		fmt.Fprint(writer, `{"code":0,"data":null}`)
	})
	libraries, err := adapter.Libraries(context.Background())
	if err != nil || len(libraries) != 1 || libraries[0].GUID != serviceLibGUID || libraries[0].Directories[0] != "/vol2/media" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
	if err := adapter.Scan(context.Background(), serviceLibGUID, "/vol2/media/电影/作品 (2026)"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/vol2/media", "/vol2/media-other/movie", "/vol2/media/../outside", "/", "relative", "/vol2/media/movie/"} {
		if err := adapter.Scan(context.Background(), serviceLibGUID, target); err == nil {
			t.Fatalf("unsafe scan accepted: %q", target)
		}
	}
	if scans != 1 {
		t.Fatalf("issued %d scans", scans)
	}
}

func TestServiceMovieVerificationNeedsActualPath(t *testing.T) {
	for _, test := range []struct{ name, file, parent, code string }{
		{"matched", "/vol2/media/夜王 (2026)/夜王 (2026).mkv", "", ""},
		{"wrong-file", "/vol2/media/夜王 (2025)/夜王 (2025).mkv", "", "needs_attention"},
		{"outside-root", "/vol2/media-outside/file.mkv", "", "needs_attention"},
		{"wrong-parent", "/vol2/media/夜王 (2026)/夜王 (2026).mkv", serviceTVGUID, "needs_attention"},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := testService(t, func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v/api/v1/search/list":
					if request.URL.Query().Get("q") != "夜王" {
						t.Error("incorrect exact query")
					}
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"type":"Movie","title":"夜王"}]}`, serviceMovieGUID)
				case "/v/api/v1/item/" + serviceMovieGUID:
					fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":%q,"type":"Movie","title":"夜王"}}`, serviceMovieGUID, serviceLibGUID, test.parent)
				case "/v/api/v1/media/itemfile/" + serviceMovieGUID:
					fmt.Fprintf(writer, `{"code":0,"data":[{"item_guid":%q,"file_path":%q}]}`, serviceMovieGUID, test.file)
				default:
					t.Errorf("unexpected route %s", request.URL.Path)
				}
			})
			guid, err := adapter.Verify(context.Background(), serviceLibGUID, "夜王", "Movie", "/vol2/media/夜王 (2026)/夜王 (2026).mkv")
			if test.code != "" {
				serviceErrorCode(t, err, test.code)
				if guid != "" {
					t.Fatal("returned visible GUID without proof")
				}
			} else if err != nil || guid != serviceMovieGUID {
				t.Fatalf("guid=%q err=%v", guid, err)
			}
		})
	}
}

func TestServiceEpisodeRequiresActualParentChainAndNumbers(t *testing.T) {
	for _, wrong := range []string{"", "season-title-omitted", "season-parent", "episode-parent", "episode-number", "file-relation"} {
		t.Run("reject-"+wrong, func(t *testing.T) {
			adapter := testService(t, func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v/api/v1/search/list":
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"type":"TV","title":"诛仙"}]}`, serviceTVGUID)
				case "/v/api/v1/item/" + serviceTVGUID:
					fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"TV","title":"诛仙"}}`, serviceTVGUID, serviceLibGUID)
				case "/v/api/v1/season/list/" + serviceTVGUID:
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"season_number":4}]}`, serviceSeasonGUID)
				case "/v/api/v1/item/" + serviceSeasonGUID:
					parent := serviceTVGUID
					if wrong == "season-parent" {
						parent = serviceMovieGUID
					}
					title := "第 4 季"
					if wrong == "season-title-omitted" {
						title = ""
					}
					fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":%q,"type":"Season","title":%q,"season_number":4}}`, serviceSeasonGUID, serviceLibGUID, parent, title)
				case "/v/api/v1/episode/list/" + serviceSeasonGUID:
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"episode_number":5}]}`, serviceEpisodeGUID)
				case "/v/api/v1/item/" + serviceEpisodeGUID:
					parent, episode := serviceSeasonGUID, 5
					if wrong == "episode-parent" {
						parent = serviceTVGUID
					}
					if wrong == "episode-number" {
						episode = 6
					}
					fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":%q,"type":"Episode","title":"偶遇小白","season_number":4,"episode_number":%d}}`, serviceEpisodeGUID, serviceLibGUID, parent, episode)
				case "/v/api/v1/media/itemfile/" + serviceEpisodeGUID:
					item := serviceEpisodeGUID
					if wrong == "file-relation" {
						item = serviceMovieGUID
					}
					fmt.Fprintf(writer, `{"code":0,"data":[{"item_guid":%q,"file_path":"/vol2/media/诛仙/Season 4/诛仙 S04E05.mkv"}]}`, item)
				default:
					t.Errorf("unexpected route: %s", request.URL.Path)
				}
			})
			guid, err := adapter.Verify(context.Background(), serviceLibGUID, "诛仙", "Episode", "/vol2/media/诛仙/Season 4/诛仙 S04E05.mkv")
			if wrong != "" && wrong != "season-title-omitted" {
				serviceErrorCode(t, err, "needs_attention")
			} else if err != nil || guid != serviceEpisodeGUID {
				t.Fatalf("guid=%q err=%v", guid, err)
			}
		})
	}
}

func TestServiceRequestLimitNeverReturnsEarlySuccess(t *testing.T) {
	var reads int
	adapter := testService(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v/api/v1/search/list" {
			rows := []map[string]string{}
			for i := 1; i <= 10; i++ {
				rows = append(rows, map[string]string{"guid": fmt.Sprintf("%032x", i+100), "type": "Movie", "title": "Same title"})
			}
			json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": rows})
			return
		}
		guid := pathSuffix(request.URL.Path)
		if strings.Contains(request.URL.Path, "/media/itemfile/") {
			reads++
			file := "/vol2/media/other/" + guid + ".mkv"
			if reads == 1 {
				file = "/vol2/media/movie/movie.mkv"
			}
			fmt.Fprintf(writer, `{"code":0,"data":[{"file_path":%q}]}`, file)
		} else {
			fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"Movie","title":"Same title"}}`, guid, serviceLibGUID)
		}
	})
	guid, err := adapter.Verify(context.Background(), serviceLibGUID, "Same title", "Movie", "/vol2/media/movie/movie.mkv")
	serviceErrorCode(t, err, "request_limit")
	if guid != "" || reads != 8 {
		t.Fatalf("unproven uniqueness returned guid=%q reads=%d", guid, reads)
	}
}

func pathSuffix(value string) string { return value[strings.LastIndex(value, "/")+1:] }

func TestServiceTLSConfigAndRedirectProtection(t *testing.T) {
	for _, host := range []string{"http://nas.example/v", "https://user:password@nas.example/v", "https://nas.example/v?token=x", "https://nas.example/wrong/path"} {
		if _, err := NewService(Config{Host: host, Username: "u", Password: "p"}); err == nil {
			t.Fatalf("invalid host accepted: %s", host)
		}
	}
	var hits int
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer other.Close()
	adapter := testService(t, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, other.URL, http.StatusFound)
	})
	_, err := adapter.Verify(context.Background(), serviceLibGUID, "x", "Movie", "/vol2/media/x/x.mkv")
	serviceErrorCode(t, err, "redirect_blocked")
	if hits != 0 {
		t.Fatal("credentials followed redirect")
	}
}

func TestServiceConcurrentActionsHaveIndependentSessions(t *testing.T) {
	adapter := testService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := adapter.Libraries(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if strings.Contains(fmt.Sprintf("%v %#v", adapter, adapter), "synthetic-password") {
		t.Fatal("format leaked configuration")
	}
}

func TestServiceSignatureTimestampAndRequestDeadline(t *testing.T) {
	adapter := testService(t, func(writer http.ResponseWriter, request *http.Request) {
		auth, _ := url.ParseQuery(request.Header.Get("authx"))
		ms, _ := strconv.ParseInt(auth.Get("timestamp"), 10, 64)
		if time.Since(time.UnixMilli(ms)) > time.Minute {
			t.Error("stale signature")
		}
		fmt.Fprint(writer, `{"code":1,"data":"synthetic-password"}`)
	})
	_, err := adapter.Verify(context.Background(), serviceLibGUID, "x", "Movie", "/vol2/media/x/x.mkv")
	serviceErrorCode(t, err, "unavailable")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.Libraries(ctx)
	serviceErrorCode(t, err, "canceled")
}
