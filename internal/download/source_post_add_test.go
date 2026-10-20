package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type postAddOracleCase struct {
	Kind, Branch, Mode                                           string
	Force, Selected, FilesPresent, AcceptedHash, MetadataMissing bool
	Calls                                                        []string
	WrapperReturns                                               []struct {
		Method string
		Value  *bool
	}
}

// This exercises actual Client.Add and fixed post-Add HTTP requests against
// an owned server, then compares their order/returns to the original AST oracle.
// The oracle's backend exception maps to an HTTP 500 here: the source wrapper
// return is equivalent, but the native effect remains unknown, never an ACK.
func TestSourcePostAddAgainstOriginalModuleAndServerAST(t *testing.T) {
	var oracle struct{ Cases []postAddOracleCase }
	fixture, err := os.ReadFile("testdata/post-add-source-oracle.json")
	if err != nil || json.Unmarshal(fixture, &oracle) != nil {
		t.Fatal("read source oracle", err)
	}
	if len(oracle.Cases) != 192 {
		t.Fatal("source case count", len(oracle.Cases))
	}
	for i, sample := range oracle.Cases {
		t.Run(fmt.Sprintf("%03d_%s_%s_force_%t_selected_%t_%s_files_%t", i, sample.Kind, sample.Branch, sample.Force, sample.Selected, sample.Mode, sample.FilesPresent), func(t *testing.T) {
			t.Parallel()
			torrent, hash := testTorrent()
			var mu sync.Mutex
			calls := []string{}
			var adds atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Referer() != sOrigin(r)+"/" {
					t.Error("lost configured referer")
				}
				if sample.Kind == "qbittorrent" {
					switch r.URL.Path {
					case "/api/v2/torrents/info":
						if sample.Branch == "duplicate" || adds.Load() > 0 {
							reply(w, []any{qbRow(hash)})
						} else {
							reply(w, []any{})
						}
						return
					case "/api/v2/torrents/add":
						if err := r.ParseMultipartForm(4 << 20); err != nil {
							t.Error(err)
						}
						if r.FormValue("stopped") != fmt.Sprint(sample.Selected) {
							t.Error("actual Add paused differs", r.FormValue("stopped"))
						}
						if sample.Branch == "rejected" {
							io.WriteString(w, "Fails.")
							return
						}
						adds.Add(1)
						io.WriteString(w, "Ok.")
						return
					}
					r.ParseForm()
					op := map[string]string{"/api/v2/torrents/files": "files", "/api/v2/torrents/filePrio": "files_unwanted", "/api/v2/torrents/start": "start", "/api/v2/torrents/setForceStart": "force_start"}[r.URL.Path]
					if op == "" || r.Method != "POST" {
						t.Error("ungranted postAdd operation", r.URL.Path)
						http.Error(w, "bad", 400)
						return
					}
					mu.Lock()
					calls = append(calls, op)
					mu.Unlock()
					selector := r.Form.Get("hashes")
					if op == "files" || op == "files_unwanted" {
						selector = r.Form.Get("hash")
					}
					if selector != hash {
						t.Error("widened exact hash", r.Form)
					}
					if op == "force_start" && r.Form.Get("value") != "true" {
						t.Error("force flag not sent")
					}
					if op == "files_unwanted" && (r.Form.Get("id") != "3" || r.Form.Get("priority") != "0") {
						t.Error("wrong files suppressed", r.Form)
					}
					if postAddOracleFailure(sample.Mode, op) {
						http.Error(w, "owned failure must remain secret", 500)
						return
					}
					if op == "files" {
						if sample.FilesPresent {
							io.WriteString(w, `[{"index":7,"name":"a"},{"index":3,"name":"b"}]`)
						} else {
							io.WriteString(w, `[]`)
						}
					}
					return
				}
				var request map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad RPC")
					return
				}
				var method string
				json.Unmarshal(request["method"], &method)
				var args map[string]json.RawMessage
				json.Unmarshal(request["params"], &args)
				result := map[string]any{}
				switch method {
				case "session_get":
					result["version"] = "owned-4.1"
				case "torrent_get":
					if _, present := args["fields"]; !present {
						t.Error("missing fields")
					}
					var fields []string
					json.Unmarshal(args["fields"], &fields)
					if !containsPostAddString(fields, "files") {
						if sample.Branch == "duplicate" {
							result["torrents"] = []any{map[string]any{"hash_string": hash, "name": "demo.mkv", "status": 0, "percent_done": 0, "total_size": 1024, "left_until_done": 1024}}
						} else {
							result["torrents"] = []any{}
						}
						break
					}
					mu.Lock()
					calls = append(calls, "files")
					mu.Unlock()
					if postAddOracleFailure(sample.Mode, "files") {
						http.Error(w, "owned secret", 500)
						return
					}
					files := []any{}
					priorities := []int{}
					wanted := []bool{}
					if sample.FilesPresent {
						files = []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}
						priorities = []int{0, 0}
						wanted = []bool{true, true}
					}
					result["torrents"] = []any{map[string]any{"hash_string": hash, "files": files, "priorities": priorities, "wanted": wanted}}
				case "torrent_add":
					var paused bool
					json.Unmarshal(args["paused"], &paused)
					if paused != sample.Selected {
						t.Error("TR Add paused differs")
					}
					if sample.Branch == "rejected" {
						reply(w, map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -1, "message": "owned rejection"}})
						return
					}
					adds.Add(1)
					result["torrent_added"] = map[string]any{"hash_string": hash}
				case "torrent_set", "torrent_start":
					op := "start"
					if method == "torrent_set" {
						op = "files_unwanted"
						if args["files_wanted"] != nil {
							op = "files_wanted"
						}
					}
					mu.Lock()
					calls = append(calls, op)
					mu.Unlock()
					var ids []string
					json.Unmarshal(args["ids"], &ids)
					if !reflect.DeepEqual(ids, []string{hash}) {
						t.Error("TR widened selector", ids)
					}
					if postAddOracleFailure(sample.Mode, op) {
						http.Error(w, "owned secret", 500)
						return
					}
				default:
					t.Error("ungranted RPC", method)
				}
				reply(w, map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
			}))
			defer server.Close()
			client, err := New(Config{ID: "owned", Kind: sample.Kind, BaseURL: server.URL, ForceResume: sample.Force})
			if err != nil {
				t.Fatal(err)
			}
			added, err := client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/owned", Paused: sample.Selected})
			if sample.Branch == "rejected" {
				if !MutationRejected(err) {
					t.Fatal("actual fixed Add rejection lost", err)
				}
				if _, err = client.SourcePostAdd(context.Background(), added, nil, nil); !MutationNotStarted(err) {
					t.Fatal("rejected Add minted postAdd receipt", err)
				}
				return
			}
			if err != nil || added.Hash != hash || added.Duplicate != (sample.Branch == "duplicate") {
				t.Fatal("actual Add", added, err)
			}
			episodes := []int(nil)
			if sample.Selected {
				episodes = []int{1}
			}
			out, err := client.SourcePostAdd(context.Background(), added, episodes, func(name string) ([]int, error) {
				if name == "a" {
					return []int{1}, nil
				}
				return []int{2}, nil
			})
			if err != nil || out.Hash != hash || out.Selection.MetadataMissing != sample.MetadataMissing {
				t.Fatal("postAdd source branch", out, err)
			}
			mu.Lock()
			actual := append([]string{}, calls...)
			mu.Unlock()
			if !reflect.DeepEqual(actual, sample.Calls) {
				t.Fatal("native fixed calls differ from original AST", actual, sample.Calls)
			}
			mutations := []SourcePostAddEffect{}
			for _, effect := range out.Effects {
				if effect.Operation == "files" {
					continue
				}
				mutations = append(mutations, effect)
				failed := postAddOracleFailure(sample.Mode, effect.Operation)
				if effect.Acknowledged == failed || effect.Unknown != failed || (effect.Failure != nil) != failed || effect.NotStarted || effect.Rejected {
					t.Fatal("native failure was promoted to ACK or false no-effect proof", effect)
				}
				if effect.Failure != nil && strings.Contains(effect.Failure.Error(), "secret") {
					t.Fatal("remote body leaked")
				}
			}
			if len(mutations) != len(sample.WrapperReturns) {
				t.Fatal("wrapper return count", len(mutations), sample.WrapperReturns)
			}
			for i, expected := range sample.WrapperReturns {
				value := "none"
				if expected.Value != nil {
					value = fmt.Sprint(*expected.Value)
				}
				if mutations[i].OriginalReturn != value {
					t.Fatal("original None/False/True collapsed", mutations[i], expected)
				}
			}
			if _, err = client.SourcePostAdd(context.Background(), added, episodes, func(string) ([]int, error) { return []int{1}, nil }); !MutationNotStarted(err) {
				t.Fatal("same receipt was replayed", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(calls, actual) {
				t.Fatal("unknown/accepted receipt repeated daemon operation", calls, actual)
			}
		})
	}
}

