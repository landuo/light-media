package app

import (
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/download"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHostDownloaderURLCookieActualSDKAndEmptyNoDispatch(t *testing.T) {
	a, _, object := hostDownloaderFixture(t)
	var hits atomic.Int32
	address, cookie := "https://tracker.invalid/file?passkey=OWNED_URL_PRIVATE", "uid=OWNED_COOKIE; passkey=OWNED_VALUE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer OWNED_HOST_KEY" {
			t.Error("configured downloader credential not retained by host")
		}
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.3")
		case "/api/v2/app/cookies":
			_, _ = io.WriteString(w, `[{"domain":"unrelated.invalid","path":"/","name":"keep","value":"OWNED_UNRELATED_COOKIE"}]`)
		case "/api/v2/app/setCookies":
			_ = r.ParseForm()
			var values []map[string]any
			if json.Unmarshal([]byte(r.Form.Get("cookies")), &values) != nil || len(values) != 3 {
				t.Error("source global cookie merge changed")
			}
			if values[0]["domain"] != "unrelated.invalid" || values[1]["domain"] != "tracker.invalid" {
				t.Error("cookie domains widened or old entries discarded")
			}
		case "/api/v2/torrents/add":
			if r.ParseMultipartForm(1<<20) != nil {
				t.Error("bad URL multipart")
			}
			if r.FormValue("urls") != address {
				t.Error("private URL material was modified")
			}
			if _, exists := r.MultipartForm.Value["cookie"]; exists {
				t.Error("successful cookie sync still sent fallback cookie")
			}
			_, _ = io.WriteString(w, "Ok.")
		default:
			t.Error("unexpected URL cookie endpoint")
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL, APIKey: "OWNED_HOST_KEY"}}}
	ownedDownloaderSave(t, a, settings)
	var out plugins.DownloaderMutationV1
	if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &address, Cookie: &cookie, Paused: true}, &out); err != nil || !out.Accepted || hits.Load() != 4 {
		t.Fatal("SDK URL/cookie source chain failed", err, out, hits.Load())
	}
	encoded, _ := json.Marshal(out)
	for _, secret := range []string{address, cookie, "OWNED_HOST_KEY", "OWNED_UNRELATED_COOKIE"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("URL/cookie material echoed from host")
		}
	}
	empty := ""
	if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &empty, Cookie: &cookie}, &out); err != nil || out.Accepted || out.Outcome != "rejected" || hits.Load() != 4 {
		t.Fatal("source empty content did not return false without dispatch", err, out, hits.Load())
	}
	if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), URL: &address}, &out); err == nil || hits.Load() != 4 {
		t.Fatal("ambiguous material dispatched")
	}
}
func TestHostDownloaderTransmissionURLCookieActualSDK(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			address, cookie := "https://tracker.invalid/file?passkey=OWNED_URL_PRIVATE", "uid=OWNED_COOKIE"
			var adds atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var env map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&env)
				var method string
				_ = json.Unmarshal(env["method"], &method)
				if !modern && method == "session_get" {
					_, _ = io.WriteString(w, `{"result":"method name not recognized","arguments":{}}`)
					return
				}
				key, id := "arguments", env["tag"]
				if modern {
					key, id = "params", env["id"]
				}
				response := `{"version":"4.1"}`
				if method == "torrent_add" || method == "torrent-add" {
					adds.Add(1)
					var args map[string]any
					_ = json.Unmarshal(env[key], &args)
					if args["filename"] != address || args["cookies"] != cookie || !reflect.DeepEqual(args["labels"], []any{"owned"}) || args["paused"] != true {
						t.Error("source TR URL/cookie wire changed")
					}
					response = `{"torrent_added":{"hash_string":"` + ownedDownloaderHash + `","id":1}}`
					if !modern {
						response = `{"torrent-added":{"hashString":"` + ownedDownloaderHash + `","id":1}}`
					}
				}
				if modern {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, response)
				} else {
					_, _ = fmt.Fprintf(w, `{"tag":%s,"result":"success","arguments":%s}`, id, response)
				}
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			var out plugins.DownloaderMutationV1
			if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &address, Cookie: &cookie, Paused: true, Tags: []string{"owned"}}, &out); err != nil || !out.Accepted || out.Hash != ownedDownloaderHash || adds.Load() != 1 {
				t.Fatal("actual TR URL/cookie add failed", err, out)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "OWNED_COOKIE") || strings.Contains(string(encoded), "OWNED_URL_PRIVATE") {
				t.Fatal("TR returned cookie or private URL")
			}
			address = ""
			if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &address, Cookie: &cookie, Paused: true, Tags: []string{"owned"}}, &out); err != nil || !out.Accepted || adds.Load() != 2 {
				t.Fatal("source vendor delegates empty string filename", out, err, adds.Load())
			}
			for _, rejectedURI := range []string{"FILE:///owned/file.torrent", "http://[invalid]/a", "http://[::1/a", "http://host：80/a"} {
				address = rejectedURI
				if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &address, Cookie: &cookie, Paused: true, Tags: []string{"owned"}}, &out); err != nil || out.Accepted || adds.Load() != 2 {
					t.Fatal("source vendor must reject URI before dispatch", out, err, adds.Load())
				}
			}
		})
	}
}
