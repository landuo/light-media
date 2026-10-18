package networkproxy

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOriginalLinuxRequestsEnvironmentProxy(t *testing.T) {
	f, err := os.Open("testdata/requests-linux-2.34.2.jsonl.gz")
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
	count := 0
	for {
		var c struct {
			Environment []string
			URL         string
			Proxy       *string
		}
		if err = d.Decode(&c); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		count++
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			target, err := url.Parse(c.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := RequestsEnvironmentProxy(c.Environment, target)
			if err != nil {
				t.Fatal(err)
			}
			if c.Proxy == nil {
				if proxy != nil {
					t.Fatalf("got synthetic proxy %s; expected direct", proxy)
				}
			} else if proxy == nil || proxy.String() != *c.Proxy {
				t.Fatalf("url=%s env=%v got=%v want=%s", c.URL, c.Environment, proxy, *c.Proxy)
			}
		})
	}
	if count != 1080 {
		t.Fatal(count)
	}
}

func TestRequestsProxyActuallyRoutesLoopbackAndCredentials(t *testing.T) {
	calls := make(chan string, 4)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "Basic c3ludGhldGljOnBhc3M=" {
			t.Error("synthetic proxy authentication missing")
		}
		calls <- r.URL.String()
		w.WriteHeader(204)
	}))
	defer proxy.Close()
	p, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	p.User = url.UserPassword("synthetic", "pass")
	environment := []string{"ALL_PROXY=" + p.String()}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.Proxy = func(r *http.Request) (*url.URL, error) { return RequestsEnvironmentProxy(environment, r.URL) }
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, target := range []string{"http://owned.invalid/test", "http://127.0.0.1:1/test"} {
		r, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 204 {
			t.Fatal(r.StatusCode)
		}
		select {
		case seen := <-calls:
			if seen != target {
				t.Fatal(seen)
			}
		case <-time.After(time.Second):
			t.Fatal("actual owned proxy not contacted")
		}
	}
}

func TestRequestsProxyDoesNotMutateEnvironmentOrExposeInvalidProxy(t *testing.T) {
	target, _ := url.Parse("https://owned.invalid")
	values := []string{"ALL_PROXY=http://valid.invalid", "all_proxy=://synthetic-secret"}
	before := strings.Join(values, "\n")
	_, err := RequestsEnvironmentProxy(values, target)
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("invalid proxy did not use sanitized error")
	}
	if strings.Join(values, "\n") != before {
		t.Fatal("caller environment mutated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if request.Context().Err() == nil {
		t.Fatal("canceled context lost")
	}
	if _, err = RequestsEnvironmentProxy(nil, nil); err == nil {
		t.Fatal("missing target accepted")
	}
}