func postAddOracleFailure(mode, operation string) bool {
	return mode == "files_exception" && operation == "files" || mode == "setter_exception" && strings.HasPrefix(operation, "files_") || mode == "start_exception" && (operation == "start" || operation == "force_start")
}
func containsPostAddString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func TestSourcePostAddReceiptCannotBeForgedOrReused(t *testing.T) {
	torrent, hash := testTorrent()
	var adds, forces atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if adds.Load() > 0 {
				reply(w, []any{qbRow(hash)})
			} else {
				reply(w, []any{})
			}
		case "/api/v2/torrents/add":
			adds.Add(1)
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/setForceStart":
			forces.Add(1)
		default:
			t.Error("ungranted receipt gate endpoint", r.URL.Path)
		}
	}))
	defer server.Close()
	config := Config{ID: "owned", Kind: "qb", BaseURL: server.URL, ForceResume: true}
	client, _ := New(config)
	other, _ := New(config)
	added, err := client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/owned"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(added)
	if err != nil {
		t.Fatal(err)
	}
	var reconstructed AddResult
	if json.Unmarshal(encoded, &reconstructed) != nil {
		t.Fatal("bad test JSON")
	}
	changedHash := added
	changedHash.Hash = strings.Repeat("a", 40)
	changedDuplicate := added
	changedDuplicate.Duplicate = true
	for _, bad := range []AddResult{{Hash: hash}, reconstructed, changedHash, changedDuplicate} {
		if _, err = client.SourcePostAdd(context.Background(), bad, nil, nil); !MutationNotStarted(err) {
			t.Fatal("forged result authorized native call", err)
		}
	}
	if _, err = other.SourcePostAdd(context.Background(), added, nil, nil); !MutationNotStarted(err) {
		t.Fatal("cross-client receipt authorized native call", err)
	}
	if _, err = client.SourcePostAdd(context.Background(), added, []int{1}, func(string) ([]int, error) { return nil, nil }); !MutationNotStarted(err) {
		t.Fatal("unpaused receipt authorized selected file branch", err)
	}
	if forces.Load() != 0 {
		t.Fatal("invalid receipt issued request")
	}
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := client.SourcePostAdd(context.Background(), added, nil, nil)
			if err == nil {
				successes.Add(1)
				if len(out.Effects) != 1 || !out.Effects[0].Acknowledged || out.Effects[0].OriginalReturn != "none" {
					t.Error("actual force ACK not distinct from source None", out)
				}
			} else if !MutationNotStarted(err) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || forces.Load() != 1 || adds.Load() != 1 {
		t.Fatal("receipt copy/concurrency replay", successes.Load(), forces.Load(), adds.Load())
	}
}

