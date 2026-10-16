package mergemessagesmtp

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	merge "light-media/internal/plugins/mergemessagenotify"
	"os"
	"testing"
)

func corpus(t *testing.T, path string, fn func(json.RawMessage)) {
	t.Helper()
	f, e := os.Open(originalFixture(path))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	g, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	s := bufio.NewScanner(g)
	s.Buffer(make([]byte, 4096), 16<<20)
	for s.Scan() {
		fn(append(json.RawMessage(nil), s.Bytes()...))
	}
	if e = s.Err(); e != nil {
		t.Fatal(e)
	}
}
func TestActualPythonMIMERawBytes(t *testing.T) {
	n, failed := 0, 0
	corpus(t, "testdata/mime-python-3.12.13.jsonl.gz", func(raw json.RawMessage) {
		var c struct {
			ID                 int
			Request            merge.SMTPRequest
			Raw, SHA256, Error string
		}
		if e := json.Unmarshal(raw, &c); e != nil {
			t.Fatal(e)
		}
		got, e := BuildMessage(c.Request)
		n++
		if c.Error != "" {
			var failure *Error
			if !errors.As(e, &failure) || failure.Class != c.Error {
				t.Errorf("case%d original %s got %v", c.ID, c.Error, e)
				failed++
			}
			return
		}
		if e != nil {
			t.Errorf("case%d: %v", c.ID, e)
			failed++
			return
		}
		want, e := base64.StdEncoding.DecodeString(c.Raw)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != c.SHA256 || string(got) != string(want) {
			t.Errorf("case%d raw MIME differs\ngot  %q\nwant %q", c.ID, got, want)
			failed++
		}
	})
	if n != 364 {
		t.Fatal("corpus count", n)
	}
	t.Logf("%d actual raw MIME source cases; mismatches=%d", n, failed)
}
