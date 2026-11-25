package app

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
)

const lazyTaskHash = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

// Each fixture lists at most one task. Serve the same row for qBittorrent's
// hash-specific existence check before a file listing.
func lazyDirectoryDownloader(t testing.TB, savePath string, files http.HandlerFunc) DownloaderConfig {
	t.Helper()
	list := []byte("[]")
	if savePath != "" {
		task := workflowTask(lazyTaskHash, 1)
		task["state"], task["save_path"] = "uploading", savePath
		var err error
		list, err = json.Marshal([]any{task})
		if err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/api/v2/torrents/info":
				_, _ = w.Write(list)
				return
			case "/api/v2/torrents/files":
				if files != nil && r.URL.Query().Get("hash") == lazyTaskHash {
					files(w, r)
					return
				}
			}
		}
		t.Errorf("unexpected downloader request: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return DownloaderConfig{Config: download.Config{Kind: "qb", ID: "qb", BaseURL: server.URL}, SharedPaths: true}
}

func BenchmarkDirectoryFilesForDownloaderHistory1024(b *testing.B) {
	a, source, sibling := importTestApp(b)
	ctx := b.Context()
	result, err := a.Store.DB.ExecContext(ctx, `WITH RECURSIVE seq(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM seq WHERE i<1024)
		INSERT INTO downloadhistory(uid,payload,updated_at)
		SELECT printf('fixture-%d',i), json_object('downloader_id','qb','hash',printf('%040x',i),'state','imported','media_type','movie','tmdb_id',i), '2026-01-01' FROM seq`)
	if err != nil {
		b.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1024 {
		b.Fatalf("seeded %d records: %v", n, err)
	}
	for _, tc := range []struct{ name, path string }{{"empty", ""}, {"unrelated", sibling}} {
		b.Run(tc.name, func(b *testing.B) {
			d := lazyDirectoryDownloader(b, tc.path, nil)
			prior := map[string]string{lazyTaskHash: "previous"}
			b.ReportAllocs()
			for b.Loop() {
				out, next, err := a.directoryFilesForDownloader(ctx, d, source, prior, nil)
				if err != nil || len(out) != 0 || len(next) != 0 {
					b.Fatalf("files=%v next=%v err=%v", out, next, err)
				}
			}
		})
	}
}

func TestDirectoryDownloaderHistoryReadBoundary(t *testing.T) {
	a, source, sibling := importTestApp(t)
	a.invalidateSettings()
	// A missing table makes accidental history reads observable, without a
	// production test hook. Relevant tasks must still propagate that error.
	if _, err := a.Store.DB.Exec(`DROP TABLE downloadhistory`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path string }{{"empty", ""}, {"unrelated", sibling}, {"unverified", source + "/./bad"}, {"relevant", source}} {
		t.Run(tc.name, func(t *testing.T) {
			d := lazyDirectoryDownloader(t, tc.path, nil)
			prior := map[string]string{lazyTaskHash: "previous"}
			out, next, err := a.directoryFilesForDownloader(t.Context(), d, source, prior, nil)
			switch tc.name {
			case "relevant":
				if err == nil || !strings.Contains(err.Error(), "no such table: downloadhistory") || out != nil || next != nil {
					t.Fatalf("expected history read failure, got %v %v %v", out, next, err)
				}
			case "unverified":
				var api *APIError
				if !errors.As(err, &api) || api.Code != "DIRECTORY_PATH_UNVERIFIED" || out != nil || next != nil {
					t.Fatalf("expected path error before history access, got %v %v %v", out, next, err)
				}
			default:
				if err != nil || len(out) != 0 || len(next) != 0 {
					t.Fatalf("files=%v next=%v err=%v", out, next, err)
				}
				next[lazyTaskHash] = "changed"
				if prior[lazyTaskHash] != "previous" {
					t.Fatal("returned fingerprint map aliases input")
				}
			}
		})
	}
}

func TestDirectoryDownloaderRelevantFingerprintAndIdentity(t *testing.T) {
	a, source, _ := importTestApp(t)
	a.invalidateSettings()
	ctx := t.Context()
	var listings atomic.Int32
	d := lazyDirectoryDownloader(t, source, func(w http.ResponseWriter, r *http.Request) {
		listings.Add(1)
		workflowJSON(w, []any{map[string]any{"name": "film.mkv", "size": 1024, "progress": 1}})
	})
	row := NativeDownload{ID: "fixture", Hash: lazyTaskHash, DownloaderID: d.ID, State: "imported", MediaType: "movie", TMDBID: 123}
	if err := a.Store.Put(ctx, "native_download", row.ID, row); err != nil {
		t.Fatal(err)
	}
	prior := map[string]string{lazyTaskHash: "stale", "other": "keep"}
	out, next, err := a.directoryFilesForDownloader(ctx, d, source, prior, nil)
	want := map[string]string{lazyTaskHash: "seeding|1.000000|1024|1024|test|1"}
	entry := out[filepath.Join(source, "film.mkv")]
	if err != nil || listings.Load() != 1 || len(out) != 1 || !entry.Managed || !entry.Imported || !entry.Complete || entry.OwnerHash != lazyTaskHash || entry.Hint == nil || entry.Hint.ID != 123 || entry.Hint.MediaType != "movie" || !maps.Equal(next, want) || prior[lazyTaskHash] != "stale" {
		t.Fatalf("fresh listing: entry=%+v next=%v prior=%v calls=%d err=%v", entry, next, prior, listings.Load(), err)
	}
	// A fingerprint hit skips the file listing, not the current history and
	// media identity check. A cancelled record no longer claims the file.
	row.State = "cancelled"
	if err := a.Store.Put(ctx, "native_download", row.ID, row); err != nil {
		t.Fatal(err)
	}
	out, next, err = a.directoryFilesForDownloader(ctx, d, source, next, map[string]map[string]directoryDownloadFile{lazyTaskHash: out})
	entry = out[filepath.Join(source, "film.mkv")]
	if err != nil || listings.Load() != 1 || len(out) != 1 || entry.Managed || entry.Imported || !entry.Complete || entry.OwnerHash != lazyTaskHash || entry.Hint != nil || !maps.Equal(next, want) {
		t.Fatalf("cached listing: entry=%+v next=%v calls=%d err=%v", entry, next, listings.Load(), err)
	}
}
