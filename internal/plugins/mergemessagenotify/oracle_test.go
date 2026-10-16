package mergemessagenotify

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func cases(t *testing.T, path string, fn func(int, map[string]any)) {
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
	s.Buffer(make([]byte, 4096), 2<<20)
	n := 0
	for s.Scan() {
		v, e := Decode(s.Bytes())
		if e != nil {
			t.Fatal(e)
		}
		fn(n, v.(map[string]any))
		n++
	}
	if e = s.Err(); e != nil {
		t.Fatal(e)
	}
	t.Logf("%d actual installed AST cases", n)
}
func canonical(v any) any {
	b, e := json.Marshal(normalize(v))
	if e != nil {
		panic(e)
	}
	r, e := Decode(b)
	if e != nil {
		panic(e)
	}
	return r
}
func equal(t *testing.T, label string, got, want any) {
	t.Helper()
	a, b := canonical(got), canonical(want)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s\ngot  %s\nwant %s", label, jsonText(a), jsonText(b))
	}
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestInstalledCore(t *testing.T) {
	cases(t, "testdata/installed-core.jsonl.gz", func(i int, c map[string]any) {
		p := New(Host{})
		cfg, _ := c["config"].(map[string]any)
		if e := p.Init(context.Background(), cfg, false); e != nil {
			t.Fatal(e)
		}
		event, _ := c["event"].(map[string]any)
		results := c["results"].([]any)
		calls := []any{}
		p.dispatch(context.Background(), event, func(_ context.Context, key string, m Message) (any, error) {
			var typ any
			if m.Type != nil {
				typ = map[string]any{"$enum": m.Type.Name, "value": m.Type.Value}
			}
			calls = append(calls, map[string]any{"key": key, "title": m.Title, "text": m.Text, "type": typ, "ext_info": m.Ext})
			v := results[(len(calls)-1)%len(results)]
			if v == "error" {
				return nil, errors.New("ValueError")
			}
			return v, nil
		})
		equal(t, fmt.Sprint(i, " calls"), calls, c["calls"])
		equal(t, fmt.Sprint(i, " state"), p.State(), c["state"])
		equal(t, fmt.Sprint(i, " config"), p.Config(), c["config_after"])
	})
}
func TestInstalledChannels(t *testing.T) {
	cases(t, "testdata/installed-channels.jsonl.gz", func(i int, c map[string]any) {
		if t.Failed() {
			return
		}
		key := c["key"].(string)
		trace := []any{}
		replies := append([]any(nil), c["replies"].([]any)...)
		host := Host{Proxy: map[string]any{"https": "http://proxy.invalid:9000"}, Timezone: "Asia/Shanghai", Now: func() time.Time { return time.Unix(1700000000, 125000000) }, UUID: func() string { return "01234567-89ab-cdef-0123-456789abcdef" }, Render: RenderBasic}
		host.HTTP = func(_ context.Context, r Request) (Response, error) {
			v := map[string]any{"kind": "http", "method": r.Method, "url": r.URL}
			for k, x := range r.Kwargs {
				v[k] = x
			}
			trace = append(trace, v)
			reply := replies[0].(map[string]any)
			replies = replies[1:]
			if truth(reply["error"]) {
				return Response{}, errors.New("RuntimeError")
			}
			status := 200
			if n, ok := reply["status"].(json.Number); ok {
				nn, _ := n.Int64()
				status = int(nn)
			}
			text, _ := reply["text"].(string)
			return Response{Status: status, JSON: reply["json"], JSONError: truth(reply["json_error"]), Text: text}, nil
		}
		host.Apprise = func(_ context.Context, r AppriseRequest) (any, error) {
			trace = append(trace, map[string]any{"kind": "apprise", "urls": r.URLs, "title": r.Title, "body": r.Body, "attach": r.Attach})
			return true, nil
		}
		host.SMTP = func(_ context.Context, r SMTPRequest) error {
			trace = append(trace, smtpSemanticTrace(r)...)
			return nil
		}
		p := New(host)
		cfg := map[string]any{}
		for k, v := range c["config"].(map[string]any) {
			cfg[key+"."+k] = v
		}
		if e := p.Init(context.Background(), cfg, false); e != nil {
			t.Fatal(e)
		}
		msg := c["message"].(map[string]any)
		ext, _ := msg["ext_info"].(map[string]any)
		r, e := p.Send(context.Background(), key, Message{Title: msg["title"], Text: msg["text"], Type: NotifyType(c["type"].(string)), Ext: ext})
		out := map[string]any{"return": r}
		if e != nil {
			out = map[string]any{"error": e.Error()}
		}
		equal(t, fmt.Sprint(i, " ", key, " result"), out, c["outcome"])
		equal(t, fmt.Sprint(i, " ", key, " trace"), trace, sourceSMTPTrace(t, c["trace"].([]any)))
	})
}
func TestInstalledForm(t *testing.T) {
	v, e := Decode(formsJSON)
	if e != nil {
		t.Fatal(e)
	}
	want := v.(map[string]any)["plugin"].(map[string]any)
	updates := []any{}
	p := New(Host{SaveConfig: func(_ context.Context, c map[string]any) (bool, error) {
		updates = append(updates, c)
		return true, nil
	}})
	p.Init(context.Background(), map[string]any{}, false)
	elements, data, e := p.Form(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	equal(t, "elements", elements, want["elements"])
	equal(t, "data", data, want["data"])
	equal(t, "updates", updates, want["updates"])
}
