package download

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBrushFlowNativeSelectedQBAndTRNeverRetry(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, op := range []string{"add", "pause", "delete", "limits", "delete_tags", "reannounce"} {
			if kind == "qbittorrent" && op == "limits" || kind == "transmission" && (op == "delete_tags" || op == "reannounce") {
				continue
			}
			t.Run(kind+"/"+op, func(t *testing.T) {
				var writes, logins atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if kind == "qbittorrent" {
						switch r.URL.Path {
						case "/api/v2/app/webapiVersion":
							io.WriteString(w, "2.11.3")
						case "/api/v2/auth/login":
							logins.Add(1)
							io.WriteString(w, "Ok.")
						default:
							writes.Add(1)
							w.WriteHeader(401)
						}
						return
					}
					var in map[string]any
					json.NewDecoder(r.Body).Decode(&in)
					if in["method"] == "session_get" {
						json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": in["id"], "result": map[string]any{"version": "4.1.1"}})
						return
					}
					writes.Add(1)
					w.Header().Set("X-Transmission-Session-Id", "owned-new")
					w.WriteHeader(409)
				}))
				defer s.Close()
				c := phaseClient(t, Config{ID: "owned", Kind: kind, BaseURL: s.URL}, IYUUOptions{})
				var e error
				out := ""
				if op == "add" {
					v, x := c.BrushFlowAddDownloading(context.Background(), IYUUAddSpec{Torrent: []byte("owned")}, nil, nil, nil)
					e = x
					out = v.Outcome
				} else {
					n := int64(12)
					v, x := c.BrushFlowControl(context.Background(), op, []string{nativeHash}, []string{"刷流-owned"}, &n, nil)
					e = x
					out = v.Outcome
				}
				if e == nil || MutationNotDispatched(e) || out != "unknown" || writes.Load() != 1 || logins.Load() != 0 {
					t.Fatal("selected request repeated or misclassified", op, out, e, writes.Load(), logins.Load())
				}
			})
		}
	}
}
func TestBrushFlowNativeLocalAndOwnerRejectionProof(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("denied operation reached wire")
	}))
	defer s.Close()
	c := phaseClient(t, Config{ID: "owned", Kind: "qbittorrent", BaseURL: s.URL}, IYUUOptions{Guard: func(context.Context) error { return errors.New("owned denied") }})
	for _, op := range []string{"pause", "delete", "reannounce", "delete_tags"} {
		r, e := c.BrushFlowControl(context.Background(), op, []string{nativeHash}, []string{"owned"}, nil, nil)
		if !MutationNotDispatched(e) || MutationFailureClass(e) != "owner_denied" || r.Outcome != "rejected" {
			t.Fatal(op, r, e)
		}
	}
	r, e := c.BrushFlowAddDownloading(context.Background(), IYUUAddSpec{Magnet: "https://bad"}, nil, nil, nil)
	if !MutationNotDispatched(e) || MutationFailureClass(e) != "native" || r.Outcome != "rejected" {
		t.Fatal(r, e)
	}
	r, e = c.BrushFlowAddDownloading(context.Background(), IYUUAddSpec{Torrent: []byte("owned")}, nil, nil, nil)
	if !MutationNotDispatched(e) || MutationFailureClass(e) != "owner_denied" {
		t.Fatal(r, e)
	}
	if requests.Load() != 0 {
		t.Fatal(requests.Load())
	}
}
