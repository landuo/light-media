package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSourceConstructorMatchesIndependentAuthOracle(t *testing.T) {
	var oracle struct {
		QB []struct {
			Case, Body string
			Status     int
			APIKey     bool `json:"api_key"`
			SID        bool `json:"set_sid"`
			Present    bool `json:"client_present"`
		} `json:"qbittorrent"`
		TR []struct {
			Case     string
			Status   int
			Response json.RawMessage
			Present  bool `json:"client_present"`
		} `json:"transmission"`
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-source-connection-20261105/constructor-auth-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.QB) != 14 || len(oracle.TR) != 9 {
		t.Fatal("independent actual source auth oracle", err)
	}
	for _, sample := range oracle.QB {
		t.Run("QB/"+sample.Case, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				path := "/api/v2/auth/login"
				if sample.APIKey {
					path = "/api/v2/app/version"
				}
				if r.URL.Path != path {
					t.Error("wrong constructor operation", r.URL.Path)
				}
				if sample.SID {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned", Path: "/"})
				}
				w.WriteHeader(sample.Status)
				io.WriteString(w, sample.Body)
			}))
			defer peer.Close()
			key := ""
			if sample.APIKey {
				key = "owned-key"
			}
			client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL, Username: "owned", Password: "owned"}, IYUUOptions{APIKey: key})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = client.SourceConstructorConnected(context.Background())
			if (err == nil) != sample.Present || calls.Load() != 1 {
				t.Fatal("source auth presence mismatch", err, sample.Present, calls.Load())
			}
		})
	}
	for _, sample := range oracle.TR {
		t.Run("TR/"+sample.Case, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var query map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&query) != nil || len(query) != 2 || string(query["method"]) != `"session-get"` || string(query["arguments"]) != `{}` {
					t.Error("constructor RPC differs", query)
				}
				wantHeader := "0"
				if n > 1 {
					wantHeader = "owned-session"
				}
				if r.Header.Get("X-Transmission-Session-Id") != wantHeader {
					t.Error("source constructor session header differs")
				}
				if strings.Contains(sample.Case, "409") && n == 1 {
					w.Header().Set("X-Transmission-Session-Id", "owned-session")
					w.WriteHeader(409)
					return
				}
				w.WriteHeader(sample.Status)
				if string(sample.Response) == "null" {
					io.WriteString(w, "owned invalid JSON")
				} else {
					w.Write(sample.Response)
				}
			}))
			defer peer.Close()
			client, err := NewIYUUClient(Config{ID: "owned", Kind: "transmission", BaseURL: peer.URL}, IYUUOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = client.SourceConstructorConnected(context.Background())
			expected := int32(1)
			if strings.Contains(sample.Case, "409") {
				expected = 2
			}
			if (err == nil) != sample.Present || calls.Load() != expected {
				t.Fatal("source TR ctor presence mismatch", err, sample.Present, calls.Load())
			}
		})
	}
}
