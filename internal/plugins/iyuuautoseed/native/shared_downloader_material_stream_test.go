package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/downloadercommands"
)

func ownedMaterialStreamPeer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: h}}
	peer.Start()
	t.Cleanup(peer.Close)
	return peer
}

// Mechanical comparison with the prior Go encoders, not a fabricated Python
// oracle. Source field/response semantics inherit the accepted S03 fixtures.
func TestMaterialStreamEncoding(t *testing.T) {
	content := []byte{0, 1, 2, '<', '>', '&', 255}
	form := url.Values{"cookie": {"owned=synthetic"}, "tags": {"one,two"}, "paused": {"True"}, "savepath": {"/owned"}}
	payload, err := commandQBMaterialPayload(form, content)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(payload.reader)
	if err != nil || int64(len(raw)) != payload.length {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(payload.contentType)
	if err != nil {
		t.Fatal(err)
	}
	var old bytes.Buffer
	writer := multipart.NewWriter(&old)
	if err = writer.SetBoundary(params["boundary"]); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err = writer.WriteField(key, form.Get(key)); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="torrent__0"; filename="torrent__0"`}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, old.Bytes()) {
		t.Fatal("multipart framing/bytes differ from retained encoder")
	}
	for _, n := range []int{0, 1, 2, 3, (24 << 10) - 1, 24 << 10, (24 << 10) + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			source := bytes.Repeat(content, n/len(content)+1)[:n]
			args := map[string]any{"cookies": "owned=synthetic", "download-dir": "/owned/<>&中文", "labels": []string{"b", "a"}, "paused": false}
			streamed, err := commandTRMaterialPayload(args, source)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(streamed.reader)
			if err != nil || int64(len(b)) != streamed.length {
				t.Fatal(err)
			}
			prior := map[string]any{}
			for key, v := range args {
				prior[key] = v
			}
			prior["metainfo"] = base64.StdEncoding.EncodeToString(source)
			expected, err := json.Marshal(map[string]any{"method": "torrent-add", "arguments": prior})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b, expected) {
				t.Fatal("JSON bytes/padding differ from retained encoder")
			}
			if _, changed := args["metainfo"]; changed {
				t.Fatal("caller args mutated")
			}
		})
	}
	if _, err = commandMaterialLength(math.MaxInt64, 1); err == nil {
		t.Fatal("wire size wrapped")
	}
	if _, err = commandMaterialBase64Length(math.MaxInt64); err == nil {
		t.Fatal("base64 size wrapped")
	}
}

type materialObserveTransport struct {
	next     http.RoundTripper
	t        *testing.T
	selected atomic.Int64
	before   func()
}

func (p *materialObserveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if mutationSelectedCall(r.Context()) {
		p.selected.Add(1)
		if p.before != nil {
			p.before()
		}
		if r.Method != http.MethodPost || r.ContentLength <= 0 || r.GetBody != nil {
			p.t.Error("selected material length/replay metadata")
		}
	}
	return p.next.RoundTrip(r)
}
func (p *materialObserveTransport) CloseIdleConnections() {
	if next, ok := p.next.(interface{ CloseIdleConnections() }); ok {
		next.CloseIdleConnections()
	}
}

func TestSharedMaterialStreamOwnedLargeAdd(t *testing.T) {
	const size = (16 << 20) + 1 // Native-only old-bound witness; no live downloader.
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 251)
	}
	expectedDigest := sha256.Sum256(content)
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			var sessions, adds int
			peer := ownedMaterialStreamPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				paths = append(paths, r.Method+" "+r.URL.Path)
				if kind == "qbittorrent" {
					switch r.URL.Path {
					case "/api/v2/auth/login":
						if err := r.ParseForm(); err != nil {
							t.Error(err)
						}
						if r.Form.Get("username") != "owned" || r.Form.Get("password") != "owned-password" {
							t.Error("login fields")
						}
						http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-session", Path: "/"})
						io.WriteString(w, "Ok.")
					case "/api/v2/app/webapiVersion":
						io.WriteString(w, "2.11.3")
					case "/api/v2/torrents/add":
						adds++
						if r.ContentLength <= int64(size) || len(r.TransferEncoding) != 0 {
							t.Error("fixed material Content-Length absent")
						}
						reader, err := r.MultipartReader()
						if err != nil {
							t.Error(err)
							return
						}
						fields := map[string]string{}
						files := 0
						for {
							part, err := reader.NextPart()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Error(err)
								return
							}
							if part.FileName() == "" {
								b, e := io.ReadAll(part)
								if e != nil {
									t.Error(e)
								}
								fields[part.FormName()] = string(b)
							} else {
								files++
								if part.FileName() != "torrent__0" || part.FormName() != "torrent__0" || part.Header.Get("Content-Type") != "" {
									t.Error("source bytes part metadata")
								}
								hash := sha256.New()
								n, e := io.Copy(hash, part)
								if e != nil || n != size || !bytes.Equal(hash.Sum(nil), expectedDigest[:]) {
									t.Error("material wire length/SHA", n, e)
								}
							}
							part.Close()
						}
						want := map[string]string{"savepath": "/owned", "category": "owned", "tags": "first,second", "autoTMM": "False", "sequentialDownload": "True", "firstLastPiecePrio": "False", "ratioLimit": "2.0"}
						if files != 1 || !reflect.DeepEqual(fields, want) {
							t.Error("source field mapping", fields)
						}
						io.WriteString(w, `{"added_torrent_ids":[9007199254740993],"owned":true}`)
					default:
						t.Error("unlisted fixed qB route", r.URL.Path)
						w.WriteHeader(404)
					}
					return
				}
				if r.URL.Path != "/transmission/rpc" {
					t.Error("unlisted TR route")
					w.WriteHeader(404)
					return
				}
				user, password, ok := r.BasicAuth()
				if !ok || user != "owned" || password != "owned-password" {
					t.Error("TR auth")
				}
				var body struct {
					Method    string         `json:"method"`
					Arguments map[string]any `json:"arguments"`
				}
				d := json.NewDecoder(r.Body)
				d.UseNumber()
				if err := d.Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if err := d.Decode(new(any)); err != io.EOF {
					t.Error("JSON tail", err)
				}
				if body.Method == "session-get" {
					sessions++
					w.Header().Set("X-Transmission-Session-Id", "owned-session")
					if sessions == 1 {
						w.WriteHeader(409)
						return
					}
					io.WriteString(w, `{"result":"success","arguments":{"rpc-version":17}}`)
					return
				}
				if body.Method != "torrent-add" {
					t.Error("unlisted fixed TR method")
					return
				}
				adds++
				if r.Header.Get("X-Transmission-Session-Id") != "owned-session" || len(r.TransferEncoding) != 0 || r.ContentLength <= int64(size) {
					t.Error("TR session/length")
				}
				encoded, ok := body.Arguments["metainfo"].(string)
				if !ok {
					t.Error("metainfo missing")
					return
				}
				hash := sha256.New()
				n, err := io.Copy(hash, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
				if err != nil || n != size || !bytes.Equal(hash.Sum(nil), expectedDigest[:]) {
					t.Error("TR material SHA/length", n, err)
				}
				delete(body.Arguments, "metainfo")
				if !reflect.DeepEqual(body.Arguments, map[string]any{"cookies": "owned=synthetic", "download-dir": "/owned", "paused": false, "labels": []any{"first", "second"}}) {
					t.Error("TR arguments", body.Arguments)
				}
				io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"id":9007199254740993,"hashString":"owned","extra":true}}}`)
			}))
			qb, tr, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-stream", Kind: kind, BaseURL: peer.URL, Username: "owned", Password: "owned-password"}, "", func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			var owner *sharedDownloaderCommands
			if qb != nil {
				owner = qb.(*sharedQBCommands).owner
			} else {
				owner = tr.(*sharedTRCommands).owner
			}
			beforeByte := content[0]
			observe := &materialObserveTransport{next: owner.iyuu.client.http.Transport, t: t, before: func() { content[0] ^= 0xff }}
			owner.iyuu.client.http.Transport = observe
			var result any
			if qb != nil {
				result, err = qb.Add(context.Background(), downloadercommands.QBAddCall{TorrentFiles: content, SavePath: "/owned", Category: "owned", Tags: []string{"first", "second"}, AutoManagement: false, Sequential: true, FirstLastPiece: false, Paused: false, Extra: map[string]any{"ratio_limit": json.Number("2.0")}})
			} else {
				result, err = tr.Add(context.Background(), downloadercommands.TRAddCall{Content: content, Cookies: "owned=synthetic", DownloadDir: "/owned", Paused: false, Labels: []string{"first", "second"}})
			}
			content[0] = beforeByte
			if err != nil {
				t.Fatal(err)
			}
			object, ok := result.(map[string]any)
			if !ok {
				t.Fatalf("native result %T", result)
			}
			if qb != nil {
				if object["added_torrent_ids"].([]any)[0] != json.Number("9007199254740993") {
					t.Fatal(object)
				}
			} else if object["id"] != json.Number("9007199254740993") || object["extra"] != true {
				t.Fatal(object)
			}
			mu.Lock()
			defer mu.Unlock()
			if adds != 1 || observe.selected.Load() != 1 {
				t.Fatal("selected attempts", adds, observe.selected.Load())
			}
			if kind == "qbittorrent" {
				if !reflect.DeepEqual(paths, []string{"POST /api/v2/auth/login", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/add"}) {
					t.Fatal(paths)
				}
			} else if sessions != 2 || len(paths) != 3 {
				t.Fatal("preflight negotiation changed", paths)
			}
		})
	}
}