func TestSourcePostAddLostACKAndAuthenticationProofs(t *testing.T) {
	for _, mode := range []string{"disconnect", "server_error", "redirect", "api_key_rejected", "auth_then_login_failure", "login_preflight_failure", "auth_retry_ack"} {
		t.Run(mode, func(t *testing.T) {
			torrent, hash := testTorrent()
			var adds, forces, logins, foreign atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1) }))
			defer other.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					n := logins.Add(1)
					if n > 1 && (mode == "auth_then_login_failure" || mode == "login_preflight_failure") {
						http.Error(w, "owned login secret", 401)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-session", Path: "/"})
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/info":
					if adds.Load() > 0 {
						reply(w, []any{qbRow(hash)})
					} else {
						reply(w, []any{})
					}
				case "/api/v2/torrents/add":
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/setForceStart":
					n := forces.Add(1)
					switch mode {
					case "disconnect":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
					case "server_error":
						http.Error(w, "owned daemon secret", 500)
					case "redirect":
						http.Redirect(w, r, other.URL, 307)
					case "api_key_rejected", "auth_then_login_failure":
						http.Error(w, "owned rejection", 401)
					case "auth_retry_ack":
						if n == 1 {
							http.Error(w, "owned rejection", 403)
						}
					default:
						t.Error("unexpected force write after failed preflight")
					}
				default:
					t.Error("ungranted request", r.URL.Path)
				}
			}))
			defer server.Close()
			config := Config{ID: "owned", Kind: "qb", BaseURL: server.URL, ForceResume: true}
			if mode == "api_key_rejected" {
				config.APIKey = "owned-test-key"
			} else if strings.HasPrefix(mode, "auth_") || mode == "login_preflight_failure" {
				config.Username = "owned"
				config.Password = "owned-test-password"
			}
			client, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			added, err := client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/owned"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "login_preflight_failure" {
				client.loggedIn = false
			}
			out, err := client.SourcePostAdd(context.Background(), added, nil, nil)
			if err != nil || out.Hash != hash || len(out.Effects) != 1 {
				t.Fatal("accepted hash lost after source None", out, err)
			}
			effect := out.Effects[0]
			wantAck := mode == "auth_retry_ack"
			wantPre := mode == "login_preflight_failure"
			wantReject := mode == "api_key_rejected" || mode == "auth_then_login_failure"
			wantUnknown := !wantAck && !wantPre && !wantReject
			if effect.OriginalReturn != "none" || effect.Acknowledged != wantAck || effect.NotStarted != wantPre || effect.Rejected != wantReject || effect.Unknown != wantUnknown || (effect.Failure != nil) == wantAck {
				t.Fatal("actual phase/None incorrectly collapsed", effect)
			}
			wantWrites := int32(1)
			if wantPre {
				wantWrites = 0
			}
			if wantAck {
				wantWrites = 2
			}
			if forces.Load() != wantWrites || foreign.Load() != 0 {
				t.Fatal("unexpected retry or credential redirect", forces.Load(), foreign.Load())
			}
			if _, err = client.SourcePostAdd(context.Background(), added, nil, nil); !MutationNotStarted(err) || forces.Load() != wantWrites {
				t.Fatal("unknown/rejected receipt could be replayed", err, forces.Load())
			}
		})
	}
}

