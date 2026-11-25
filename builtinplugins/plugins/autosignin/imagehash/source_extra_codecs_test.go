package imagehash

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Expected pixels and hashes come from the original installed _tohash body and
// source Pillow decoder, not the Go encoder or a Go round-trip.
func TestSourceExtraImageCodecs(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-extra-codecs.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name      string `json:"name"`
			SHA256    string `json:"sha256"`
			Base64    string `json:"base64"`
			Hash      string `json:"hash"`
			Gray      []byte `json:"gray"`
			Size      [2]int `json:"loaded_size"`
			Mode      string `json:"loaded_mode"`
			LoadedSHA string `json:"loaded_sha256"`
			Error     string `json:"error"`
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(c.Base64)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != c.SHA256 {
				t.Fatal("source fixture bytes changed")
			}
			r, err := Decode(data)
			if c.Error != "" {
				if err == nil {
					t.Fatalf("source rejected image: %s", c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Width != c.Size[0] || r.Height != c.Size[1] {
				t.Fatal("source dimensions", r.Width, r.Height, c.Size)
			}
			if c.LoadedSHA != "" {
				pixels := r.Pixels
				if c.Mode == "1" && r.Mode == "1" {
					stride := (r.Width + 7) / 8
					pixels = make([]byte, stride*r.Height)
					for y := 0; y < r.Height; y++ {
						for x := 0; x < r.Width; x++ {
							if r.Pixels[y*r.Width+x] != 0 {
								pixels[y*stride+x/8] |= 1 << (7 - x%8)
							}
						}
					}
				} else if c.Mode == "RGB" && r.Mode == "RGBA" {
					// The JP2 decoder stores opaque RGB in an NRGBA image. Its
					// extra storage byte is internal, not a source alpha channel.
					pixels = make([]byte, 0, r.Width*r.Height*3)
					for i := 0; i < len(r.Pixels); i += 4 {
						if r.Pixels[i+3] != 255 {
							t.Fatal("unexpected source alpha")
						}
						pixels = append(pixels, r.Pixels[i:i+3]...)
					}
				} else if c.Mode != r.Mode {
					t.Fatalf("source mode got %s want %s", r.Mode, c.Mode)
				}
				if fmt.Sprintf("%x", sha256.Sum256(pixels)) != c.LoadedSHA {
					t.Error("decoded source pixels differ before resize")
				}
			}
			gray, err := Gray(r, [2]int{10, 10})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gray, c.Gray) {
				n := 0
				first := -1
				for i, v := range gray {
					if i >= len(c.Gray) || v != c.Gray[i] {
						n++
						if first < 0 {
							first = i
						}
					}
				}
				t.Errorf("source gray differs: %d pixels, first=%d", n, first)
			}
			got, err := Hash(data)
			if err != nil || got != c.Hash {
				t.Errorf("source 100-bit hash differs: got=%s want=%s err=%v", got, c.Hash, err)
			}
			if c.Name == "AVIF-high-mono-12" {
				t.Run("reserved-matrix", func(t *testing.T) {
					// libavif's reformat guard rejects the reserved CICP matrix.
					// Change just the matrix field of the valid nclx color box.
					color := bytes.Index(data, []byte("nclx"))
					if color < 0 || color+10 > len(data) || binary.BigEndian.Uint16(data[color+8:color+10]) != 6 {
						t.Fatal("unexpected source nclx box")
					}
					invalid := bytes.Clone(data)
					binary.BigEndian.PutUint16(invalid[color+8:color+10], 3)
					if _, err := Decode(invalid); err == nil {
						t.Fatal("reserved CICP matrix decoded")
					}
				})
			}
		})
	}
}