func TestSharedMaterialStreamFailureAndRecovery(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			var selected atomic.Int64
			var sessions atomic.Int64
			peer := ownedMaterialStreamPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qbittorrent" {
					if r.URL.Path == "/api/v2/app/webapiVersion" {
						io.WriteString(w, "2.11.3")
						return
					}
					if r.URL.Path != "/api/v2/torrents/add" {
						t.Error("unlisted qB route")
						w.WriteHeader(404)
						return
					}
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Error(err)
					}
					switch selected.Add(1) {
					case 1:
						w.WriteHeader(401)
					case 2:
						w.WriteHeader(403)
					default:
						io.WriteString(w, "Ok.")
					}
					return
				}
				var in struct {
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
					return
				}
				if in.Method == "session-get" {
					sessions.Add(1)
					io.WriteString(w, `{"result":"success","arguments":{}}`)
					return
				}
				if in.Method != "torrent-add" {
					t.Error("unlisted TR method")
					return
				}
				if selected.Add(1) == 1 {
					w.Header().Set("X-Transmission-Session-Id", "owned-new")
					w.WriteHeader(409)
					return
				}
				io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"id":1}}}`)
			}))
			qb, tr, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-stream-recovery", Kind: kind, BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			call := func() (any, error) {
				if qb != nil {
					return qb.Add(context.Background(), downloadercommands.QBAddCall{TorrentFiles: []byte("owned")})
				}
				return tr.Add(context.Background(), downloadercommands.TRAddCall{Content: []byte("owned")})
			}
			failures := 1
			if qb != nil {
				failures = 2
			}
			for i := 1; i <= failures; i++ {
				if _, err = call(); err == nil || MutationNotDispatched(err) {
					t.Fatal("delegated response became source/no-effect", err)
				}
				if selected.Load() != int64(i) {
					t.Fatal("automatic selected retry", selected.Load())
				}
			}
			if _, err = call(); err != nil {
				t.Fatal("explicit same client recovery", err)
			}
			if selected.Load() != int64(failures+1) {
				t.Fatal("unexpected selected requests")
			}
			if tr != nil && sessions.Load() != 1 {
				t.Fatal("selected 409 invoked session retry")
			}
		})
	}
	// Owned pull-body cancellation/guard state, not a fabricated HTTP success.
	denied := errors.New("owned current authority denied")
	ctx, proof := beginMutation(context.Background())
	ctx = mutationSelected(ctx)
	checks := 0
	b := &commandMaterialBody{ctx: ctx, reader: strings.NewReader("owned"), guard: func(context.Context) error {
		checks++
		if checks == 2 {
			return denied
		}
		return nil
	}}
	p := make([]byte, 16)
	if n, err := b.Read(p); n != 0 || !errors.Is(err, denied) {
		t.Fatal("post-read authority", n, err)
	}
	if _, err := b.Read(p); !errors.Is(err, denied) {
		t.Fatal("failed body resurrected", err)
	}
	if proof.terminal.Load() != mutationFailureOwner {
		t.Fatal("owner provenance lost")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = b.Close() }()
	}
	wg.Wait()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c := &commandMaterialBody{ctx: canceled, reader: strings.NewReader("owned"), guard: func(context.Context) error { return nil }}
	if n, err := c.Read(p); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(n, err)
	}
}
