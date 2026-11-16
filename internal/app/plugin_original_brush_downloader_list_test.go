package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	raw, err := json.Marshal(plugins.DownloaderListRequestV1{DownloaderID: "qbittorrent-0"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.originalHostDownloaderResource(context.Background(), scope, plugins.DownloaderListCapability, raw)
	if err != nil {
		t.Fatalf("check-phase list rejected qBittorrent 5 source login: %v", err)
	}
	var tasks plugins.DownloaderTasksV1
	if json.Unmarshal(result, &tasks) != nil || len(tasks.Tasks) != 1 || tasks.Tasks[0].Hash != hash || !login || !listed {
		t.Fatalf("check-phase list did not preserve the exact daemon task: login=%v listed=%v tasks=%v %v", login, listed, tasks.Tasks, err)
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
