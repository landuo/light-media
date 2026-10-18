package mako

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestPinnedOriginalTemplates(t *testing.T) {
	checkOracle(t, "../../../../testdata/plugins/merge-message-notify-1.20.1/input/_merge_message_notify_port/testdata/installed-templates.jsonl.gz", 19)
}
func TestPinnedExpandedMakoOracle(t *testing.T) { checkOracle(t, "testdata/mako-1.4.1.jsonl.gz", 1236) }
func checkOracle(t *testing.T, path string, want int) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	d := json.NewDecoder(z)
	d.UseNumber()
	count := 0
	for {
		var c struct {
			Text      string
			Variables map[string]any
			Result    string
		}
		if err = d.Decode(&c); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		count++
		t.Run(stringIndex(count), func(t *testing.T) {
			got, err := Render(context.Background(), c.Text, c.Variables)
			if err != nil {
				t.Fatalf("%s: %v", c.Text, err)
			}
			if got != c.Result {
				t.Fatalf("%s\ngot:  %q\nwant: %q", c.Text, got, c.Result)
			}
		})
	}
	if count != want {
		t.Fatalf("source oracle count %d != %d", count, want)
	}
}
func stringIndex(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
func TestCancellationAndExplicitUnsupported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := Render(ctx, "% while True:\nx\n% endwhile\n", map[string]any{"v": true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	_, err = Render(context.Background(), "<% import os %>", map[string]any{"v": true})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("valid unimplemented import must stay visible: %v", err)
	}
}
