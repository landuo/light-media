package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyntheticExclusiveFixtureTree(t *testing.T) {
	old, _ := os.Getwd()
	tmp := t.TempDir()
	os.Chdir(tmp)
	t.Cleanup(func() { os.Chdir(old) })
	os.Mkdir("work", 0700)
	id := "00000000-0000-4000-8000-000000000001"
	root := "work/fixture"
	if e := generate(id, root, false, "legacy", "namespaced"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("dryrun wrote files")
	}
	if e := generate(id, root, true, "legacy", "namespaced"); e != nil {
		t.Fatal(e)
	}
	if e := generate(id, root, true, "legacy", "namespaced"); e == nil {
		t.Fatal("existing root overwritten")
	}
	count := 0
	filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if strings.HasSuffix(p, ".nfo") {
			count++
			b, _ := os.ReadFile(p)
			dec := xml.NewDecoder(bytes.NewReader(b))
			for {
				_, e := dec.Token()
				if e != nil {
					if e.Error() != "EOF" {
						t.Fatal(e)
					}
					break
				}
			}
			if !bytes.Contains(b, []byte(id)) {
				t.Fatal("missing synthetic namespace")
			}
		}
		return nil
	})
	if count != 5 {
		t.Fatal(count)
	}
	dir := filepath.Join(root, "light-media-m0-test-"+id+"-Movie (2024)")
	p, _ := os.ReadFile(filepath.Join(dir, "poster.jpg"))
	f, _ := os.ReadFile(filepath.Join(dir, "fanart.jpg"))
	if sha256.Sum256(p) != sha256.Sum256(f) {
		t.Fatal("historical sample reproduction changed")
	}
	if e := generate(id, "../outside", false, "legacy", "namespaced"); e == nil {
		t.Fatal("outside work accepted")
	}
	if e := generate("", root, false, "legacy", "namespaced"); e == nil {
		t.Fatal("missing UUID accepted")
	}
}

func TestDistinctImageRolesAndLegacyHash(t *testing.T) {
	old, _ := os.Getwd()
	tmp := t.TempDir()
	os.Chdir(tmp)
	t.Cleanup(func() { os.Chdir(old) })
	os.Mkdir("work", 0700)
	id := "00000000-0000-4000-8000-000000000002"
	if e := generate(id, "work/distinct", true, "distinct", "namespaced"); e != nil {
		t.Fatal(e)
	}
	seen := map[[32]byte]bool{}
	for _, suffix := range []string{"-Movie (2024)", "-Series", "-Animation"} {
		for _, name := range []string{"poster.jpg", "fanart.jpg"} {
			p := filepath.Join("work/distinct", "light-media-m0-test-"+id+suffix, name)
			b, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			hash := sha256.Sum256(b)
			if seen[hash] {
				t.Fatal("role/work images share bytes")
			}
			seen[hash] = true
			cfg, _, e := image.DecodeConfig(bytes.NewReader(b))
			if e != nil {
				t.Fatal(e)
			}
			if name == "poster.jpg" && (cfg.Width != 48 || cfg.Height != 72) {
				t.Fatal("wrong poster shape")
			}
			if name == "fanart.jpg" && (cfg.Width != 96 || cfg.Height != 54) {
				t.Fatal("wrong fanart shape")
			}
		}
	}
	if e := generate(id, "work/legacy", true, "legacy", "namespaced"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join("work/legacy", "light-media-m0-test-"+id+"-Movie (2024)", "poster.jpg"))
	if fmt.Sprintf("%x", sha256.Sum256(b)) != "f1834cc17c989c791432ad17580fa777e8c48adc30313304bfe1f99ce5ea8f29" {
		t.Fatal("historical hash changed")
	}
	if e := generate(id, "work/invalid", true, "unknown", "namespaced"); e == nil {
		t.Fatal("unknown mode accepted")
	}
	if _, e := os.Stat("work/invalid"); !os.IsNotExist(e) {
		t.Fatal("invalid mode created output")
	}
}

func TestCanonicalChangesPathsOnly(t *testing.T) {
	old, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() { os.Chdir(old) })
	os.Mkdir("work", 0700)
	id := "12567b4f-a170-40c4-acec-79bf497c9e01" // Keep the E01-containing UUID; never simplify it to pass.
	for _, layout := range []string{"namespaced", "canonical"} {
		if e := generate(id, "work/"+layout, true, "distinct", layout); e != nil {
			t.Fatal(e)
		}
	}
	ns := "light-media-m0-test-" + id
	pairs := [][2]string{
		{ns + "-Movie (2024)/" + ns + "-Movie (2024).nfo", "合成电影 (2024)/合成电影 (2024).nfo"},
		{ns + "-Series/tvshow.nfo", "合成普通剧/tvshow.nfo"},
		{ns + "-Series/Season 1/" + ns + "-Series S01E02.nfo", "合成普通剧/Season 1/合成普通剧 S01E02.nfo"},
		{ns + "-Animation/tvshow.nfo", "合成国漫/tvshow.nfo"},
		{ns + "-Animation/Season 2/" + ns + "-Animation S02E01.nfo", "合成国漫/Season 2/合成国漫 S02E01.nfo"},
	}
	for _, p := range pairs {
		a, e := os.ReadFile(filepath.Join("work/namespaced", p[0]))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join("work/canonical", p[1]))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(a, b) {
			t.Fatal("layout changed XML metadata")
		}
		if strings.Contains(p[1], "S01E02") && (!bytes.Contains(b, []byte("<season>1</season>")) || !bytes.Contains(b, []byte("<episode>2</episode>"))) {
			t.Fatal("ordinary series expectation no longer S01E02")
		}
	}
	seen := map[[32]byte]bool{}
	for i, dir := range []string{"合成电影 (2024)", "合成普通剧", "合成国漫"} {
		for _, file := range []string{"poster.jpg", "fanart.jpg"} {
			b, e := os.ReadFile(filepath.Join("work/canonical", dir, file))
			if e != nil {
				t.Fatal(e)
			}
			legacyDir := []string{ns + "-Movie (2024)", ns + "-Series", ns + "-Animation"}[i]
			a, _ := os.ReadFile(filepath.Join("work/namespaced", legacyDir, file))
			if !bytes.Equal(a, b) || seen[sha256.Sum256(b)] {
				t.Fatal("layout altered or collapsed distinct image roles")
			}
			seen[sha256.Sum256(b)] = true
		}
	}
	if e := generate(id, "work/invalid-layout", true, "distinct", "other"); e == nil {
		t.Fatal("unknown layout accepted")
	}
	if _, e := os.Stat("work/invalid-layout"); !os.IsNotExist(e) {
		t.Fatal("invalid layout wrote output")
	}
}
