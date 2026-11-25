package download

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Replay one fixed daemon response through the regular List/request path,
// excluding a live daemon, dialect negotiation and fixture generation from timing.
type listReplay string

func (r listReplay) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		req.Body.Close()
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(r))), Header: make(http.Header)}, nil
}

func replayListClient(t testing.TB, dialect, response string) *Client {
	t.Helper()
	kind := "tr"
	if dialect == "qb" {
		kind = "qb"
	}
	c, err := New(Config{ID: "replay", Kind: kind, BaseURL: "http://replay.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	c.trDialect = dialect
	c.http.Transport = listReplay(response)
	return c
}

func listReplayResponse(dialect string, count int) string {
	var b strings.Builder
	var row string
	switch dialect {
	case "qb":
		row = `{"hash":"%040X","name":"item-%d","state":"downloading","save_path":"/downloads","progress":0.5,"total_size":9007199254740993,"completed":41,"ratio":1.25,"eta":20,"added_on":99,"dlspeed":111,"upspeed":222,"tags":"first, second","unknown":{"future":true}}`
	case "jsonrpc":
		b.WriteString(`{"jsonrpc":"2.0","id":1,"result":{"torrents":`)
		row = `{"hash_string":"%040X","name":"item-%d","status":4,"download_dir":"/downloads","percent_done":0.5,"total_size":9007199254740993,"left_until_done":9007199254740952,"upload_ratio":1.25,"eta":20,"added_date":99,"rate_download":111,"rate_upload":222,"labels":["first","second"],"unknown":{"future":true}}`
	case "legacy":
		b.WriteString(`{"tag":1,"result":"success","arguments":{"torrents":`)
		row = `{"hashString":"%040X","name":"item-%d","status":4,"downloadDir":"/downloads","percentDone":0.5,"totalSize":9007199254740993,"leftUntilDone":9007199254740952,"uploadRatio":1.25,"eta":20,"addedDate":99,"rateDownload":111,"rateUpload":222,"labels":["first","second"],"unknown":{"future":true}}`
	}
	b.WriteByte('[')
	for i := count; i > 0; i-- {
		if i != count {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, row, i+10, i)
	}
	b.WriteByte(']')
	if dialect != "qb" {
		b.WriteString(`}}`)
	}
	b.WriteString("\r\n")
	return b.String()
}

func TestListDecodeProtocolReplay(t *testing.T) {
	for _, dialect := range []string{"qb", "jsonrpc", "legacy"} {
		t.Run(dialect, func(t *testing.T) {
			const count = 3
			response := listReplayResponse(dialect, count)
			c := replayListClient(t, dialect, response)
			tasks, err := c.List(t.Context())
			if err != nil || len(tasks) != count {
				t.Fatalf("got %d tasks, err=%v", len(tasks), err)
			}
			for idx, task := range tasks {
				i := count - idx
				hash := fmt.Sprintf("%040x", i+10)
				want := Task{ID: "replay:" + hash, Hash: hash, Name: fmt.Sprintf("item-%d", i), InstanceID: "replay", Progress: .5, Status: "downloading", DownloadDir: "/downloads", TotalSize: 9007199254740993, Completed: 41, Ratio: 1.25, ETA: 20, AddedAt: 99, DownloadSpeed: 111, UploadSpeed: 222, Tags: []string{"first", "second"}}
				if !reflect.DeepEqual(task, want) {
					t.Fatalf("task[%d] = %#v, want %#v", idx, task, want)
				}
			}
			for _, tail := range []string{"null", "{", "garbage"} {
				c := replayListClient(t, dialect, response+tail)
				if tasks, err := c.List(t.Context()); tasks != nil || !reflect.DeepEqual(err, failure("response", "invalid_json_tail")) {
					t.Errorf("tail %q: tasks=%v, err=%v", tail, tasks, err)
				}
			}
		})
	}
}

func TestListDecodeResponseBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, body, op, code string }{
		{"empty-qb", " \t", "", ""},
		{"null-qb", "null", "list", "invalid_task_list"},
		{"truncated", `[{"hash":`, "response", "invalid_json"},
		{"wrong-field-type", `[{"hash":[]}]`, "response", "invalid_json"},
		{"wrong-field-type-before-tail", `[{"hash":[]}] null`, "response", "invalid_json"},
		{"row-limit", "[" + strings.Repeat("{},", 10000) + "{}]", "list", "invalid_task_list"},
		{"at-byte-limit", "[]" + strings.Repeat(" ", maxResponse-2), "", ""},
		{"over-byte-limit", "[]" + strings.Repeat(" ", maxResponse-1), "response", "body_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := replayListClient(t, "qb", tc.body)
			tasks, err := c.List(t.Context())
			if tc.code == "" {
				if err != nil || tasks == nil || len(tasks) != 0 {
					t.Fatalf("tasks=%v err=%v", tasks, err)
				}
			} else if tasks != nil || !reflect.DeepEqual(err, failure(tc.op, tc.code)) {
				t.Fatalf("tasks=%v err=%v; want %s/%s", tasks, err, tc.op, tc.code)
			}
		})
	}
}

func BenchmarkListDecodeProtocolReplay(b *testing.B) {
	const count = 2048
	for _, dialect := range []string{"qb", "jsonrpc", "legacy"} {
		b.Run(dialect, func(b *testing.B) {
			response := listReplayResponse(dialect, count)
			c := replayListClient(b, dialect, response)
			b.SetBytes(int64(len(response)))
			b.ReportAllocs()
			for b.Loop() {
				c.requestID = 0 // recorded envelope echoes request 1
				tasks, err := c.List(b.Context())
				if err != nil || len(tasks) != count {
					b.Fatalf("got %d tasks, err=%v", len(tasks), err)
				}
			}
		})
	}
}
