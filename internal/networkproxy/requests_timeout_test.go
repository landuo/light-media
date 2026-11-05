package networkproxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRequestsScalarTimeoutSourceHTTP(t *testing.T) {
	var oracle struct {
		Requests string
		Cases    []struct {
			Name           string
			TimeoutSeconds float64 `json:"timeout_seconds"`
			Content        *string
		}
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-network-20261031/timeout-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Requests != "2.34.2" || len(oracle.Cases) != 4 {
		t.Fatal("actual source timeout oracle missing", err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/header-idle" {
			time.Sleep(800 * time.Millisecond)
		}
		if strings.HasPrefix(r.URL.Path, "/redirect-") {
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/redirect-"))
			time.Sleep(300 * time.Millisecond)
			if n < 2 {
				w.Header().Set("Location", "/redirect-"+strconv.Itoa(n+1))
				w.WriteHeader(302)
				return
			}
		}
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(200)
		for i := 0; i < 5; i++ {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			if r.URL.Path == "/progress" && i < 4 {
				time.Sleep(300 * time.Millisecond)
			}
			if r.URL.Path == "/body-idle" && i == 0 {
				time.Sleep(800 * time.Millisecond)
			}
		}
	}))
	defer peer.Close()
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			base := NewRequestsTimeoutTransport(time.Duration(sample.TimeoutSeconds * float64(time.Second)))
			defer base.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL+"/"+sample.Name, nil)
			start := time.Now()
			response, err := (&http.Client{Transport: base}).Do(req)
			var body []byte
			if err == nil {
				defer response.Body.Close()
				body, err = io.ReadAll(response.Body)
			}
			if sample.Content == nil {
				if err == nil {
					t.Fatal("source inactivity timeout did not fail")
				}
			} else if err != nil || string(body) != *sample.Content {
				t.Fatal("progress/redirect incorrectly total-limited", string(body), err)
			}
			if sample.Name == "progress" && time.Since(start) < time.Second {
				t.Fatal("fixture did not cross scalar total duration")
			}
		})
	}
}
