package noticetemplate

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"light-media/internal/jinja/exec"
)

func cases(t *testing.T, name string, fn func(*testing.T, []byte)) {
	t.Helper()
	f, e := os.Open("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	s := bufio.NewScanner(z)
	s.Buffer(make([]byte, 65536), 4<<20)
	for s.Scan() {
		var n struct{ Name string }
		if e = json.Unmarshal(s.Bytes(), &n); e != nil {
			t.Fatal(e)
		}
		raw := append([]byte(nil), s.Bytes()...)
		t.Run(n.Name, func(t *testing.T) { fn(t, raw) })
	}
	if e = s.Err(); e != nil {
		t.Fatal(e)
	}
}
func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}
func equalJSON(a, b any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return string(ra) == string(rb)
}
func TestContextSourceOracle(t *testing.T) {
	cases(t, "context.jsonl.gz", func(t *testing.T, raw []byte) {
		var c struct {
			Input struct {
				Meta, Media, Torrent, Transfer Object
				Episodes                       []Object
				FileExtension                  any   `json:"file_extension"`
				IncludeRawObjects              *bool `json:"include_raw_objects"`
				Extra                          Object
			}
			Output struct {
				Value        Object
				Error        string
				TorrentAfter Object `json:"torrent_after"`
			}
		}
		if e := decode(raw, &c); e != nil {
			t.Fatal(e)
		}
		i := c.Input
		out, e := BuildContext(ContextInput{Meta: i.Meta, Media: i.Media, Torrent: i.Torrent, Transfer: i.Transfer, Episodes: i.Episodes, FileExtension: i.FileExtension, IncludeRawObjects: i.IncludeRawObjects, Extra: i.Extra})
		if c.Output.Error != "" {
			var se SourceError
			if !errors.As(e, &se) || se.Class != c.Output.Error {
				t.Fatalf("source error %s, native %v", c.Output.Error, e)
			}
		} else if e != nil || !equalJSON(out, c.Output.Value) {
			t.Fatalf("context mismatch err=%v got=%v expected=%v", e, out, c.Output.Value)
		}
		if !equalJSON(i.Torrent, c.Output.TorrentAfter) {
			t.Fatal("source description side effect differs")
		}
	})
}
func TestJinjaSourceOracle(t *testing.T) {
	cases(t, "jinja.jsonl.gz", func(t *testing.T, raw []byte) {
		var c struct {
			Text      string
			Variables json.RawMessage
			Result    string
			Error     string
		}
		if e := decode(raw, &c); e != nil {
			t.Fatal(e)
		}
		variables, e := DecodeContextJSON(c.Variables)
		if e != nil {
			t.Fatal(e)
		}
		out, e := RenderWithContext(context.Background(), c.Text, variables)
		if c.Error != "" {
			var source *exec.SourceError
			if !errors.As(e, &source) || source.Class != c.Error {
				t.Fatalf("source %s, native %q err=%v", c.Error, out, e)
			}
			return
		}
		if e != nil || !reflect.DeepEqual(out, c.Result) {
			t.Fatalf("source %q native %q err=%v", c.Result, out, e)
		}
	})
}
