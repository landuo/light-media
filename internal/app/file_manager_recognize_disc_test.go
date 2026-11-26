package app

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

func TestFileManagerRecognizeSelectedDisc(t *testing.T) {
	a, source, _ := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	show := catalog.Media{ID: 106379, MediaType: "tv", Title: "辐射", OriginalTitle: "Fallout", Year: 2024}
	importCache(t, a, "directory-identify:tv:Fallout", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 1, Results: []catalog.Media{show}})
	importCache(t, a, "media:tv:106379", show)
	disc := filepath.Join(source, "电视剧", "Fallout.S01.2160p.UHD.Blu-ray.HEVC.Atmos.TrueHD7.1-DiY@HDHome", "Disc 1")
	importWrite(t, filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts"), []byte("stream"))
	ordinary := filepath.Join(source, "episodes")
	importWrite(t, filepath.Join(ordinary, "Fallout.S01E02.mkv"), []byte("episode"))
	for _, tc := range []struct {
		path    string
		episode int
	}{{disc, 0}, {ordinary, 2}} {
		raw, _ := json.Marshal(map[string]string{"path": tc.path})
		r := callTest(t, a.Handler(), "POST", "/api/files/recognize", string(raw), cookie, "")
		var response struct {
			Data struct {
				Media    catalog.Media
				Parsed   media.ParseResult
				Season   int
				Episodes []int
			}
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil {
			t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body.String())
		}
		got := response.Data
		if got.Media.ID != show.ID || got.Season != 1 || got.Parsed.Title != "Fallout" || (tc.episode == 0 && len(got.Episodes) != 0) || (tc.episode != 0 && (len(got.Episodes) != 1 || got.Episodes[0] != tc.episode)) {
			t.Fatalf("%s: %+v", tc.path, got)
		}
	}
}
