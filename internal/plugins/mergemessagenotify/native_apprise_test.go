package mergemessagenotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"light-media/internal/plugins/mergemessagenotify/apprise"
)

func TestActualNativeAppriseChannelHTTP(t *testing.T) {
	var mu sync.Mutex
	payloads := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("User-Agent") != "Apprise" {
			t.Error("source request headers or method lost")
		}
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		var payload map[string]any
		if e = json.Unmarshal(raw, &payload); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer server.Close()
	port := &apprise.HTTPTransport{Environment: func() []string { return nil }}
	p := New(Host{Apprise: NativeApprise(port.Send)})
	cfg := map[string]any{"custom.apprise.service_urls": "\n" + strings.Replace(server.URL, "http:", "json:", 1) + "/first\nunknown://ignored.invalid\n" + strings.Replace(server.URL, "http:", "json:", 1) + "/second\n"}
	if e := p.Init(context.Background(), cfg, false); e != nil {
		t.Fatal(e)
	}
	value, e := p.Send(context.Background(), "custom.apprise", Message{Title: " title ", Text: "正文 ", Ext: map[string]any{}})
	if e != nil || value != true {
		t.Fatal(value, e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 2 {
		t.Fatal(len(payloads))
	}
	for _, p := range payloads {
		if p["title"] != "title" || p["message"] != "正文" || p["version"] != "1.0" {
			t.Fatal(p)
		}
	}
}

func TestNativeAppriseDependencyAndNoImplicitNetwork(t *testing.T) {
	send := NativeApprise(nil)
	for _, urls := range [][]string{{}, {"not-a-url"}, {"unknown://owned.invalid"}} {
		value, e := send(context.Background(), AppriseRequest{URLs: urls, Title: "title", Body: "body"})
		if e != nil || value != false {
			t.Fatal(value, e)
		}
	}
	for _, r := range []AppriseRequest{{URLs: []string{"json://owned.invalid"}, Title: "title", Body: "body"}, {URLs: []string{"tgram://synthetic"}, Title: "title", Body: "body"}, {URLs: []string{"json://owned.invalid"}, Title: "title", Body: "body", Attach: "https://owned.invalid/image.png"}} {
		_, e := send(context.Background(), r)
		if !errors.Is(e, ErrDependency) {
			t.Fatal(e)
		}
	}
}
