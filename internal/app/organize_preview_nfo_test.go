package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryPreviewNFOKeepsOriginalEvidenceAndGuards(t *testing.T) {
	a, source, _ := importTestApp(t)
	root := filepath.Join(source, "Fallout")
	dir := filepath.Join(root, "Season 2")
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "Fallout.S02E01.mkv")
	put := func(file, body string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(root, "tvshow.nfo"), `<tvshow><title>Fallout</title><uniqueid type="tmdb">106379</uniqueid></tvshow>`)
	check := func(path string, wantID int64, wantError string) {
		t.Helper()
		inputs := &manualPreviewInputs{nfoDirectories: make(map[string]map[string]bool)}
		m, _, reason := a.readImportNFOEvidence(path, inputs.readNFO, &inputs.seriesNFO)
		if reason != wantError || wantID != 0 && (m == nil || m.ID != wantID) || wantID == 0 && m != nil {
			t.Fatalf("%s: %+v %q", path, m, reason)
		}
	}
	check(video, 106379, "")
	episode := filepath.Join(dir, "Fallout.S02E01.nfo")
	put(episode, "malformed")
	check(video, 0, "invalid_or_conflicting_nfo")
	if err := os.Remove(episode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "tvshow.nfo"), episode); err != nil {
		t.Fatal(err)
	}
	check(video, 0, "invalid_or_conflicting_nfo")
	if err := os.Remove(episode); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 130; i++ {
		put(filepath.Join(dir, fmt.Sprintf("padding-%d", i)), "")
	}
	put(episode, "malformed")
	check(video, 0, "invalid_or_conflicting_nfo")
}
