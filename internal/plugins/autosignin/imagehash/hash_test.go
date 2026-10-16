package imagehash

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
)

type reference struct {
	Pillow       string
	SourceSHA256 string `json:"source_sha256"`
	Raw          []struct {
		Name string
		Raster
		Shape [2]int
		Gray  []byte
		Hash  string
	}
	Encoded []struct {
		Name, Format, Mode    string
		Encoded, Pixels, Gray []byte
		Width, Height         int
		Hash                  string
	}
	Comparison []struct {
		A, B  string
		Shape [2]int
		Value float64
	}
}

func fixture(t testing.TB) reference {
	return loadFixture(t, "../../../../testdata/plugins/auto-signin-image-hash/pillow-12.2.0-reference.json.gz", true)
}

func loadFixture(t testing.TB, path string, baseline bool) reference {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	g, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	var ref reference
	if e = json.NewDecoder(g).Decode(&ref); e != nil {
		t.Fatal(e)
	}
	if baseline && (ref.Pillow != "12.2.0" || len(ref.Raw) != 336) {
		t.Fatalf("wrong source fixture: %s/%d", ref.Pillow, len(ref.Raw))
	}
	return ref
}

func TestPillowAdditionalRawAndEncodedBoundaries(t *testing.T) {
	ref := loadFixture(t, "../../../../testdata/plugins/auto-signin-image-hash/pillow-12.2.0-edges.json.gz", false)
	if len(ref.Raw) != 252 || len(ref.Encoded) != 20 {
		t.Fatalf("wrong edge fixture sizes %d/%d", len(ref.Raw), len(ref.Encoded))
	}
	if ref.SourceSHA256 != fixture(t).SourceSHA256 {
		t.Fatal("source changed across reference rounds")
	}
	for _, c := range ref.Raw {
		t.Run("raw-"+c.Name, func(t *testing.T) {
			g, e := Gray(c.Raster, c.Shape)
			assertGray(t, g, c.Gray, e)
			h, e := Raw(c.Raster, c.Shape)
			if e != nil || h != c.Hash {
				t.Fatalf("hash mismatch %v: got %s want %s", e, h, c.Hash)
			}
		})
	}
	for _, c := range ref.Encoded {
		t.Run(c.Name, func(t *testing.T) {
			r, e := Decode(c.Encoded)
			if e != nil {
				t.Fatal(e)
			}
			if r.Mode != c.Mode || r.Width != c.Width || r.Height != c.Height {
				t.Fatalf("mode/shape mismatch %s/%dx%d want %s/%dx%d", r.Mode, r.Width, r.Height, c.Mode, c.Width, c.Height)
			}
			g, e := Gray(r, [2]int{10, 10})
			assertGray(t, g, c.Gray, e)
			h, e := Hash(c.Encoded)
			if e != nil || h != c.Hash {
				t.Fatalf("hash mismatch %v: got %s want %s", e, h, c.Hash)
			}
		})
	}
}

func assertGray(t *testing.T, got, want []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("gray length %d want %d", len(got), len(want))
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("gray pixel %d got %d want %d", i, v, want[i])
		}
	}
}

func TestActualPillowRawReference(t *testing.T) {
	for _, c := range fixture(t).Raw {
		t.Run(c.Name, func(t *testing.T) {
			g, e := Gray(c.Raster, c.Shape)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(g, c.Gray) {
				for i := range g {
					if g[i] != c.Gray[i] {
						t.Fatalf("gray pixel %d: got %d want %d", i, g[i], c.Gray[i])
					}
				}
			}
			h, e := Raw(c.Raster, c.Shape)
			if e != nil {
				t.Fatal(e)
			}
			if h != c.Hash {
				t.Fatalf("hash got %s want %s", h, c.Hash)
			}
		})
	}
}

func TestCompareActualSourceReference(t *testing.T) {
	for _, c := range fixture(t).Comparison {
		got, e := CompareShape(c.A, c.B, c.Shape)
		if e != nil || got != c.Value {
			t.Fatalf("compare %q/%q shape%v: %v/%v want %v", c.A, c.B, c.Shape, got, e, c.Value)
		}
	}
	if Compare("a", "ab") != -1 || Compare("你好", "你好") != .02 {
		t.Fatal("source character/length behavior changed")
	}
	if Compare("x"+string(bytes.Repeat([]byte("1"), 99)), string(bytes.Repeat([]byte("1"), 100))) > .99 {
		t.Fatal("strict denominator")
	}
}

func TestInputBounds(t *testing.T) {
	for _, r := range []Raster{{Mode: "RGB", Width: 8193, Height: 1}, {Mode: "RGB", Width: 2, Height: 2, Pixels: make([]byte, 11)}, {Mode: "P", Width: 1, Height: 1, Pixels: []byte{1}}, {Mode: "L", Width: -1, Height: 1}} {
		if _, e := Raw(r, [2]int{10, 10}); e == nil {
			t.Fatal("accepted invalid raster")
		}
	}
	if _, e := Raw(Raster{Mode: "I;16"}, [2]int{10, 10}); e != ErrUnsupported {
		t.Fatal(e)
	}
	if _, e := CompareShape("a", "a", [2]int{0, 10}); e != ErrInvalid {
		t.Fatal(e)
	}
}
