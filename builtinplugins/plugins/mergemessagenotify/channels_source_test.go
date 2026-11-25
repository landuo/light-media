package mergemessagenotify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
)

func TestSourceChannels1203(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-channels-1.20.3.json")
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		URL     string
		Headers map[string]string
		JSON    map[string]any
	}
	var fixture struct {
		Cases []struct {
			Channel  string
			Config   map[string]any
			Result   bool
			Requests []request
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing original channel observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Channel, func(t *testing.T) {
			observed := make(chan request, len(row.Requests)+1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{}
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "invalid synthetic OneBot request", http.StatusBadRequest)
					return
				}
				headers := map[string]string{}
				if value := r.Header.Get("Authorization"); value != "" {
					headers["Authorization"] = value
				}
				observed <- request{URL: r.URL.RequestURI(), Headers: headers, JSON: body}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"retcode":0}`))
			}))
			defer server.Close()
			p := New(Host{HTTP: RequestsHTTP{}.Send, Render: RenderNative})
			config := map[string]any{}
			for key, value := range row.Config {
				if key == "server_url" {
					value = server.URL + "/"
				}
				config[row.Channel+"."+key] = value
			}
			if err := p.Init(t.Context(), config, false); err != nil {
				t.Fatal(err)
			}
			got, err := p.Send(t.Context(), row.Channel, Message{Title: "owned title", Text: "owned text"})
			if err != nil || got != row.Result {
				t.Fatalf("send result %v, %v, want %v", got, err, row.Result)
			}
			if len(observed) != len(row.Requests) {
				t.Fatalf("received %d requests, want %d", len(observed), len(row.Requests))
			}
			for _, want := range row.Requests {
				original, err := url.Parse(want.URL)
				if err != nil {
					t.Fatal(err)
				}
				want.URL = original.RequestURI()
				if got := <-observed; !reflect.DeepEqual(got, want) {
					t.Fatalf("synthetic request = %#v, want %#v", got, want)
				}
			}
		})
	}
}
