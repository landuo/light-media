package download

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestQBittorrentCompletedNativeFiniteWireAndExistingSession(t *testing.T) {
	var mu sync.Mutex
	var forms []string
	logins, guards := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned", Path: "/"})
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			if r.Method != "POST" {
				t.Error("source completed info must POST")
			}
			body, _ := io.ReadAll(r.Body)
			forms = append(forms, string(body))
			if cookie, _ := r.Cookie("SID"); cookie == nil || cookie.Value != "owned" {
				t.Error("existing session lost")
			}
			io.WriteString(w, `[{"progress":0,"tags":null,"unfiltered":18446744073709551615}]`)
		default:
			t.Error("unexpected request, preferences/mutations forbidden")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewQBittorrentCompletedClient(Config{ID: "owned", Kind: "qb", BaseURL: server.URL, Username: "owned", Password: "owned"}, "", func(context.Context) error { guards++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	empty, compound := "", "B|a|B"
	for _, hashes := range []*string{nil, &empty, &compound} {
		raw, err := client.Rows(context.Background(), hashes)
		if err != nil || string(raw) != `[{"progress":0,"tags":null,"unfiltered":18446744073709551615}]` {
			t.Fatal("raw rows prematurely filtered", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if logins != 1 || guards != 4 || !reflect.DeepEqual(forms, []string{"filter=completed", "filter=completed&hashes=", "filter=completed&hashes=B%7Ca%7CB"}) {
		t.Fatal("finite wire/session changed", logins, guards, forms)
	}
}

func TestQBittorrentCompletedNativeGuardAndCancellation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `[]`) }))
	defer server.Close()
	client, err := NewQBittorrentCompletedClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, "", func(context.Context) error { return errors.New("owned denied") })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Rows(context.Background(), nil); err == nil || calls != 0 {
		t.Fatal("guard bypass")
	}
	if _, err := client.Rows(nil, nil); err == nil || calls != 0 {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Rows(ctx, nil); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("cancelled query dispatched")
	}
	if _, err := NewQBittorrentCompletedClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, "", nil); err == nil {
		t.Fatal("missing guard accepted")
	}
}

func TestQBittorrentCompletedVendorNullAndReannounce(t *testing.T) {
	for _, row := range []struct{ input, want string }{
		{`null`, `[]`}, {`false`, `[]`}, {`0`, `[]`}, {`0.0`, `[]`},
		{`-0.0`, `[]`}, {`1e-400`, `[]`}, {`""`, `[]`}, {`{}`, `[]`},
		{`[null]`, `[null]`}, {`"x"`, `[null]`}, {`{"x":1}`, `[null]`},
		{`[{"progress":1,"reannounce":18446744073709551615}]`, `[{"progress":1,"reannounce_in":18446744073709551615}]`},
		{`[{"reannounce":null,"reannounce_in":22}]`, `[{"reannounce_in":null}]`},
	} {
		got, err := qbittorrentCompletedVendorRows(json.RawMessage(row.input))
		if err != nil || string(got) != row.want {
			t.Fatal("source vendor JSON shape/key adaptation changed", string(got), err)
		}
	}
}

func TestQBittorrentCompletedVendorNoniterableJSONFailure(t *testing.T) {
	for _, raw := range []string{`true`, `1`, `-1`, `1e400`, `not-json`} {
		if _, err := qbittorrentCompletedVendorRows(json.RawMessage(raw)); err == nil {
			t.Fatal("noniterable/invalid vendor root accepted")
		}
	}
}
