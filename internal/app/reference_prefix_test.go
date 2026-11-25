package app

import (
	"strings"
	"testing"
)

func TestReferenceSlashPrefixParity(t *testing.T) {
	paths := []string{"", "/", "//", ".", "/a", "/a/b", "/a/bb", "/a/b/", "../a", "/本地/电影", "/本地/电影/01", "/本地/电影版", strings.Repeat("/library", 20)}
	for _, p := range paths {
		for _, r := range paths {
			if got, want := hasSlashDelimitedPrefix(p, r), strings.HasPrefix(p, r+"/"); got != want {
				t.Fatalf("prefix(%q,%q)=%t want %t", p, r, got, want)
			}
		}
	}
}

func BenchmarkReferenceSlashPrefix(b *testing.B) {
	root := strings.Repeat("/library", 10)
	path := root + "-archive/file"
	for _, tc := range []struct {
		name string
		f    func(string, string) bool
	}{{"concat", func(p, r string) bool { return strings.HasPrefix(p, r+"/") }}, {"direct", hasSlashDelimitedPrefix}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if tc.f(path, root) {
					b.Fatal("unexpected match")
				}
			}
		})
	}
}
