package imagehash

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestActualPillowSourceImages(t *testing.T) {
	b, err := os.ReadFile("testdata/pillow-source-images-r3.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name, Base64, Hash string
			LoadedBase64       string `json:"loaded_base64"`
			LoadedMode         string `json:"loaded_mode"`
			GzipBase64         string `json:"gzip_base64"`
			Gray               []byte
			Error              any
		}
	}
	if err = json.Unmarshal(b, &source); err != nil {
		t.Fatal(err)
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Base64 == "" && c.GzipBase64 == "" {
				t.Skip("source r1 omitted large encoded bytes; source r2 captures gzip")
			}
			data, err := base64.StdEncoding.DecodeString(c.Base64)
			if err != nil {
				t.Fatal(err)
			}
			if c.GzipBase64 != "" {
				packed, e := base64.StdEncoding.DecodeString(c.GzipBase64)
				if e != nil {
					t.Fatal(e)
				}
				r, e := gzip.NewReader(bytes.NewReader(packed))
				if e != nil {
					t.Fatal(e)
				}
				data, err = io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := Hash(data)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.Hash {
				t.Errorf("hash got %q want actual original %q", got, c.Hash)
			}
			if c.Gray != nil {
				r, err := Decode(data)
				if err != nil {
					t.Fatal(err)
				}
				wantPixels, e := base64.StdEncoding.DecodeString(c.LoadedBase64)
				if e != nil {
					t.Fatal(e)
				}
				if c.LoadedBase64 != "" && !bytes.Equal(r.Pixels, wantPixels) {
					t.Errorf("decoded pixels differ from actual Pillow mode %s", c.LoadedMode)
				}
				got, err := Gray(r, [2]int{10, 10})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, c.Gray) {
					t.Errorf("gray got %v want actual Pillow %v", got, c.Gray)
				}
			}
		})
	}
}

func TestActualPillowBombBoundaries(t *testing.T) {
	b, err := os.ReadFile("testdata/pillow-source-images-r3.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pixels   int
			Error    *struct{ Message string }
			Warnings []struct{ Message string }
		} `json:"bomb_check_cases"`
	}
	if err = json.Unmarshal(b, &source); err != nil {
		t.Fatal(err)
	}
	for _, c := range source.Cases {
		t.Run(fmt.Sprint(c.Pixels), func(t *testing.T) {
			warning, err := imageSizeOutcome(c.Pixels, 1)
			if c.Error != nil {
				if err == nil || err.Error() != c.Error.Message {
					t.Fatalf("got %v want actual %s", err, c.Error.Message)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := ""
			if len(c.Warnings) > 0 {
				want = c.Warnings[0].Message
			}
			if warning != want {
				t.Fatalf("warning got %q want %q", warning, want)
			}
		})
	}
}
