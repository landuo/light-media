package iyuuautoseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	download "light-media/internal/plugins/iyuuautoseed/native"
)

func TestNativeDestinationUnobservedPathAndColdBindingChangesHaveZeroDispatch(t *testing.T) {
	for _, change := range []string{"origin", "credential"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			key := bytes.Repeat([]byte{42}, 32)
			var addCalls, oldCalls, newCalls atomic.Int32
			old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				oldCalls.Add(1)
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					writeJSON(w, []any{map[string]any{"hash": strings.Repeat("a", 40), "save_path": "/observed", "state": "pausedUP", "progress": 1, "amount_left": 0}})
				case "/download.php":
					w.Write([]byte("d4:infod6:lengthi1e4:name9:owned.mkvee"))
				case "/api/v2/torrents/add":
					addCalls.Add(1)
					w.Write([]byte("Ok."))
				default:
					t.Error("unexpected source daemon request", r.URL.Path)
				}
			}))
			defer old.Close()
			next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { newCalls.Add(1) }))
			defer next.Close()
			filename := filepath.Join(dir, "journal.sqlite")
			j, err := OpenJournal(filename, key)
			if err != nil {
				t.Fatal(err)
			}
			identity := RunIdentity{Instance: "owned", Generation: 1, Revision: 1, Fingerprint: "destination-authority", Run: "owned-run"}
			input := map[string]any{"owned": true}
			run, err := j.Begin(ctx, identity, input, liveGuard)
			if err != nil {
				t.Fatal(err)
			}
			site := Site{Ref: "owned-site", Fields: map[string]any{"id": json.Number("1"), "name": "owned", "url": old.URL + "/", "cookie": "", "ua": "owned"}}
			httpSite, err := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: site.Ref, Fields: site.Fields, AllowedOrigins: []string{old.URL}}}, SiteTransportOptions{Guard: liveGuard})
			if err != nil {
				t.Fatal(err)
			}
			defer httpSite.Close()
			cache, err := OpenFileTorrentCache(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			binding := NativeBinding{Service: Service{Ref: "native", Name: "owned", Kind: "qbittorrent"}, Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: old.URL}, AllowDefaultPath: true}
			host, err := NewNativeHost(NativeHostOptions{Run: run, Bindings: []NativeBinding{binding}, Sites: []Site{site}, SiteHTTP: httpSite, Cache: cache})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = host.Completed(ctx, binding.Service); err != nil {
				t.Fatal(err)
			}
			address, err := host.DownloadURL(ctx, map[string]any{"torrent_id": json.Number("7")}, site, "download.php?id={id}")
			if err != nil {
				t.Fatal(err)
			}
			content, err := host.Torrent(ctx, site, address)
			if err != nil {
				t.Fatal(err)
			}
			if len(content.Bytes) == 0 || content.ErrorMessage != "" {
				t.Fatal("actual torrent content lineage missing", content.ErrorMessage)
			}
			if _, err = host.AddPaused(ctx, binding.Service, AddSpec{Content: content.Bytes, SavePath: "/worker-unobserved", Paused: true}); err == nil {
				t.Fatal("worker arbitrary save path granted")
			} else if !strings.Contains(err.Error(), "save path was not observed") {
				t.Fatal("unobserved path did not reach intended authority check", err)
			}
			if addCalls.Load() != 0 {
				t.Fatal("unobserved path dispatched")
			}
			host.Close()
			if err = j.Close(); err != nil {
				t.Fatal(err)
			}
			before := oldCalls.Load()
			j, err = OpenJournal(filename, key)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			run, err = j.Begin(ctx, identity, input, liveGuard)
			if err != nil {
				t.Fatal(err)
			}
			if change == "origin" {
				binding.Config.BaseURL = next.URL
			} else {
				binding.Options.APIKey = "changed-owner-key"
			}
			_, err = NewNativeHost(NativeHostOptions{Run: run, Bindings: []NativeBinding{binding}, Sites: []Site{site}, SiteHTTP: httpSite, Cache: cache})
			if !errors.Is(err, ErrReplayDiverged) {
				t.Fatal("stale daemon binding did not fence cold replay", err)
			}
			if newCalls.Load() != 0 || oldCalls.Load() != before || addCalls.Load() != 0 {
				t.Fatal("stale origin/config performed IO", newCalls.Load(), oldCalls.Load(), before)
			}
		})
	}
}
