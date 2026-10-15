package metainfo

import (
	"os"
	"strings"
	"testing"
)

func TestProductMaterialIdentityAndSafePaths(t *testing.T) {
	b, e := os.ReadFile("../../testdata/protocol/synthetic-torrent-v1.torrent")
	if e != nil {
		t.Fatal(e)
	}
	m, e := ParseMaterial(b)
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Hash) != 40 || len(m.Files) != 1 || m.Files[0].Size != m.TotalBytes {
		t.Fatalf("material: %+v", m)
	}
	if strings.Contains(m.Hash, "http") || strings.Contains(m.Files[0].Path, "..") {
		t.Fatal("unsafe summary")
	}
	changed := strings.Replace(string(b), "6:sample", "6:../bad", 1)
	if changed != string(b) {
		if _, e = ParseMaterial([]byte(changed)); e == nil {
			t.Fatal("accepted traversal")
		}
	}
}
func TestProductLimitDoesNotChangeProbeLimit(t *testing.T) {
	b := make([]byte, MaxBody+1)
	if _, e := Inspect(b); e == nil || e.Error() != "body_limit" {
		t.Fatal("probe body limit changed")
	}
	if _, e := ProductInspect(b); e == nil || e.Error() == "body_limit" {
		t.Fatal("product parser did not reach bounded parser")
	}
	if _, e := ProductInspect(make([]byte, (32<<20)+1)); e == nil || e.Error() != "body_limit" {
		t.Fatal("product body limit missing")
	}
}
func TestSafeMaterialComponent(t *testing.T) {
	for _, s := range []string{"..", ".", "a/b", "a\\b", "x\x00", "x\n"} {
		if safeComponent(s) {
			t.Fatal("accepted", s)
		}
	}
	if !safeComponent("中文 S01E01.mkv") {
		t.Fatal("rejected unicode")
	}
}
