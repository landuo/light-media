package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/downloadercommands"
)

// Same finite owned TR protocols as the accepted query/command fixtures, now
// over self-signed HTTPS. No proxy, forwarding, real daemon or environment edit.
func TestPluginPrivateTLSDirectTransmission(t *testing.T) {
	global := http.DefaultTransport.(*http.Transport)
	baseline := global.Clone()
	baseline.CloseIdleConnections()
	globalTLS := global.TLSClientConfig
	beforeEnv := os.Environ()
	var mu sync.Mutex
	methods := map[string][]string{}
	var revoked atomic.Bool
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if revoked.Load() {
			return errors.New("owned TLS current scope revoked")
		}
		return nil
	}
	peer := completedOwnedTLSPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.IsAbs() || r.URL.RawQuery != "" || (r.URL.Path != "/query/rpc" && r.URL.Path != "/commands/rpc") || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected owned TR route")
			w.WriteHeader(400)
			return
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "owned" || password != "synthetic-password" {
			t.Error("bound TR auth missing")
			w.WriteHeader(403)
			return
		}
		var request map[string]json.RawMessage
		dec := json.NewDecoder(r.Body)
		if dec.Decode(&request) != nil || dec.Decode(new(any)) != io.EOF {
			t.Error("invalid TR frame")
			w.WriteHeader(400)
			return
		}
		var method string
		if json.Unmarshal(request["method"], &method) != nil {
			t.Error("missing TR method")
			return
		}
		mu.Lock()
		methods[r.URL.Path] = append(methods[r.URL.Path], method)
		mu.Unlock()
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			if method != "session-get" {
				t.Error("selected operation before session negotiation")
			}
			w.Header().Set("X-Transmission-Session-Id", "owned-tls-session")
			w.WriteHeader(409)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != "owned-tls-session" {
			t.Error("wrong exact TR session")
			w.WriteHeader(403)
			return
		}
		args := map[string]any{}
		switch method {
		case "session-get":
			args = map[string]any{"version": "owned", "rpc-version": 17}
		case "torrent-get":
			if r.URL.Path != "/query/rpc" {
				t.Error("query crossed consumer")
			}
			var in struct {
				IDs    []int    `json:"ids"`
				Fields []string `json:"fields"`
			}
			if json.Unmarshal(request["arguments"], &in) != nil || !reflect.DeepEqual(in.IDs, []int{7}) || !reflect.DeepEqual(in.Fields, nativeTRFields) {
				t.Error("finite query selector/fields changed")
			}
			args = map[string]any{"torrents": []any{}}
		case "torrent-verify":
			if r.URL.Path != "/commands/rpc" {
				t.Error("command crossed consumer")
			}
			var in struct {
				IDs []int `json:"ids"`
			}
			if json.Unmarshal(request["arguments"], &in) != nil || !reflect.DeepEqual(in.IDs, []int{7}) {
				t.Error("recheck selector changed")
			}
		case "torrent-set":
			var in map[string]any
			d := json.NewDecoder(bytes.NewReader(request["arguments"]))
			d.UseNumber()
			if d.Decode(&in) != nil || !reflect.DeepEqual(in, map[string]any{"ids": []any{json.Number("7")}, "uploadLimit": json.Number("12")}) {
				t.Error("fixed S13 change arguments changed")
			}
		case "torrent-add":
			// A selected 409 must not repeat the add/session or cross into another route.
			w.Header().Set("X-Transmission-Session-Id", "owned-rotated-session")
			w.WriteHeader(409)
			return
		default:
			t.Error("unexpected fixed TR method", method)
			w.WriteHeader(400)
			return
		}
		reply := map[string]any{"result": "success", "arguments": args}
		if id, ok := request["tag"]; ok {
			reply["tag"] = id
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	config := Config{ID: "owned-private-tr-query", Kind: "tr", BaseURL: peer.URL + "/query/rpc", Username: "owned", Password: "synthetic-password"}
	query, err := NewTransmissionQueryClient(config, guard)
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	if raw, err := query.Torrents(context.Background(), json.RawMessage(`[7]`)); err != nil || string(raw) != "[]" {
		t.Fatal("private TR query rejected owned TLS", err)
	}
	config.ID = "owned-private-tr-command"
	config.BaseURL = peer.URL + "/commands/rpc"
	_, tr, closeClient, err := NewSharedDownloaderCommands(config, "", guard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	if _, err = tr.Recheck(context.Background(), json.Number("7")); err != nil {
		t.Fatal("private TR recheck rejected owned TLS", err)
	}
	limit := int64(12)
	if _, err = tr.(downloadercommands.TRChangePort).Change(context.Background(), downloadercommands.TRChangeCall{IDs: json.Number("7"), UploadLimit: &limit}); err != nil {
		t.Fatal("private fixed TR change failed", err)
	}
	_, err = tr.Add(context.Background(), downloadercommands.TRAddCall{Content: []byte("owned-tls-material")})
	if err == nil || MutationNotDispatched(err) {
		t.Fatal("selected 409 lost dispatched/unknown failure", err)
	}
	mu.Lock()
	gotQuery := append([]string(nil), methods["/query/rpc"]...)
	gotCommand := append([]string(nil), methods["/commands/rpc"]...)
	mu.Unlock()
	if !reflect.DeepEqual(gotQuery, []string{"session-get", "session-get", "torrent-get"}) || !reflect.DeepEqual(gotCommand, []string{"session-get", "session-get", "torrent-verify", "torrent-set", "torrent-add"}) {
		t.Fatal("handshake/selected dispatch count changed", gotQuery, gotCommand)
	}
	revoked.Store(true)
	if _, err = query.Torrents(context.Background(), json.RawMessage(`[7]`)); err == nil {
		t.Fatal("query ignored current guard")
	}
	if _, err = tr.Recheck(context.Background(), json.Number("7")); !MutationNotDispatched(err) || MutationFailureClass(err) != "owner_denied" {
		t.Fatal("command ignored current guard", err)
	}
	mu.Lock()
	unchanged := len(methods["/query/rpc"]) == 3 && len(methods["/commands/rpc"]) == 5
	mu.Unlock()
	if !unchanged || http.DefaultTransport != global || global.TLSClientConfig != globalTLS || (globalTLS != nil && globalTLS.InsecureSkipVerify) || !reflect.DeepEqual(beforeEnv, os.Environ()) {
		t.Fatal("scope/global/environment boundary changed")
	}
}
