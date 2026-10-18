package mergemessagenotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestInstalledChannelsNativeTemplate(t *testing.T) {
	cases(t, "testdata/installed-channels.jsonl.gz", func(i int, c map[string]any) {
		if t.Failed() {
			return
		}
		key := c["key"].(string)
		trace := []any{}
		replies := append([]any(nil), c["replies"].([]any)...)
		host := Host{Proxy: map[string]any{"https": "http://proxy.invalid:9000"}, Timezone: "Asia/Shanghai", Now: func() time.Time { return time.Unix(1700000000, 125000000) }, UUID: func() string { return "01234567-89ab-cdef-0123-456789abcdef" }, Render: RenderNative}
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
