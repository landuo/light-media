package imagehash

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// These encoded bytes, loaded modes, pixels and final hashes were recorded by
// the installed333 Pillow readers and original Tjupt _tohash, not a Go encoder.
func TestAdditionalSourceReaders(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-extra-codecs.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name, SHA256, Base64, Hash string
			Mode                       string `json:"loaded_mode"`
			Size                       [2]int `json:"loaded_size"`
			Gray                       []byte
			LoadedSHA256               string `json:"loaded_sha256"`
		}
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, c := range doc.Cases {
		kind := strings.SplitN(c.Name, "-", 2)[0]
		switch kind {
		case "BLP", "ICNS", "IM", "QOI", "SPIDER", "MSP", "XBM":
		default:
			continue
		}
		count++
		t.Run(c.Name, func(t *testing.T) {
			data, e := base64.StdEncoding.DecodeString(c.Base64)
			if e != nil {
				t.Fatal(e)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != c.SHA256 {
				t.Fatal("source bytes changed")
			}
			r, ok, e := decodeAdditional(data)
			if e != nil || !ok {
				t.Fatal("reader", ok, e)
			}
			if r.Mode != c.Mode || r.Width != c.Size[0] || r.Height != c.Size[1] {
				t.Fatal("source mode/size", r.Mode, r.Width, r.Height, c.Mode, c.Size)
			}
			loaded := r.Pixels
			if r.Mode == "1" {
				// Pillow.tobytes packs each mode-1 row MSB-first; Raster
				// deliberately stores one 0/255 byte per pixel for resize.
				stride := (r.Width + 7) / 8
				loaded = make([]byte, stride*r.Height)
				for y := 0; y < r.Height; y++ {
					for x := 0; x < r.Width; x++ {
						if r.Pixels[y*r.Width+x] != 0 {
							loaded[y*stride+x/8] |= 1 << uint(7-x%8)
						}
					}
				}
			}
			if c.LoadedSHA256 != "" && fmt.Sprintf("%x", sha256.Sum256(loaded)) != c.LoadedSHA256 {
				t.Error("decoded source pixels differ")
			}
			gray, e := Gray(r, [2]int{10, 10})
			if e != nil || !bytes.Equal(gray, c.Gray) {
				t.Error("source gray100 differs", e)
			}
			hash, e := Raw(r, [2]int{10, 10})
			if e != nil || hash != c.Hash {
				t.Error("source100bit hash differs", hash, c.Hash, e)
			}
		})
	}
	if count != 13 {
		t.Fatalf("expected13 original successful additional-reader samples, got%d", count)
	}
}

func TestAdditionalBLP1Source(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-blp1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name, SHA256, Base64, Hash string
			Mode                       string `json:"loaded_mode"`
			Size                       [2]int `json:"loaded_size"`
			Gray                       []byte
			LoadedSHA256               string `json:"loaded_sha256"`
		}
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 2 {
		t.Fatal("two actual BLP1 source controls required")
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(c.Base64)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != c.SHA256 {
				t.Fatal("source bytes changed")
			}
			r, ok, err := decodeAdditional(data)
			if err != nil || !ok {
				t.Fatal("source positive decode", ok, err)
			}
			if r.Mode != c.Mode || r.Width != c.Size[0] || r.Height != c.Size[1] {
				t.Fatal("source mode/size differs")
			}
			if fmt.Sprintf("%x", sha256.Sum256(r.Pixels)) != c.LoadedSHA256 {
				t.Error("source loaded pixels differ")
			}
			gray, err := Gray(r, [2]int{10, 10})
			if err != nil || !bytes.Equal(gray, c.Gray) {
				t.Error("source gray100 differs", err)
			}
			hash, err := Raw(r, [2]int{10, 10})
			if err != nil || hash != c.Hash {
				t.Error("source100bit hash differs", hash, c.Hash, err)
			}
		})
	}
}
