// Package fixturelayout defines the two explicit local M0 fixture layouts.
package fixturelayout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Work struct{ Dir, Group, Label, Season, EpisodeStem string }

func Names(mode, id string) ([]Work, error) {
	ns := "light-media-m0-test-" + id
	switch mode {
	case "namespaced":
		return []Work{{ns + "-Movie (2024)", "movie", "Movie (2024)", "", ""}, {ns + "-Series", "series", "Series", "Season 1", ns + "-Series S01E02"}, {ns + "-Animation", "animation", "Animation", "Season 2", ns + "-Animation S02E01"}}, nil
	case "canonical":
		return []Work{{"合成电影 (2024)", "movie", "Movie (2024)", "", ""}, {"合成普通剧", "series", "Series", "Season 1", "合成普通剧 S01E02"}, {"合成国漫", "animation", "Animation", "Season 2", "合成国漫 S02E01"}}, nil
	default:
		return nil, errors.New("INVALID_FIXTURE_LAYOUT")
	}
}

// Detect accepts one complete layout, never a mixed set or arbitrary directory.
// Season directories may be absent in image-only fixtures. Files are validated
// by the relevant XML/image reader; symlinks and more than 64 entries are refused.
func Detect(root, id string) ([]Work, error) {
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, errors.New("FIXTURE_UNAVAILABLE")
	}
	var selected []Work
	for _, mode := range []string{"namespaced", "canonical"} {
		works, _ := Names(mode, id)
		expected := map[string]bool{}
		for _, w := range works {
			expected[w.Dir] = true
		}
		ok := len(entries) == len(works)
		for _, entry := range entries {
			if !entry.IsDir() || !expected[entry.Name()] {
				ok = false
			}
		}
		if ok {
			selected = works
			break
		}
	}
	if selected == nil {
		return nil, errors.New("FIXTURE_LAYOUT_MISMATCH")
	}
	allowed := map[string]bool{".": true}
	for _, w := range selected {
		allowed[w.Dir] = true
		if w.Season != "" {
			allowed[filepath.Join(w.Dir, w.Season)] = true
		}
	}
	count := 0
	e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return errors.New("FIXTURE_UNAVAILABLE")
		}
		count++
		if count > 64 || d.Type()&os.ModeSymlink != 0 {
			return errors.New("FIXTURE_LIMIT_OR_SYMLINK")
		}
		rel, e := filepath.Rel(root, p)
		if e != nil || strings.HasPrefix(rel, "..") {
			return errors.New("FIXTURE_LAYOUT_MISMATCH")
		}
		if d.IsDir() && !allowed[rel] {
			return errors.New("FIXTURE_LAYOUT_MISMATCH")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return selected, nil
}
