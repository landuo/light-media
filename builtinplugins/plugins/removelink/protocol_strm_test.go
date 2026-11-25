package removelink

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActualWorkerLocalSTRMUsesCurrentStorageHistoryAndNoTorrentEvent(t *testing.T) {
	for _, decline := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete-confirmed", true: "ordinary-false"}[decline], func(t *testing.T) {
			f := startProtocol(t)
			strm := filepath.Join(f.root, "strm")
			media := filepath.Join(f.root, "media/series")
			for _, dir := range []string{strm, media} {
				if e := os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
			}
			file := filepath.Join(strm, "a.strm")
			video := filepath.Join(media, "a.mkv")
			for _, p := range []string{file, filepath.Join(strm, "a.nfo"), video, filepath.Join(media, "a.nfo"), filepath.Join(media, "poster.jpg"), filepath.Join(media, "keep.bin")} {
				if e := os.WriteFile(p, []byte("owned synthetic"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			f.call("init", map[string]any{"enabled": true, "monitor_strm_deletion": true, "strm_path_mappings": strm + ":local:" + media, "delete_scrap_infos": true, "delete_history": true, "delete_torrents": true, "notify": true})
			if decline {
				f.mu.Lock()
				f.declineDelete = video
				f.mu.Unlock()
			}
			if e := os.Remove(file); e != nil {
				t.Fatal(e)
			}
			f.call("test_event", map[string]any{"kind": "deleted", "path": file, "mode": "strm", "at": 1760000100})
			if _, e := os.Stat(video); errors.Is(e, os.ErrNotExist) == decline {
				t.Fatal("selected storage outcome changed", decline, e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			notices, directoryNotices := 0, 0
			for _, x := range f.trace {
				if x["op"] == "event" {
					t.Fatal("STRM invented torrent event", x)
				}
				if x["op"] == "notify" {
					if x["title"] == "📁 目录清理" {
						directoryNotices++
						continue
					}
					notices++
					if !strings.Contains(x["text"].(string), video) {
						t.Fatal("wrong mapped path in notice", x)
					}
				}
			}
			if decline {
				if len(f.historyReads) != 0 || notices != 0 || directoryNotices != 0 {
					t.Fatal("false media delete still cleaned/notified")
				}
				for _, p := range []string{filepath.Join(strm, "a.nfo"), filepath.Join(media, "a.nfo"), filepath.Join(media, "poster.jpg")} {
					if _, e := os.Stat(p); e != nil {
						t.Fatal("cleanup after false media deletion", p, e)
					}
				}
			} else {
				if len(f.historyReads) != 1 || f.historyReads[0].Target != video || f.historyReads[0].Source != "" || notices != 1 || directoryNotices != 1 {
					t.Fatal("destination history or source notice semantics changed", f.historyReads, notices)
				}
				for _, p := range []string{filepath.Join(strm, "a.nfo"), filepath.Join(media, "a.nfo"), filepath.Join(media, "poster.jpg")} {
					if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
						t.Fatal("owned scrape file not removed", p, e)
					}
				}
			}
		})
	}
}
