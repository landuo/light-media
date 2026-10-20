package app

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/fileextensions"
	"light-media/internal/releasemeta"
)

func TestActualParserUsesSourceMediaSubtitleAudioUnion(t *testing.T) {
	a, _, _ := importTestApp(t)
	ownedCleanExtensionEnvironment(t)
	ownedExtensionConfig(a, []string{".ownedvideo"}, []string{".ownedsub"}, []string{".ownedaudio"})
	options, e := a.globalRecognitionOptions(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(options.MediaExts, []string{".ownedvideo", ".ownedsub", ".ownedaudio"}) {
		t.Fatal("global parser still uses primary media classifier list", options.MediaExts)
	}
	for _, mode := range []string{"legacy", "rust"} {
		t.Run(mode, func(t *testing.T) {
			options.SourceMode = mode
			parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
			if e != nil {
				t.Fatal(e)
			}
			for _, ext := range options.MediaExts {
				meta, e := parser.ParseTitle("Owned.S01E01"+ext, nil, nil)
				if e != nil || meta.Get("isfile") != true {
					t.Fatal("source parser did not strip recognized auxiliary extension", ext, e)
				}
			}
		})
	}
	snapshot, e := a.sourceFileExtensions(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(snapshot.Values(fileextensions.MediaKey), []string{".ownedvideo"}) {
		t.Fatal("parser union contaminated primary media predicate")
	}
}
func TestFileManagerActualCustomExtensionAuthenticatedSelectionAndImport(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	ctx := context.Background()
	ownedExtensionConfig(a, []string{".video"}, []string{".subs"}, []string{".audio"})
	input := filepath.Join(source, "Owned.Movie.2024.video")
	importWrite(t, input, []byte("actual custom browser input"))
	importWrite(t, filepath.Join(source, "Other.Movie.2024.mkv"), []byte("excluded primary extension"))
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	root := fileBrowserRoot(t, a, source)
	path := "/api/files/list?" + url.Values{"root_id": {root.ID}, "path": {source}}.Encode()
	if w := callTest(t, handler, "GET", path, "", nil, ""); w.Code != 401 {
		t.Fatal("unauthed", w.Code)
	}
	w := callTest(t, handler, "GET", path, "", cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var listed struct {
		Data struct{ Items []browserEntry }
	}
	if e := json.Unmarshal(w.Body.Bytes(), &listed); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, item := range listed.Data.Items {
		if item.Path == input {
			found = true
			if !item.Video {
				t.Fatal("custom media cannot be selected in actual file listing")
			}
		} else if item.Video {
			t.Fatal("unconfigured video flagged", item)
		}
	}
	if !found {
		t.Fatal("actual list missing customfile")
	}
	if a.transferExtensions.constructor != nil {
		t.Fatal("read-only listing prematurely constructed transfer instance")
	}
	scan, _ := json.Marshal(map[string]any{"paths": []string{source}, "recursive": true})
	w = callTest(t, handler, "POST", "/api/files/scan", string(scan), cookie, "")
	var scanned struct {
		Data struct{ Files []importScanFile }
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &scanned) != nil || len(scanned.Data.Files) != 1 || scanned.Data.Files[0].Path != input {
		t.Fatal("actual directory selection", w.Code, w.Body)
	}
	scan, _ = json.Marshal(map[string]any{"paths": []string{input}, "recursive": false})
	w = callTest(t, handler, "POST", "/api/files/scan", string(scan), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	req := ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}}
	body, _ := json.Marshal(req)
	w = callTest(t, handler, "POST", "/api/import/preview", string(body), cookie, "")
	var preview struct{ Data ImportPlan }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || len(preview.Data.Files) != 1 || preview.Data.Files[0].Status != "ready" {
		t.Fatal("actual authenticated preview", w.Code, w.Body)
	}
	body, _ = json.Marshal(map[string]any{"plan_id": preview.Data.ID, "file_ids": []string{preview.Data.Files[0].ID}})
	w = callTest(t, handler, "POST", "/api/import/execute", string(body), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	job, e := a.claim(ctx)
	if e != nil || job.Kind != "import" {
		t.Fatal("actual queued import", job, e)
	}
	if e = a.runImport(ctx, job, preview.Data.ID, []string{preview.Data.Files[0].ID}); e != nil {
		t.Fatal(e)
	}
	src, e := os.Stat(input)
	if e != nil {
		t.Fatal(e)
	}
	dst, e := os.Stat(preview.Data.Files[0].Target)
	if e != nil || !os.SameFile(src, dst) {
		t.Fatal("actual API-selected hardlink missing", e)
	}
}
