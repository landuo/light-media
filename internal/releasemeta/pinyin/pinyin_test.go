package pinyin

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOriginalCompleteInventoriesNameWrappersAndSegmentations(t *testing.T) {
	file, e := os.Open("../../../testdata/plugins/metainfo-pinyin-oracle.ndjson.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	zip, e := gzip.NewReader(file)
	if e != nil {
		t.Fatal(e)
	}
	defer zip.Close()
	d := json.NewDecoder(zip)
	syllables, names, segments := 0, 0, 0
	for {
		var record struct {
			Record string
			Data   json.RawMessage
		}
		if e := d.Decode(&record); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		switch record.Record {
		case "header":
			continue
		case "syllable":
			var c struct {
				Text         string
				Legacy, Rust bool
			}
			if e := json.Unmarshal(record.Data, &c); e != nil {
				t.Fatal(e)
			}
			syllables++
			if got := LegacySyllable(c.Text); got != c.Legacy {
				t.Fatalf("original legacy syllable %q got%t want%t", c.Text, got, c.Legacy)
			}
			if got := RustSyllable(c.Text); got != c.Rust {
				t.Fatalf("original Rust syllable %q got%t want%t", c.Text, got, c.Rust)
			}
		case "name":
			var c struct {
				Text         string
				Legacy, Rust bool
			}
			if e := json.Unmarshal(record.Data, &c); e != nil {
				t.Fatal(e)
			}
			names++
			got, e := LegacyName(c.Text)
			if e != nil || got != c.Legacy {
				t.Fatalf("original legacy name %q got%t/%v want%t", c.Text, got, e, c.Legacy)
			}
			got, e = RustName(c.Text)
			if e != nil || got != c.Rust {
				t.Fatalf("original Rust name %q got%t/%v want%t", c.Text, got, e, c.Rust)
			}
		case "segment":
			var c struct {
				Text  string
				Value [][]string
				Panic bool
			}
			if e := json.Unmarshal(record.Data, &c); e != nil {
				t.Fatal(e)
			}
			segments++
			got, e := Segment(c.Text)
			if c.Panic {
				if !errors.Is(e, ErrRustUTF8Boundary) {
					t.Fatal("original UTF8 slice panic silently changed", e)
				}
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			actual := make([][]string, len(got))
			for i, s := range got {
				actual[i] = s.Syllables
			}
			if !reflect.DeepEqual(actual, c.Value) {
				t.Fatalf("original complete ordered segmentation %q got%v want%v", c.Text, actual, c.Value)
			}
		default:
			t.Fatal(record.Record)
		}
	}
	if len(LegacyInventory()) != 411 || len(RustInventory()) != 403 || syllables != 2629 || names != 180608 || segments != 2508 {
		t.Fatalf("incomplete source reference %d/%d/%d", syllables, names, segments)
	}
}
func TestNameBooleanUsesExactReachabilityWithoutEnumeratingExponentialPaths(t *testing.T) {
	name := strings.Repeat("xian", 10000)
	ok, err := RustName(name)
	if err != nil || !ok {
		t.Fatal("exact complete-split reachability lost", ok, err)
	}
	ok, err = RustName(name + "q")
	if err != nil || ok {
		t.Fatal("partial split must not count as complete", ok, err)
	}
}
func TestInvalidGoUTF8DoesNotBecomeAValidRustOrPythonString(t *testing.T) {
	value := string([]byte{0xff})
	if _, err := LegacyName(value); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatal(err)
	}
	if _, err := RustName(value); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatal(err)
	}
	if _, err := Segment(value); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatal(err)
	}
}
func TestReturnedInventoriesCannotMutateSharedTables(t *testing.T) {
	inventory := LegacyInventory()
	inventory[0] = "not-a-syllable"
	if !LegacySyllable("a") {
		t.Fatal("caller mutated legacy table")
	}
	rust := RustInventory()
	rust[0] = "not-a-syllable"
	if !RustSyllable("a") {
		t.Fatal("caller mutated rust table")
	}
}
