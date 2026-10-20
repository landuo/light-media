package apprise

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestOriginalJSONExpanded(t *testing.T) {
	testOriginalCases(t, "testdata/json-expanded.jsonl.gz", 1054)
}
func TestOriginalJSONOverflowAndEmojis(t *testing.T) {
	testOriginalCases(t, "testdata/json-overflow.jsonl.gz", 240)
}
func testOriginalCases(t *testing.T, path string, count int) {
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	scan := bufio.NewScanner(z)
	scan.Buffer(make([]byte, 4096), 1<<20)
	n := 0
	for scan.Scan() {
		n++
		var c struct {
			URL         string
			Title, Body any
			Attach      any
			Added       bool
			Result      any
			Error       *string
			Calls       []map[string]any
			Status      int
		}
		if e := json.Unmarshal(scan.Bytes(), &c); e != nil {
			t.Fatal(e)
		}
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			calls := []map[string]any{}
			client := New(func(ctx context.Context, r Request) (Response, error) {
				var auth any
				if r.Auth != nil {
					auth = r.Auth
				}
				calls = append(calls, map[string]any{"method": r.Method, "url": r.URL, "data": string(r.Data), "headers": r.Headers, "params": r.Params, "auth": auth, "verify": r.Verify, "timeout": r.Timeout})
				status := c.Status
				if status == 0 {
					status = 200
				}
				return Response{Status: status}, nil
			})
			added, e := client.Add(c.URL)
			if e != nil || added != c.Added {
				t.Fatalf("Add %q: %t,%v expected %t", c.URL, added, e, c.Added)
			}
			result, e := client.Notify(context.Background(), c.Title, c.Body, c.Attach)
			if c.Error != nil {
				var original *SourceError
				if !errors.As(e, &original) || original.Kind != *c.Error || len(calls) != 0 {
					t.Fatalf("source exception %s, got %v", *c.Error, result)
				}
				return
			}
			if e != nil || !reflect.DeepEqual(result, c.Result) {
				t.Fatalf("Notify %q %T/%T got %v,%v expected %v", c.URL, c.Title, c.Body, result, e, c.Result)
			}
			b, e := json.Marshal(calls)
			if e != nil {
				t.Fatal(e)
			}
			var normalized []map[string]any
			json.Unmarshal(b, &normalized)
			if !reflect.DeepEqual(normalized, c.Calls) {
				t.Fatalf("%q calls got=%s expected=%v", c.URL, b, c.Calls)
			}
		})
	}
	if e := scan.Err(); e != nil {
		t.Fatal(e)
	}
	if n != count {
		t.Fatal(n)
	}
}
