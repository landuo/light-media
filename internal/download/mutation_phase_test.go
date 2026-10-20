package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAddRejectionProofOnlyAcceptsAcknowledgedFixedProtocolRejection(t *testing.T) {
	for _, body := range []string{"Fails.", "proxy-generated response", ""} {
		t.Run(fmt.Sprintf("body_%q", body), func(t *testing.T) {
			var adds atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					reply(w, []any{})
				case "/api/v2/torrents/add":
					adds.Add(1)
					io.WriteString(w, body)
				default:
					t.Error("ungranted endpoint", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			client, e := New(Config{ID: "owned", Kind: "qb", BaseURL: server.URL})
			if e != nil {
				t.Fatal(e)
			}
			torrent, _ := testTorrent()
			_, e = client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads"})
			if e == nil || adds.Load() != 1 || MutationNotStarted(e) || MutationRejected(e) != (body == "Fails.") {
				t.Fatal("unknown daemon response forged fixed rejection proof", e, adds.Load())
			}
			if MutationRejected(fmt.Errorf("wrapped %w", e)) || MutationRejected(errors.Join(e, errors.New("other effect"))) || MutationRejected(&Error{Operation: "add", Code: "torrent_rejected"}) {
				t.Fatal("public/aggregate error forged rejection")
			}
		})
	}
	client, e := New(Config{ID: "owned", Kind: "qb", BaseURL: "http://127.0.0.1:1"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.Add(context.Background(), AddRequest{SavePath: "relative"})
	if !MutationNotStarted(e) || MutationRejected(e) {
		t.Fatal("local pre-Add validation did not retain exact no-write proof", e)
	}
}

func TestMutationPhaseLocalProofCannotComeFromJSONOrErrorCode(t *testing.T) {
	c := mustClient(t, "qb", "http://127.0.0.1:1")
	for _, err := range []error{c.Control(context.Background(), "bad-hash", "pause"), c.Control(context.Background(), nativeHash, "wildcard"), c.SetTorrentTags(context.Background(), nativeHash, []string{string([]byte{0xff})})} {
		if err == nil || !MutationNotStarted(err) {
			t.Fatal("local rejection lacks actual no-write proof", err)
		}
		var remote Error
		b, _ := json.Marshal(err)
		_ = json.Unmarshal(b, &remote)
		if MutationNotStarted(&remote) || MutationNotStarted(fmt.Errorf("wrapped: %w", err)) || MutationNotStarted(errors.Join(err, errors.New("unknown other mutation"))) {
			t.Fatal("foreign/aggregate error certified a mutation")
		}
	}
	for _, code := range []string{"DOWNLOADER_MUTATION_NOT_STARTED", "not_started", "invalid_hash"} {
		if MutationNotStarted(&Error{Operation: "control", Code: code}) {
			t.Fatal("string code forged boundary")
		}
	}
	var zero *mutationNotStarted
	if MutationNotStarted(zero) || MutationNotStarted(&mutationNotStarted{cause: ErrNotFound}) || MutationNotStarted(nil) {
		t.Fatal("zero proof was trusted")
	}
	err := c.Control(context.Background(), "other:"+nativeHash, "pause")
	var concrete *Error
	if !MutationNotStarted(err) || !errors.As(err, &concrete) || concrete.Code != "wrong_instance" {
		t.Fatal("underlying safe error contract lost", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = c.SetTorrentTags(ctx, nativeHash, []string{"owned"}); !MutationNotStarted(err) || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lock wait was not known local rejection", err)
	}
}

func TestMutationPhaseQBReadOnlyPreflightAndIssuedMutationAreDistinct(t *testing.T) {
	for _, action := range []string{"control", "tags"} {
		for _, mode := range []string{"login_rejected", "lookup_missing", "lookup_ambiguous", "lookup_transport", "post_500", "post_disconnect", "post_bad_reply", "post_auth_then_login_failure", "ok"} {
			if action == "tags" && strings.HasPrefix(mode, "lookup_") {
				continue
			}
			t.Run(action+"/"+mode, func(t *testing.T) {
				var writes, logins atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v2/auth/login":
						n := logins.Add(1)
						if mode == "login_rejected" || (mode == "post_auth_then_login_failure" && n > 1) {
							http.Error(w, "synthetic-login-secret", 401)
							return
						}
						http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-session", Path: "/"})
						io.WriteString(w, "Ok.")
					case "/api/v2/torrents/info":
						if mode == "lookup_transport" {
							conn, _, _ := w.(http.Hijacker).Hijack()
							_ = conn.Close()
							return
						}
						rows := []any{qbRow(nativeHash)}
						if mode == "lookup_missing" {
							rows = []any{}
						}
						if mode == "lookup_ambiguous" {
							rows = append(rows, qbRow(nativeHash))
						}
						reply(w, rows)
					case "/api/v2/torrents/delete", "/api/v2/torrents/addTags":
						writes.Add(1)
						_ = r.ParseForm()
						if r.Form.Get("hashes") != nativeHash {
							t.Error("unsafe selector")
						}
						if action == "control" && r.Form.Get("deleteFiles") != "false" {
							t.Error("data deletion requested")
						}
						switch mode {
						case "post_500":
							http.Error(w, "synthetic-mutation-secret", 500)
						case "post_disconnect":
							conn, _, _ := w.(http.Hijacker).Hijack()
							_ = conn.Close()
						case "post_bad_reply":
							io.WriteString(w, "not-acknowledged")
						case "post_auth_then_login_failure":
							http.Error(w, "re-authentication", 401)
						default:
							io.WriteString(w, "Ok.")
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer s.Close()
				c := mustClient(t, "qb", s.URL)
				var err error
				if action == "control" {
					err = c.Control(context.Background(), nativeHash, "remove_keep_data")
				} else {
					err = c.SetTorrentTags(context.Background(), nativeHash, []string{"owned"})
				}
				pre := mode == "login_rejected" || strings.HasPrefix(mode, "lookup_")
				if mode == "ok" {
					if err != nil || writes.Load() != 1 {
						t.Fatal("normal mutation changed", err, writes.Load())
					}
					return
				}
				if err == nil || MutationNotStarted(err) != pre {
					t.Fatal("wrong actual write boundary", MutationNotStarted(err), err)
				}
				want := int32(1)
				if pre {
					want = 0
				}
				if writes.Load() != want {
					t.Fatal("claimed phase differs from observed wire", writes.Load(), want)
				}
				if strings.Contains(err.Error(), "synthetic-") {
					t.Fatal("remote body escaped safe error")
				}
				if mode == "lookup_missing" && !errors.Is(err, ErrNotFound) {
					t.Fatal("existing ErrNotFound contract lost", err)
				}
			})
		}
	}
}

func TestMutationPhaseTransmissionReadOnlyRPCVersusIssuedRPC(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, action := range []string{"control", "tags"} {
			for _, mode := range []string{"connect_rejected", "lookup_missing", "lookup_malformed", "post_500", "post_disconnect", "post_rpc_error", "ok"} {
				if action == "tags" && strings.HasPrefix(mode, "lookup_") {
					continue
				}
				t.Run(fmt.Sprintf("legacy_%t/%s/%s", legacy, action, mode), func(t *testing.T) {
					var writes atomic.Int32
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req map[string]json.RawMessage
						if json.NewDecoder(r.Body).Decode(&req) != nil {
							t.Error("invalid request")
							return
						}
						var method string
						_ = json.Unmarshal(req["method"], &method)
						if legacy && req["jsonrpc"] != nil {
							reply(w, map[string]any{"result": "method name not recognized", "arguments": map[string]any{}, "tag": 0})
							return
						}
						result := map[string]any{}
						operation := strings.ReplaceAll(method, "-", "_")
						switch operation {
						case "session_get":
							if mode == "connect_rejected" {
								http.Error(w, "connection rejected", 500)
								return
							}
							result["version"] = "owned-4.1"
						case "torrent_get":
							rows := []any{map[string]any{"hash_string": nativeHash, "hashString": nativeHash, "name": "owned", "status": 6, "percent_done": 1, "percentDone": 1, "total_size": 4, "totalSize": 4, "left_until_done": 0, "leftUntilDone": 0}}
							if mode == "lookup_missing" {
								rows = []any{}
							}
							if mode == "lookup_malformed" {
								rows = []any{map[string]any{"hash_string": "bad", "hashString": "bad"}}
							}
							result["torrents"] = rows
						case "torrent_remove", "torrent_set":
							writes.Add(1)
							if mode == "post_500" {
								http.Error(w, "synthetic-mutating-rpc-secret", 500)
								return
							}
							if mode == "post_disconnect" {
								conn, _, _ := w.(http.Hijacker).Hijack()
								_ = conn.Close()
								return
							}
						default:
							t.Error("unexpected method", method)
						}
						if legacy {
							var tag any
							_ = json.Unmarshal(req["tag"], &tag)
							status := "success"
							if mode == "post_rpc_error" && strings.HasPrefix(operation, "torrent_") && operation != "torrent_get" {
								status = "rejected"
							}
							reply(w, map[string]any{"result": status, "arguments": result, "tag": tag})
						} else {
							var id any
							_ = json.Unmarshal(req["id"], &id)
							res := map[string]any{"jsonrpc": "2.0", "result": result, "id": id}
							if mode == "post_rpc_error" && (operation == "torrent_remove" || operation == "torrent_set") {
								res["error"] = map[string]any{"code": -1, "message": "rejected"}
							}
							reply(w, res)
						}
					}))
					defer s.Close()
					c := mustClient(t, "transmission", s.URL)
					var err error
					if action == "control" {
						err = c.Control(context.Background(), nativeHash, "remove_keep_data")
					} else {
						err = c.SetTorrentTags(context.Background(), nativeHash, []string{"owned"})
					}
					pre := mode == "connect_rejected" || strings.HasPrefix(mode, "lookup_")
					if mode == "ok" {
						if err != nil || writes.Load() != 1 {
							t.Fatal("normal RPC changed", err, writes.Load())
						}
						return
					}
					if err == nil || MutationNotStarted(err) != pre {
						t.Fatal("wrong RPC write boundary", MutationNotStarted(err), err)
					}
					want := int32(1)
					if pre {
						want = 0
					}
					if writes.Load() != want {
						t.Fatal("proof differs from observed RPC", writes.Load(), want)
					}
				})
			}
		}
	}
}
