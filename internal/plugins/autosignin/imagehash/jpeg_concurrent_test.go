package imagehash

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"sync"
	"testing"
)

// Decoders have per-call entropy/progressive state. Actual source-produced
// bytes and decoded pixels exercise independent concurrent decoder instances.
func TestConcurrentJPEG(t *testing.T) {
	f, err := os.Open("../../../../testdata/plugins/auto-signin-image-hash/jpeg/jpeg-fullblock-pillow-12.2.0.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	dec := json.NewDecoder(g)
	var meta map[string]any
	if err = dec.Decode(&meta); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Encoded, Pixels []byte
		Mode            string
		Width, Height   int
	}
	if err = dec.Decode(&c); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				got, err := DecodeJPEG(c.Encoded)
				if err != nil || got.Mode != c.Mode || got.Width != c.Width || got.Height != c.Height || !bytes.Equal(got.Pixels, c.Pixels) {
					t.Errorf("concurrent decode changed source pixels: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