func TestSourcePostAddTransmissionFixedRPCRejectionAndLostACK(t *testing.T) {
	for _, dialect := range []string{"jsonrpc", "legacy"} {
		for _, mode := range []string{"ack", "rpc_rejected", "disconnect"} {
			t.Run(dialect+"/"+mode, func(t *testing.T) {
				torrent, hash := testTorrent()
				var adds, starts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("invalid request")
						return
					}
					var method string
					json.Unmarshal(request["method"], &method)
					if dialect == "legacy" && request["jsonrpc"] != nil {
						reply(w, map[string]any{"arguments": map[string]any{}, "tag": 0, "result": "unknown method"})
						return
					}
					var args map[string]json.RawMessage
					key := "params"
					if dialect == "legacy" {
						key = "arguments"
					}
					json.Unmarshal(request[key], &args)
					method = strings.ReplaceAll(method, "-", "_")
					result := map[string]any{}
					switch method {
					case "session_get":
						result["version"] = "owned-4.1"
					case "torrent_get":
						var fields []string
						json.Unmarshal(args["fields"], &fields)
						if containsPostAddString(fields, "files") {
							hashKey := "hash_string"
							if dialect == "legacy" {
								hashKey = "hashString"
							}
							result["torrents"] = []any{map[string]any{hashKey: hash, "files": []any{map[string]any{"name": "owned"}}, "priorities": []int{0}, "wanted": []bool{true}}}
						} else {
							result["torrents"] = []any{}
						}
					case "torrent_add":
						adds.Add(1)
						addKey, hashKey := "torrent_added", "hash_string"
						if dialect == "legacy" {
							addKey, hashKey = "torrent-added", "hashString"
						}
						result[addKey] = map[string]any{hashKey: hash}
					case "torrent_set": // both original setters run, including empty unwanted IDs.
					case "torrent_start":
						starts.Add(1)
						if mode == "disconnect" {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							conn.Close()
							return
						}
					default:
						t.Error("ungranted RPC", method)
					}
					rejected := mode == "rpc_rejected" && method == "torrent_start"
					if dialect == "legacy" {
						status := "success"
						if rejected {
							status = "owned rejection"
						}
						reply(w, map[string]any{"tag": request["tag"], "result": status, "arguments": result})
					} else {
						response := map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result}
						if rejected {
							response["error"] = map[string]any{"code": -1, "message": "owned secret"}
						}
						reply(w, response)
					}
				}))
				defer server.Close()
				client, err := New(Config{ID: "owned", Kind: "tr", BaseURL: server.URL, ForceResume: true})
				if err != nil {
					t.Fatal(err)
				}
				added, err := client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/owned", Paused: true})
				if err != nil {
					t.Fatal(err)
				}
				out, err := client.SourcePostAdd(context.Background(), added, []int{1}, func(string) ([]int, error) { return []int{1}, nil })
				if err != nil || len(out.Effects) != 4 || out.Hash != hash {
					t.Fatal("actual post-Add chain", out, err)
				}
				effect := out.Effects[3]
				sourceReturn := "false"
				if mode == "ack" {
					sourceReturn = "true"
				}
				if effect.Operation != "start" || effect.Acknowledged != (mode == "ack") || effect.Rejected != (mode == "rpc_rejected") || effect.Unknown != (mode == "disconnect") || effect.OriginalReturn != sourceReturn || starts.Load() != 1 || adds.Load() != 1 {
					t.Fatal("TR force ignored/False/proof mismatch", effect, starts.Load(), adds.Load())
				}
				if _, err = client.SourcePostAdd(context.Background(), added, []int{1}, nil); !MutationNotStarted(err) || starts.Load() != 1 {
					t.Fatal("TR lost ACK replay", err, starts.Load())
				}
			})
		}
	}
}
