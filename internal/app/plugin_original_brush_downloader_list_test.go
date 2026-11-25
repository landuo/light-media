package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// BrushFlow's check and promotion_expiry callbacks read the daemon through the
// same DownloaderList capability as the brush run. qBittorrent 5 answers
// auth/login with 204 and a QBT_SID_<port> cookie; the legacy login path used
// to reject both after the phase check limited the source login to "brush".
// A failed list read silently disabled promotion protection, so this covers
// the exact non-brush phase that let free downloads expire.
func TestOriginalDownloaderListBrushCheckAcceptsQBittorrent5Login(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	var login, listed bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if r.Method != http.MethodPost {
				t.Error("login method", r.Method)
			}
			login = true
			w.Header().Set("Set-Cookie", "QBT_SID_19080=owned-session; Path=/; HttpOnly")
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/torrents/info":
			cookie, err := r.Cookie("QBT_SID_19080")
			if err != nil || cookie.Value != "owned-session" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			listed = true
			task := workflowTask(hash, .5)
			task["state"], task["tags"] = "downloading", "刷流"
			task["time_active"], task["seeding_time"] = 408, 12
			workflowJSON(w, []any{task})
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer peer.Close()

	enabled := true
	a := &App{}
	a.config.Downloaders = []DownloaderConfig{{
		Config: download.Config{ID: "qbittorrent-0", Kind: "qbittorrent", BaseURL: peer.URL, Username: "owned-user", Password: "owned-password"},
		Name:   "qb", Enabled: &enabled,
	}}
	object := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: "BrushFlowLanduo"}}
	scope := originalBaseCallScope{object: object, installation: object.installation, phase: "check", validate: func(context.Context) error { return nil }}
	for _, optedIn := range []bool{false, true} {
		request := plugins.DownloaderListRequestV1{DownloaderID: "qbittorrent-0", IncludeActivityTime: optedIn}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.originalHostDownloaderResource(context.Background(), scope, plugins.DownloaderListCapability, raw)
		if err != nil {
			t.Fatal(err)
		}
		var tasks plugins.DownloaderTasksV1
		if json.Unmarshal(result, &tasks) != nil || len(tasks.Tasks) != 1 || tasks.Tasks[0].Hash != hash || !login || !listed {
			t.Fatalf("check-phase list lost the daemon task: login=%v listed=%v tasks=%v", login, listed, tasks.Tasks)
		}
		qb := tasks.Tasks[0].QBittorrent
		if qb == nil || qb.SeedingTime == nil || qb.SeedingTime.String() != "12" || (qb.TimeActive != nil) != optedIn {
			t.Fatalf("activity projection for opt-in %t: %+v", optedIn, qb)
		}
		if optedIn && qb.TimeActive.String() != "408" {
			t.Fatal(qb.TimeActive)
		}
	}
}

func TestOriginalBrushQBSourceLoginSelection(t *testing.T) {
	enabled := true
	a := &App{}
	a.config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "qb", Kind: "qb"}, Name: "qb", Enabled: &enabled},
		{Config: download.Config{ID: "qbx", Kind: "qbittorrent"}, Name: "qbx", Enabled: &enabled},
		{Config: download.Config{ID: "tr", Kind: "transmission"}, Name: "tr", Enabled: &enabled},
	}
	brush := &originalPluginInstance{installation: PluginInstallation{PluginID: "BrushFlowLanduo"}}
	other := &originalPluginInstance{installation: PluginInstallation{PluginID: "Other"}}
	for _, sample := range []struct {
		name  string
		scope originalBaseCallScope
		id    string
		want  bool
	}{
		{"brush-qb", originalBaseCallScope{object: brush, phase: "check"}, "qb", true},
		{"brush-qbittorrent", originalBaseCallScope{object: brush, phase: "promotion_expiry"}, "qbx", true},
		{"brush-transmission", originalBaseCallScope{object: brush, phase: "check"}, "tr", false},
		{"other-qb", originalBaseCallScope{object: other, phase: "check"}, "qb", false},
		{"missing", originalBaseCallScope{object: brush, phase: "check"}, "absent", false},
	} {
		if got := a.originalBrushQBSourceLogin(sample.scope, sample.id); got != sample.want {
			t.Fatalf("%s: source login = %v, want %v", sample.name, got, sample.want)
		}
	}
}

func TestActivityTimeProjectionRejectsInvalidCountersPerTorrent(t *testing.T) {
	raw := json.RawMessage(`[{"hash":"0123456789abcdef0123456789abcdef01234567","time_active":"14000","seeding_time":false},{"hash":"1123456789abcdef0123456789abcdef01234567","time_active":0,"seeding_time":0}]`)
	selection := originalDownloaderTaskSelection{daemonOrder: true, includeActivityTime: true}
	rows, err := selection.project("qbittorrent", raw, false, false)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %v: %v", rows, err)
	}
	bad, zero := rows[0].QBittorrent, rows[1].QBittorrent
	if bad.TimeActive != nil || bad.SeedingTime != nil || zero.TimeActive == nil || zero.TimeActive.String() != "0" {
		t.Fatalf("invalid and zero clocks: %+v %+v", bad, zero)
	}
}

func TestOriginalDownloaderListTransmissionActivityTime(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprintf("modern=%t", modern), func(t *testing.T) {
			hashKey, clockKey := "hashString", "secondsDownloading"
			if modern {
				hashKey, clockKey = "hash_string", "seconds_downloading"
			}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var msg struct {
					ID     int    `json:"id"`
					Tag    int    `json:"tag"`
					Method string `json:"method"`
					Params struct {
						Fields []string `json:"fields"`
					} `json:"params"`
					Args struct {
						Fields []string `json:"fields"`
					} `json:"arguments"`
				}
				if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
					t.Error(err)
					return
				}
				if !modern && msg.Method == "session_get" {
					workflowJSON(w, map[string]any{"result": "method name not recognized", "arguments": map[string]any{}})
					return
				}
				result := map[string]any{"version": "4.1"}
				if msg.Method == "torrent_get" || msg.Method == "torrent-get" {
					fields := msg.Args.Fields
					if modern {
						fields = msg.Params.Fields
					}
					if !slices.Contains(fields, clockKey) {
						t.Errorf("clock not requested: %v", fields)
					}
					result = map[string]any{"torrents": []any{map[string]any{hashKey: ownedDownloaderHash, clockKey: 14200}}}
				}
				if modern {
					workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
				} else {
					workflowJSON(w, map[string]any{"tag": msg.Tag, "result": "success", "arguments": result})
				}
			}))
			defer peer.Close()
			enabled := true
			a := &App{}
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "tr", Kind: "transmission", BaseURL: peer.URL}, Enabled: &enabled}}
			scope := originalBaseCallScope{validate: func(context.Context) error { return nil }}
			raw := json.RawMessage(`{"downloader_id":"tr","include_activity_time":true}`)
			result, err := a.originalHostDownloaderResource(context.Background(), scope, plugins.DownloaderListCapability, raw)
			if err != nil {
				t.Fatal(err)
			}
			var tasks plugins.DownloaderTasksV1
			if err := json.Unmarshal(result, &tasks); err != nil || len(tasks.Tasks) != 1 {
				t.Fatalf("tasks %v err %v", tasks, err)
			}
			tr := tasks.Tasks[0].Transmission
			if tr == nil || tr.SecondsDownloading == nil || tr.SecondsDownloading.String() != "14200" {
				t.Fatalf("counter: %+v", tr)
			}
		})
	}
}
