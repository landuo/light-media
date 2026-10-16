package imagehash

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestInitialPillowJPEG(t *testing.T) {
	f, e := os.Open("../../../../testdata/plugins/auto-signin-image-hash/pillow-12.2.0-reference.json.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	g, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	var corpus struct {
		Encoded []struct {
			Name, Format, Mode    string
			Encoded, Pixels, Gray []byte
			Width, Height         int
			Hash                  string
		}
	}
	if e = json.NewDecoder(g).Decode(&corpus); e != nil {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for _, c := range corpus.Encoded {
		if c.Format != "JPEG" {
			continue
		}
		r, e := DecodeJPEG(c.Encoded)
		if e != nil {
			t.Error(c.Name, e)
			continue
		}
		if r.Mode != c.Mode || r.Width != c.Width || r.Height != c.Height || len(r.Pixels) != len(c.Pixels) {
			t.Errorf("%s shape/mode", c.Name)
			continue
		}
		diff, maxDiff := 0, 0
		for i, p := range c.Pixels {
			d := int(r.Pixels[i]) - int(p)
			if d < 0 {
				d = -d
			}
			if d > 0 {
				diff++
			}
			if d > maxDiff {
				maxDiff = d
			}
		}
		if diff > 0 {
			t.Errorf("%s pixel mismatch", c.Name)
			counts[c.Mode]++
			t.Logf("%s pixels diff=%d/%d max=%d", c.Name, diff, len(c.Pixels), maxDiff)
		}
		gray, e := Gray(r, [2]int{10, 10})
		if e != nil {
			t.Fatal(e)
		}
		hash, _ := Raw(r, [2]int{10, 10})
		if !bytes.Equal(gray, c.Gray) {
			t.Errorf("%s gray mismatch", c.Name)
			t.Logf("%s graydiff", c.Name)
		}
		if hash != c.Hash {
			t.Errorf("%s hash mismatch", c.Name)
			t.Logf("%s HASH DIFF", c.Name)
		}
	}
	t.Log(counts)
}

func TestExpandedPillowJPEG(t *testing.T) {
	testJPEGCorpus(t, "../../../../testdata/plugins/auto-signin-image-hash/jpeg/jpeg-pillow-12.2.0.jsonl.gz", 892)
}
func TestTablePillowJPEG(t *testing.T) {
	testJPEGCorpus(t, "../../../../testdata/plugins/auto-signin-image-hash/jpeg/jpeg-table-pillow-12.2.0.jsonl.gz", 21)
}
func TestFullBlockPillowJPEG(t *testing.T) {
	testJPEGCorpus(t, "../../../../testdata/plugins/auto-signin-image-hash/jpeg/jpeg-fullblock-pillow-12.2.0.jsonl.gz", 180)
}
func TestCoefficientPillowJPEG(t *testing.T) {
	testJPEGCorpus(t, "../../../../testdata/plugins/auto-signin-image-hash/jpeg/jpeg-coefficient-pillow-12.2.0.jsonl.gz", 126)
}
func TestMarkerPillowJPEG(t *testing.T) {
	testJPEGCorpus(t, "../../../../testdata/plugins/auto-signin-image-hash/jpeg/jpeg-marker-pillow-12.2.0.jsonl.gz", 188)
}
func testJPEGCorpus(t *testing.T, path string, wantCount int) {
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
	decoder := json.NewDecoder(g)
	var meta map[string]any
	if e = decoder.Decode(&meta); e != nil || meta["libjpeg_turbo"] != "3.1.4.1" {
		t.Fatal("wrong original runtime", e, meta)
	}
	count, badPixels, badGray, badHash, badErrors := 0, 0, 0, 0, 0
	for {
		var c struct {
			Name, Mode            string
			Encoded, Pixels, Gray []byte
			Width, Height         int
			Hash                  string
			Error                 *string
		}
		e = decoder.Decode(&c)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		count++
		r, e := DecodeJPEG(c.Encoded)
		if c.Error != nil {
			if e == nil {
				badErrors++
				t.Errorf("%s Pillow error %s, Go accepts", c.Name, *c.Error)
			}
			continue
		}
		if e != nil {
			badErrors++
			t.Errorf("%s Go error %v", c.Name, e)
			continue
		}
		if r.Mode != c.Mode || r.Width != c.Width || r.Height != c.Height || !bytes.Equal(r.Pixels, c.Pixels) {
			badPixels++
			diff, maxDiff := 0, 0
			for i, p := range c.Pixels {
				if i >= len(r.Pixels) {
					break
				}
				d := int(r.Pixels[i]) - int(p)
				if d < 0 {
					d = -d
				}
				if d > 0 {
					diff++
				}
				maxDiff = max(maxDiff, d)
			}
			t.Logf("%s PIXELS %d/%d max=%d", c.Name, diff, len(c.Pixels), maxDiff)
		}
		gray, e := Gray(r, [2]int{10, 10})
		if e != nil {
			t.Fatal(e)
		}
		hash, e := Raw(r, [2]int{10, 10})
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(gray, c.Gray) {
			badGray++
			t.Log(c.Name, "GRAY")
		}
		if hash != c.Hash {
			badHash++
			t.Log(c.Name, "HASH")
		}
	}
	if count != wantCount {
		t.Fatal("case count", count)
	}
	if badPixels+badGray+badHash+badErrors > 0 {
		t.Fatalf("%dcases pixels=%d gray=%d hash=%d errors=%d", count, badPixels, badGray, badHash, badErrors)
	}
}
