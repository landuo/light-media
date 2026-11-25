package app

import (
	"compress/gzip"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func withinUsingRel(root, target string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func TestWithinMatchesRelativeComponents(t *testing.T) {
	paths := []string{"", ".", "relative/path", "/", "//", "/a", "/ab", "/a/", "/a//b", "/a/./b", "/a/../b", "/b/a", "/a/..name", "/../../a/a", "/b", "/a/b/c", "/影/片", "/影/片源", "/影/片/第一集"}
	for _, root := range paths {
		for _, target := range paths {
			if got, want := within(root, target), withinUsingRel(root, target); got != want {
				t.Fatalf("within(%q,%q)=%t want %t", root, target, got, want)
			}
		}
	}
}
func BenchmarkWithinSibling(b *testing.B) {
	for _, tc := range []struct {
		name string
		fn   func(string, string) bool
	}{{"relative", withinUsingRel}, {"components", within}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if tc.fn("/media/downloads/tv", "/media/downloads/movies/feature/video.mkv") {
					b.Fatal("overlap")
				}
			}
		})
	}
}
func TestBinaryHeapProfileRequiresSession(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	const path = "/api/debug/heap?format=pprof"
	if w := callTest(t, h, "GET", path, "", nil, ""); w.Code != 401 {
		t.Fatalf("anonymous profile: %d", w.Code)
	}
	w := callTest(t, h, "GET", path, "", cookie, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header())
	}
	gz, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	if n, err := io.Copy(io.Discard, gz); err != nil || n == 0 {
		t.Fatal(n, err)
	}
}
