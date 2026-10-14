package fixturelayout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitLayoutsOnly(t *testing.T) {
	for _, mode := range []string{"namespaced", "canonical"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			works, _ := Names(mode, "synthetic-id")
			for _, w := range works {
				if e := os.Mkdir(filepath.Join(root, w.Dir), 0700); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := Detect(root, "synthetic-id"); e != nil {
				t.Fatal(e)
			}
			arbitrary := filepath.Join(root, works[1].Dir, "Season 99")
			os.Mkdir(arbitrary, 0700)
			if _, e := Detect(root, "synthetic-id"); e == nil {
				t.Fatal("arbitrary nested directory accepted")
			}
			os.Remove(arbitrary)
			otherMode := "canonical"
			if mode == otherMode {
				otherMode = "namespaced"
			}
			other, _ := Names(otherMode, "synthetic-id")
			os.Rename(filepath.Join(root, works[0].Dir), filepath.Join(root, other[0].Dir))
			if _, e := Detect(root, "synthetic-id"); e == nil {
				t.Fatal("mixed layout accepted")
			}
			os.Rename(filepath.Join(root, other[0].Dir), filepath.Join(root, works[0].Dir))
			os.Mkdir(filepath.Join(root, "arbitrary"), 0700)
			if _, e := Detect(root, "synthetic-id"); e == nil {
				t.Fatal("extra root directory accepted")
			}
		})
	}
	if _, e := Names("auto", "id"); e == nil {
		t.Fatal("unknown layout accepted")
	}
}
