package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestResidentClassesAggregatesWithoutPaths(t *testing.T) {
	const data = `1000-2000 rw-p 00000000 00:00 0 [anon: Go: heap]
Size: 123 kB
Rss: 5 kB
2000-3000 rw-p 00000000 00:00 0
Rss: 11 kB
3000-4000 r-xp 00000000 00:00 2 /private/name
Rss: 3 kB
4000-5000 r-xp 00000000 00:00 2 /private/name
Rss: 7 kB
5000-6000 rw-p 00000000 00:00 0 [anon: Go: used]
Rss: 9 kB
`
	got, err := parseResidentClasses(strings.NewReader(data))
	want := map[string]uint64{"go_heap": 5 * 1024, "anonymous_unlabelled": 11 * 1024, "file_executable": 10 * 1024, "go_other": 9 * 1024}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	for _, s := range []string{"", "Rss: 3 kB\n", "0-1 rw-p 0 00:00 0\nRss: missing kB\n"} {
		if _, err := parseResidentClasses(strings.NewReader(s)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
