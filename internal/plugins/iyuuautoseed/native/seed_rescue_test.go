package download

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSeedRescueNativeDownloadingAndActiveAddWire(t *testing.T) {
	var adds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			io.WriteString(w, "Ok.")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.3")
		case "/api/v2/torrents/info":
			r.ParseForm()
			if r.Form.Get("filter") != "downloading" || r.Form.Get("tag") != "seed-rescue" {
				t.Error("lost exact source filter", r.Form)
			}
			io.WriteString(w, `[]`)
		case "/api/v2/torrents/add":
			adds.Add(1)
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
			}
			for key, want := range map[string]string{"paused": "False", "stopped": "False", "category": "seed-rescue", "tags": "seed-rescue", "savepath": "/owned/download"} {
				if got := r.FormValue(key); got != want {
					t.Error(key, got, want)
				}
			}
			file, _, e := r.FormFile("torrent__0")
			if e != nil {
				t.Error(e)
			} else {
				raw, _ := io.ReadAll(file)
				file.Close()
				if string(raw) != "owned-torrent" {
					t.Error("body changed")
				}
			}
			io.WriteString(w, "Ok.")
		default:
			t.Error("unexpected RPC", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := phaseClient(t, Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
	if _, e := c.SeedRescueDownloading(context.Background(), "seed-rescue"); e != nil {
		t.Fatal(e)
	}
	path, category := "/owned/download", "seed-rescue"
	r, e := c.SeedRescueAddDownloading(context.Background(), IYUUAddSpec{Torrent: []byte("owned-torrent"), SavePath: &path, Category: &category, Tags: []string{"seed-rescue"}})
	if e != nil || !r.Accepted || !r.SourceTruthy || adds.Load() != 1 {
		t.Fatal(r, e, adds.Load())
	}
}
func TestSeedRescueNativeSelectedQB401IsNeverResent(t *testing.T) {
	var adds, logins atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			logins.Add(1)
			io.WriteString(w, "Ok.")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.3")
		case "/api/v2/torrents/add":
			adds.Add(1)
			w.WriteHeader(401)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer s.Close()
	c := phaseClient(t, Config{ID: "owned", Kind: "qbittorrent", BaseURL: s.URL}, IYUUOptions{})
	_, e := c.SeedRescueAddDownloading(context.Background(), IYUUAddSpec{Torrent: []byte("owned")})
	if e == nil || MutationNotDispatched(e) || adds.Load() != 1 || logins.Load() != 0 {
		t.Fatal(e, adds.Load(), logins.Load())
	}
}
func TestSeedRescueNativeTR409AndGuardProof(t *testing.T) {
	var selected atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "owned-session")
			w.WriteHeader(409)
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		switch in["method"] {
		case "session_get":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": in["id"], "result": map[string]any{"version": "4.0.6"}})
		case "torrent_add":
			selected.Add(1)
			args := in["params"].(map[string]any)
			if args["paused"] != false {
				t.Error("source must download", args)
			}
			w.Header().Set("X-Transmission-Session-Id", "owned-second-session")
			w.WriteHeader(409)
		default:
			t.Error(in)
		}
	}))
	defer s.Close()
	c := phaseClient(t, Config{ID: "owned", Kind: "transmission", BaseURL: s.URL}, IYUUOptions{})
	_, e := c.SeedRescueAddDownloading(context.Background(), IYUUAddSpec{Torrent: []byte("owned")})
	if e == nil || MutationNotDispatched(e) || selected.Load() != 1 {
		t.Fatal(e, selected.Load())
	}
	denied := phaseClient(t, Config{ID: "owned", Kind: "qbittorrent", BaseURL: s.URL}, IYUUOptions{Guard: func(context.Context) error { return errors.New("owned denied") }})
	_, e = denied.SeedRescueAddDownloading(context.Background(), IYUUAddSpec{Torrent: []byte("owned")})
	if !MutationNotDispatched(e) || MutationFailureClass(e) != "owner_denied" {
		t.Fatal("missing exact proof", e)
	}
	_, e = denied.SeedRescueAddDownloading(context.Background(), IYUUAddSpec{Magnet: "magnet:?xt=owned"})
	if !MutationNotDispatched(e) || !strings.Contains(e.Error(), "seed") {
		t.Fatal(e)
	}
}
