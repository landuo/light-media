package app

import (
	"context"
	"light-media/internal/media"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRecognitionWordsEncryptedRestartAndFileScanUsesSharedMatcher(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	rules := []string{"Legacy => Test Film", "E <> \\.mkv >> EP-2"}
	if err := a.saveRecognitionWords(ctx, 0, rules); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	a.Store.DB.QueryRow(`SELECT sealed FROM recognition_words`).Scan(&sealed)
	if strings.Contains(string(sealed), "Legacy") {
		t.Fatal("recognition words stored in plaintext")
	}
	for _, name := range []string{"Legacy (2024).mkv", "Legacy.S01E09.mkv"} {
		importWrite(t, filepath.Join(source, name), []byte("owned media fixture"))
	}
	scan, err := a.scanFiles(ctx, source, false)
	if err != nil {
		t.Fatal(err)
	}
	files := scan.(map[string]any)["files"].([]importScanFile)
	if len(files) != 2 || files[0].Path != filepath.Join(source, "Legacy (2024).mkv") || files[0].Parsed.Title != "Test Film" || !reflect.DeepEqual(files[1].Parsed.Original.Episodes, []int{7}) {
		t.Fatal("scan words or source path mismatch", files)
	}
	dir := a.Dir
	a.Close()
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.parseMediaName("Legacy.S01E09.mkv").Original.Episodes[0] != 7 {
		t.Fatal("recognition snapshot not restored")
	}
}

func TestRecognitionWordsLimitsDoNotUseUnadjustedIdentity(t *testing.T) {
	a := newTestApp(t)
	if err := a.saveRecognitionWords(context.Background(), 0, []string{`(a+)+$ => X`}); err != nil {
		t.Fatal(err)
	}
	parsed := a.parseMediaName(strings.Repeat("a", 80) + ".S01E09.mkv")
	if parsed.Status == media.Parsed || parsed.Original != nil {
		t.Fatal("incomplete preprocessing accepted the original unadjusted episode")
	}
}
