package download

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestQBitTorrentAPIKeyAndOriginalAddOptions(t *testing.T) {
	torrent, hash := testTorrent()
	var added atomic.Bool
	var logins, adds atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owned-synthetic-api-key" {
			t.Error("API key was not used at configured daemon")
		}
		switch r.URL.Path {
		case "/api/v2/auth/login":
			logins.Add(1)
			t.Error("API key mode submitted a password")
			http.Error(w, "not allowed", 403)
		case "/api/v2/torrents/info":
			if added.Load() {
				reply(w, []any{qbRow(hash)})
			} else {
				reply(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "bad multipart", 400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			for _, field := range []string{"autoTMM", "sequentialDownload", "firstLastPiecePrio"} {
				if r.FormValue(field) != "true" {
					t.Error("source option lost", field, r.FormValue(field))
				}
			}
			if r.FormValue("savepath") != "/downloads" || r.FormValue("skip_checking") != "false" {
				t.Error("unrelated add behavior changed")
			}
			adds.Add(1)
			added.Store(true)
			io.WriteString(w, "Ok.")
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c, err := New(Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: s.URL, Username: "ignored-user", Password: "ignored-password", APIKey: "owned-synthetic-api-key", AutoCategory: true, Sequential: true, FirstLast: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads", Paused: true, Tags: []string{"light-media"}})
	if err != nil || result.Hash != hash || result.Duplicate || logins.Load() != 0 || adds.Load() != 1 {
		t.Fatal("original API key/options path failed", result, err, logins.Load(), adds.Load())
	}
	result, err = c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads", Paused: true})
	if err != nil || !result.Duplicate || adds.Load() != 1 {
		t.Fatal("options bypassed duplicate protection", result, err, adds.Load())
	}
}

func TestQBitTorrentRejectedAPIKeyNeverFallsBackOrRedirects(t *testing.T) {
	for _, status := range []int{401, 403, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var foreign, login, requests atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1) }))
			defer other.Close()
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if strings.Contains(r.URL.Path, "auth/login") {
					login.Add(1)
				}
				if status == 302 {
					w.Header().Set("Location", other.URL)
				}
				w.WriteHeader(status)
				io.WriteString(w, "owned-synthetic-api-key")
			}))
			defer s.Close()
			c, err := New(Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: s.URL, Username: "must-not-send", Password: "must-not-send", APIKey: "owned-synthetic-api-key"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.List(context.Background())
			if err == nil || strings.Contains(err.Error(), "owned-synthetic-api-key") || login.Load() != 0 || foreign.Load() != 0 || requests.Load() != 1 {
				t.Fatal("rejected key caused fallback/replay/leak", err, login.Load(), foreign.Load(), requests.Load())
			}
		})
	}
}

func TestDownloaderAPIKeyValidation(t *testing.T) {
	for _, sample := range []Config{{ID: "owned-qb", Kind: "qbittorrent", BaseURL: "http://127.0.0.1", APIKey: "invalid\r\nheader"}, {ID: "owned-tr", Kind: "transmission", BaseURL: "http://127.0.0.1", APIKey: "key"}, {ID: "owned-qb", Kind: "qbittorrent", BaseURL: "http://127.0.0.1", APIKey: strings.Repeat("x", 4097)}} {
		if _, err := New(sample); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
