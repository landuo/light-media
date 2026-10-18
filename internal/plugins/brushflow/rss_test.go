package brushflow

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	enc "light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	"os"
	"testing"
	"time"
)

func TestActualRust026RSSOracle(t *testing.T) {
	b, e := os.ReadFile("reference/framework/rss-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Version string
		Cases   []struct {
			Text     string
			Expected struct {
				Value      []Row
				ErrorClass string `json:"error_class"`
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e = decoder.Decode(&source); e != nil || source.Version != "0.2.6" {
		t.Fatal(e, source.Version)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, e := ParseRustRSS(c.Text, 1001, loc)
			if c.Expected.ErrorClass != "" {
				if e == nil {
					t.Fatal("source parser error accepted", got)
				}
				return
			}
			if e != nil || !bytes.Equal(rssCanonicalJSON(t, got), rssCanonicalJSON(t, c.Expected.Value)) {
				t.Fatal("RSS source mismatch", e, string(mustRSSJSON(got)), string(mustRSSJSON(c.Expected.Value)))
			}
		})
	}
}
func mustRSSJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func rssCanonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&result); err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestActualXMLDecodeSourceOracle(t *testing.T) {
	b, err := os.ReadFile("reference/framework/rss-decode-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Chardet string
		Cases   []struct {
			Name        string
			RawHex      string `json:"raw_hex"`
			ContentType string `json:"content_type"`
			Expected    struct {
				Text       string
				ErrorClass string `json:"error_class"`
			}
		}
	}
	if err = json.Unmarshal(b, &source); err != nil || source.Chardet != "7.4.3" {
		t.Fatal(err, source.Chardet)
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(c.RawHex)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeRSSXML(raw, c.ContentType)
			if c.Expected.ErrorClass != "" {
				if enc.ErrorClass(err) != c.Expected.ErrorClass {
					t.Fatal("source error class", err, c.Expected.ErrorClass)
				}
				return
			}
			if err != nil || got != c.Expected.Text {
				t.Fatal("source XML decoding mismatch", err, got, c.Expected.Text)
			}
		})
	}
}
