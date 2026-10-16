package imagehash

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"sync"
	"testing"
)

func TestEncodedActualPillowReference(t *testing.T) {
	count := 0
	for _, c := range fixture(t).Encoded {
		if c.Format == "JPEG" {
			continue
		}
		count++
		t.Run(c.Name, func(t *testing.T) {
			r, e := Decode(c.Encoded)
			if e != nil {
				t.Fatal(e)
			}
			if r.Mode != c.Mode {
				t.Fatalf("mode %s want %s", r.Mode, c.Mode)
			}
			g, e := Gray(r, [2]int{10, 10})
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(g, c.Gray) {
				t.Fatalf("gray differs got %v want %v", g, c.Gray)
			}
			h, e := Hash(c.Encoded)
			if e != nil || h != c.Hash {
				t.Fatalf("hash differs %s/%v want %s", h, e, c.Hash)
			}
		})
	}
	if count != 140 {
		t.Fatalf("unexpected fixture count %d", count)
	}
}

func TestMalformedAndBoundedDecode(t *testing.T) {
	var pngBytes, gifBytes []byte
	for _, c := range fixture(t).Encoded {
		if c.Format == "PNG" && c.Mode == "RGB" && pngBytes == nil {
			pngBytes = append([]byte(nil), c.Encoded...)
		}
		if c.Format == "GIF" && gifBytes == nil {
			gifBytes = append([]byte(nil), c.Encoded...)
		}
	}
	for _, b := range [][]byte{nil, {}, []byte("GIF89a"), []byte("not an image"), pngBytes[:len(pngBytes)/2], gifBytes[:13], append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 18)...)} {
		if _, e := Hash(b); e == nil {
			t.Fatal("accepted malformed image")
		}
	}
	large := append([]byte(nil), gifBytes...)
	binary.LittleEndian.PutUint16(large[6:8], 65535)
	if _, e := Hash(large); e != ErrInvalid {
		t.Fatalf("unbounded GIF accepted %v", e)
	}
	depth16 := append([]byte(nil), pngBytes...)
	depth16[24] = 16
	if _, e := Hash(depth16); e != ErrUnsupported {
		t.Fatalf("unverified 16-bit PNG accepted %v", e)
	}
	if _, e := Hash(make([]byte, (8<<20)+1)); e != ErrInvalid {
		t.Fatal("unbounded encoding accepted")
	}
}

func TestConcurrentInputsRemainUnchanged(t *testing.T) {
	ref := fixture(t)
	var jobs sync.WaitGroup
	for _, c := range ref.Encoded {
		if c.Format == "JPEG" {
			continue
		}
		before := append([]byte(nil), c.Encoded...)
		for i := 0; i < 3; i++ {
			jobs.Go(func() {
				h, e := Hash(c.Encoded)
				if e != nil || h != c.Hash {
					t.Errorf("concurrent %s differs %v", c.Name, e)
				}
			})
		}
		jobs.Wait()
		if !bytes.Equal(c.Encoded, before) {
			t.Fatalf("mutated encoding %s", c.Name)
		}
	}
}

func FuzzDecodeBounded(f *testing.F) {
	for _, c := range fixture(f).Encoded {
		if c.Width == 1 && c.Height == 1 {
			f.Add(c.Encoded)
		}
	}
	f.Add([]byte("GIF89a"))
	f.Add([]byte("\x89PNG\r\n\x1a\n"))
	f.Add([]byte{255, 216, 255, 217})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 8<<20 {
			return
		}
		_, _ = Hash(b)
	})
}

// This diagnostic rejects the tempting assumption that the stdlib JPEG output
// is a pixel-identical Pillow replacement. It never enables JPEG in Hash.
func TestStandardJPEGDecoderDifference(t *testing.T) {
	count, grayDiff, hashDiff := 0, 0, 0
	for _, c := range fixture(t).Encoded {
		if c.Format != "JPEG" {
			continue
		}
		count++
		im, e := jpeg.Decode(bytes.NewReader(c.Encoded))
		if e != nil {
			t.Fatal(e)
		}
		var r Raster
		if c.Mode == "CMYK" {
			m, ok := im.(*image.CMYK)
			if !ok {
				t.Fatalf("unexpected JPEG mode %T", im)
			}
			r = Raster{Mode: "CMYK", Width: c.Width, Height: c.Height, Pixels: append([]byte(nil), m.Pix...)}
		} else {
			r, e = fromImage(im, c.Mode)
			if e != nil {
				t.Fatal(e)
			}
		}
		g, e := Gray(r, [2]int{10, 10})
		if e != nil {
			t.Fatal(e)
		}
		h, e := Raw(r, [2]int{10, 10})
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(g, c.Gray) {
			grayDiff++
		}
		if h != c.Hash {
			hashDiff++
		}
		if h, e := Hash(c.Encoded); e != nil || h != c.Hash {
			t.Fatalf("verified JPEG pipeline differs %s/%v want %s", h, e, c.Hash)
		}
	}
	t.Logf("synthetic JPEG references=%d gray differences=%d hash differences=%d", count, grayDiff, hashDiff)
	if count != 42 || grayDiff == 0 {
		t.Fatalf("missing JPEG mismatch evidence %d/%d/%d", count, grayDiff, hashDiff)
	}
}
