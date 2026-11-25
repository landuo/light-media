package mpserverstatus

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIndependentActual312HeaderCount(t *testing.T) {
	raw, e := os.ReadFile("reference/source-header-count-review.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Count int  `json:"header_lines_excluding_blank"`
			None  bool `json:"response_is_None"`
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	for _, c := range source.Cases {
		t.Run(fmt.Sprint(c.Count), func(t *testing.T) {
			l, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			go func() {
				socket, e := l.Accept()
				if e != nil {
					return
				}
				defer socket.Close()
				var b [4096]byte
				_, _ = socket.Read(b[:])
				var out strings.Builder
				out.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n")
				for i := 1; i < c.Count; i++ {
					fmt.Fprintf(&out, "X-Owned-%d: a\r\n", i)
				}
				out.WriteString("\r\nok")
				_, _ = io.WriteString(socket, out.String())
			}()
			environment := []string{}
			n := &NativeNetwork{environment: &environment}
			r, e := n.Status(context.Background(), "http://"+l.Addr().String()+"/status", time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if (r == nil) != c.None {
				t.Fatalf("source None=%v, Go response=%v", c.None, r != nil)
			}
		})
	}
}

func TestIndependentActual312HTTPPhysicalFrames(t *testing.T) {
	raw, e := os.ReadFile("reference/source-header-extended-review.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Name                     string
			Wire                     []string `json:"wire_b64"`
			None                     bool     `json:"response_is_None"`
			Status, Protocol, Length int
			Text                     string
		} `json:"origin_cases"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	for _, c := range source.Cases {
		for _, secure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/TLS%v", c.Name, secure), func(t *testing.T) {
				listener, e := net.Listen("tcp", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				if secure {
					fixture := httptest.NewTLSServer(http.NotFoundHandler())
					certificate := fixture.TLS.Certificates[0]
					fixture.Close()
					listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}})
				}
				defer listener.Close()
				seen := make(chan []string, 1)
				go func() {
					socket, e := listener.Accept()
					if e != nil {
						seen <- nil
						return
					}
					defer socket.Close()
					reader := bufio.NewReader(socket)
					paths := []string{}
					for _, wire := range c.Wire {
						request, e := http.ReadRequest(reader)
						if e != nil {
							break
						}
						paths = append(paths, request.URL.Path)
						request.Body.Close()
						b, e := base64.StdEncoding.DecodeString(wire)
						if e != nil {
							break
						}
						if _, e = socket.Write(b); e != nil {
							break
						}
					}
					seen <- paths
				}()
				environment := []string{}
				n := &NativeNetwork{environment: &environment}
				scheme := "http"
				if secure {
					scheme = "https"
				}
				result, e := n.Status(context.Background(), scheme+"://"+listener.Addr().String()+"/phase1", time.Second)
				if e != nil {
					t.Fatal(e)
				}
				if (result == nil) != c.None {
					t.Fatalf("None=%v, source=%v", result == nil, c.None)
				}
				if result != nil && (result.Status != c.Status || result.Protocol != c.Protocol || nativeTestText(t, result) != c.Text) {
					t.Fatalf("response gotstatus%d/proto%d/text%q source%d/%d/%q", result.Status, result.Protocol, nativeTestText(t, result), c.Status, c.Protocol, c.Text)
				}
				paths := <-seen
				if len(paths) != len(c.Wire) {
					t.Fatal("redirect did not use same owned connection", paths, len(c.Wire))
				}
			})
		}
	}
}

// Source proxy expectations below executed the actual urllib3-inherited
// stdlib tunnel method with a fake socket. Go executes its same finite parser
// over owned pipe bytes; this does not claim a Python real CONNECT/TLS probe.
func TestIndependentActual312ProxyPhysicalFrames(t *testing.T) {
	raw, e := os.ReadFile("reference/source-header-extended-review.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Name, Wire string
			Error      json.RawMessage
		} `json:"proxy_cases"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	// The wire input is named wire_b64 in the independent observation.
	var full struct {
		Cases []map[string]json.RawMessage `json:"proxy_cases"`
	}
	_ = json.Unmarshal(raw, &full)
	for i, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var encoded string
			_ = json.Unmarshal(full.Cases[i]["wire_b64"], &encoded)
			wire, e := base64.StdEncoding.DecodeString(encoded)
			if e != nil {
				t.Fatal(e)
			}
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			go func() { _, _ = peer.Write(wire); peer.Close() }()
			_, e = sourceProxyResponse(client, &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: "movie-pilot.org:443"}, Host: "movie-pilot.org:443"})
			wantError := string(c.Error) != "null" && len(c.Error) > 0
			if (e != nil) != wantError {
				t.Fatalf("proxy source error=%v, got %v", wantError, e)
			}
		})
	}
}
