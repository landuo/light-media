package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// These causal tests use a real separately installed standard SDK process.
// The owned HTTP peer commits an observable synthetic effect before revoking
// the current binding; zero/confirmed/ambiguous/partial are checked separately.
func TestHostDownloaderPostguardPreservesActualSDKMutationReceipts(t *testing.T) {
	for _, mode := range []string{"readonly_revoked", "cookie_write_revoked", "add_confirmed_revoked", "add_unknown_revoked", "control_confirmed_revoked", "delete_tags_confirmed_revoked", "category_partial_revoked", "category_partial_remote_failure"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			var mu sync.Mutex
			writes := []string{}
			revoke := func() {
				a.cfgMu.Lock()
				value := false
				a.config.Downloaders[0].Enabled = &value
				a.cfgMu.Unlock()
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/api/v2/")
				switch path {
				case "app/webapiVersion":
					if mode == "readonly_revoked" {
						revoke()
					}
					_, _ = io.WriteString(w, "2.11.4")
				case "app/cookies":
					_, _ = io.WriteString(w, `[]`)
				case "app/setCookies", "torrents/add", "torrents/stop", "torrents/deleteTags", "torrents/setCategory":
					_ = r.ParseForm()
					value := path
					if path == "torrents/setCategory" {
						value += ":" + r.Form.Get("hashes")
					}
					mu.Lock()
					writes = append(writes, value)
					count := len(writes)
					mu.Unlock()
					if mode != "category_partial_remote_failure" {
						revoke()
					}
					if mode == "add_unknown_revoked" || mode == "category_partial_remote_failure" && count == 2 {
						http.Error(w, "OWNED_PRIVATE_RESPONSE_NOT_FOR_PLUGIN", 403)
						return
					}
					if path == "torrents/add" {
						_, _ = io.WriteString(w, `{"added_torrent_ids":{"`+ownedDownloaderHash+`":{}},"errors":{}}`)
					}
				default:
					t.Error("unexpected endpoint", path)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer server.Close()
			a.cfgMu.Lock()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
			a.cfgMu.Unlock()
			method := "add"
			var request any = plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), Paused: true}
			switch mode {
			case "readonly_revoked", "cookie_write_revoked":
				address, cookie := "https://owned.invalid/material?passkey=OWNED_QUERY", "owned=OWNED_COOKIE"
				request = plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &address, Cookie: &cookie, Paused: true}
			case "control_confirmed_revoked":
				method = "control"
				request = plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "pause"}
			case "delete_tags_confirmed_revoked":
				method = "delete_tags"
				request = plugins.DownloaderDeleteTagsRequestV1{DownloaderID: "owned", Tags: []string{"owned"}}
			case "category_partial_revoked", "category_partial_remote_failure":
				category := "owned"
				method = "control"
				request = plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash, strings.Repeat("b", 40)}, Action: "set_category", Category: &category}
			}
			var out plugins.DownloaderMutationV1
			err := hostDownloaderCall(t, object, method, request, &out)
			mu.Lock()
			observed := append([]string{}, writes...)
			mu.Unlock()
			if mode == "readonly_revoked" {
				if err == nil || len(observed) != 0 || out.FailureCode != "" {
					t.Fatal("read-only revocation fabricated a write", out, err, observed)
				}
				return
			}
			if err != nil {
				t.Fatal("outer host discarded causal receipt", out, err, observed)
			}
			wantCode := "CALL_CHANGED_AFTER_MUTATION"
			if mode == "category_partial_remote_failure" {
				wantCode = "PARTIAL_MUTATION"
			}
			if out.FailureCode != wantCode {
				t.Fatal("missing stop-old-reference code", out)
			}
			unknown := mode == "cookie_write_revoked" || mode == "add_unknown_revoked" || strings.HasPrefix(mode, "category_partial_")
			if unknown {
				if out.Accepted || out.Outcome != "unknown" {
					t.Fatal("sent/partial effect became unsent", out)
				}
			} else if !out.Accepted || out.Outcome != "confirmed" {
				t.Fatal("acknowledged effect lost", out)
			}
			if mode == "add_confirmed_revoked" && !reflect.DeepEqual(out.AddedIDs, []string{ownedDownloaderHash}) {
				t.Fatal("confirmed added IDs lost", out)
			}
			want := 1
			if mode == "category_partial_remote_failure" {
				want = 2
			}
			if len(observed) != want {
				t.Fatal("selected write retried or revoked later hash dispatched", observed)
			}
			if mode == "cookie_write_revoked" && observed[0] != "app/setCookies" {
				t.Fatal("cookie side effect incorrectly became torrent add", observed)
			}
			if mode == "category_partial_revoked" && observed[0] != "torrents/setCategory:"+ownedDownloaderHash {
				t.Fatal("category order changed", observed)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "OWNED_PRIVATE_RESPONSE") {
				t.Fatal("private response leaked")
			}
		})
	}
}
