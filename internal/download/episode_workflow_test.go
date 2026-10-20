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

func TestSourceQBAcknowledgedPausedAddThenSelectAndOriginalFalse(t *testing.T) {
	for _, match := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_matching_filenames", true: "failed_setter_still_starts"}[match], func(t *testing.T) {
			torrent, hash := testTorrent()
			var adds, starts, setters atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() == 0 {
						reply(w, []any{})
					} else {
						reply(w, []any{qbRow(hash)})
					}
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil {
						t.Error(e)
					}
					if r.FormValue("stopped") != "true" {
						t.Error("episode-selected torrent was not added paused")
					}
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/files":
					io.WriteString(w, `[{"index":7,"name":"Owned.S01E01.mkv"},{"index":3,"name":"Owned.S01E02.mkv"}]`)
				case "/api/v2/torrents/filePrio":
					r.ParseForm()
					if r.Form.Get("id") != "3" || r.Form.Get("priority") != "0" {
						t.Error("source suppressed wrong files", r.Form)
					}
					setters.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				case "/api/v2/torrents/start":
					starts.Add(1)
				default:
					t.Error("ungranted endpoint", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			client, e := New(Config{ID: "owned", Kind: "qb", BaseURL: server.URL})
			if e != nil {
				t.Fatal(e)
			}
			accepted, e := client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/owned", Paused: true})
			if e != nil || accepted.Hash != hash || accepted.Duplicate || adds.Load() != 1 {
				t.Fatal("actual daemon Add was not acknowledged", accepted, adds.Load(), e)
			}
			out, e := client.SourceSelectEpisodeDownload(context.Background(), accepted.Hash, []int{1}, false, func(name string) ([]int, error) {
				if !match {
					return nil, nil
				}
				if strings.Contains(name, "E01") {
					return []int{1}, nil
				}
				return []int{2}, nil
			})
			if e != nil || !out.Selection.Started || out.Selection.MetadataMissing || starts.Load() != 1 || adds.Load() != 1 {
				t.Fatal("source post-Add branch differs", out.Selection, starts.Load(), adds.Load(), e)
			}
			if match {
				if setters.Load() != 1 || len(out.Effects) != 3 || out.Effects[1].Acknowledged || out.Effects[1].Failure == nil || !out.Effects[2].Acknowledged {
					t.Fatal("source False was promoted to native acknowledgment or prevented source Start", out.Effects)
				}
			} else if setters.Load() != 0 || len(out.Effects) != 2 {
				t.Fatal("source qB unmatched-file branch changed default priorities", setters.Load(), out.Effects)
			}
		})
	}
}
