package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
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
	if e := generate(id, root, false); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("dryrun wrote files")
	}
	if e := generate(id, root, true); e != nil {
		t.Fatal(e)
	}
	if e := generate(id, root, true); e == nil {
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
	if e := generate(id, "../outside", false); e == nil {
		t.Fatal("outside work accepted")
	}
	if e := generate("", root, false); e == nil {
		t.Fatal("missing UUID accepted")
	}
}
