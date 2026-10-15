package app

import (
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/fnos"
)

func TestFNOSAutomaticallyMatchesPhysicalDirectories(t *testing.T) {
	base := t.TempDir()
	movies := filepath.Join(base, "movies")
	input := filepath.Join(movies, "Title", "movie.mkv")
	importWrite(t, input, []byte("synthetic"))
	libraries := []fnos.ServiceLibrary{{GUID: "parent", Directories: []string{base}}, {GUID: "movies", Directories: []string{movies}}}
	guid, path, e := matchFNOSDirectory(input, input, libraries)
	if e != nil || guid != "movies" || path != input {
		t.Fatalf("guid=%s path=%s err=%v", guid, path, e)
	}
	alias := filepath.Join(base, "bind-alias")
	if e = os.Symlink(movies, alias); e != nil {
		t.Fatal(e)
	}
	guid, path, e = matchFNOSDirectory(filepath.Join(alias, "Title", "movie.mkv"), "/unmatched", []fnos.ServiceLibrary{libraries[1]})
	if e != nil || guid != "movies" || path != input {
		t.Fatalf("same-directory mount not resolved: %s %s %v", guid, path, e)
	}
	if _, _, e = matchFNOSDirectory(input, input, append(libraries, fnos.ServiceLibrary{GUID: "duplicate", Directories: []string{movies}})); e == nil {
		t.Fatal("overlapping media servers guessed")
	}
	if _, _, e = matchFNOSDirectory(input, input, []fnos.ServiceLibrary{{GUID: "other", Directories: []string{filepath.Join(base, "unrelated")}}}); e == nil {
		t.Fatal("unrelated media library selected")
	}
}
