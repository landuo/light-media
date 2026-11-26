package releasemeta

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestConfiguredPatternsPreserveModeAndBound(t *testing.T) {
	var cache configuredPatterns
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			ignore := i%2 == 0
			re, err := cache.compile("^abc$", ignore)
			if err != nil {
				t.Error(err)
				return
			}
			match, err := re.MatchString("ABC")
			if err != nil || match != ignore {
				t.Errorf("match = %t, ignore = %t, error = %v", match, ignore, err)
			}
		})
	}
	wg.Wait()
	if re, err := cache.compile("[", false); re != nil || err == nil {
		t.Fatal("invalid pattern was accepted")
	}
	for i := range 80 {
		text := fmt.Sprint("word", i)
		re, err := cache.compile(text, false)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := re.MatchString(text); !ok || err != nil {
			t.Fatal("pattern did not match", err)
		}
	}
	if got := len(cache.programs); got != 64 {
		t.Fatalf("cached %d programs", got)
	}
	fresh := &configuredPatterns{}
	if _, err := fresh.compile(strings.Repeat("x", 4097), false); err != nil || len(fresh.programs) != 0 {
		t.Fatal("oversized pattern must work without retention", err)
	}
}

func BenchmarkRepeatedSubscriptionTitles(b *testing.B) {
	parser, err := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: "rust", CustomWords: []string{
		"Series => Show", "720[Pp] => SD", "1080[Pp] => HD", "Season(\\d+) => S\\1",
		"Episode(\\d+) => E\\1", "\\[Uncut\\]", "HEVC => H265", "AAC2[.]0 => AAC",
	}, ReleaseGroups: []string{"GroupABC"}}})
	if err != nil {
		b.Fatal(err)
	}
	title := "Series.Season01.Episode251.1080p.WEB-DL.HEVC.AAC2.0-GroupABC"
	if _, err := parser.ParseTitle(title, nil, nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.ParseTitle(title, nil, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRustWordLiteralAndRegexpMatches(t *testing.T) {
	for _, c := range []struct {
		title, pattern, replacement, want string
		applied                           bool
	}{
		{"剧集剧集", "剧集", "新", "新新", true}, {"ab", "ab", "ab", "ab", true}, {"a+b", "a+b", "x", "a+b", false},
		{"Fallout", "辽", "x", "Fallout", false}, {"aa", "a", `\0x`, "axax", true}, {"a0", "(.)0", `\1`, "a", true},
		{"a", "", "_", "_a_", true}, {"[abc", "[", "x", "[abc", false},
	} {
		var cache configuredPatterns
		got, hit := cache.replaceRustWord(c.title, c.pattern, c.replacement)
		if got != c.want || hit != c.applied {
			t.Errorf("%q %q %q got %q %v", c.title, c.pattern, c.replacement, got, hit)
		}
	}
}
