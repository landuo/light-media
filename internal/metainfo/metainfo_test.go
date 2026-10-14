package metainfo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"light-media/internal/integrations"
)

func bencode(v any) []byte {
	switch x := v.(type) {
	case string:
		return append([]byte(strconv.Itoa(len(x))+":"), x...)
	case []byte:
		return append([]byte(strconv.Itoa(len(x))+":"), x...)
	case int:
		return []byte(fmt.Sprintf("i%de", x))
	case int64:
		return []byte(fmt.Sprintf("i%de", x))
	case []any:
		out := []byte{'l'}
		for _, v := range x {
			out = append(out, bencode(v)...)
		}
		return append(out, 'e')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []byte{'d'}
		for _, k := range keys {
			out = append(out, bencode(k)...)
			out = append(out, bencode(x[k])...)
		}
		return append(out, 'e')
	default:
		panic("unsupported synthetic bencode value")
	}
}
func syntheticV1() []byte {
	return bencode(map[string]any{"announce": "https://tracker.example.invalid/secret-passkey", "info": map[string]any{"length": int64(1024), "name": "synthetic-secret-filename.mkv", "piece length": 16384, "pieces": bytes.Repeat([]byte{'s'}, 20)}})
}

func TestSyntheticV1AndV2Info(t *testing.T) {
	r, err := Inspect(syntheticV1())
	if err != nil || !r.V1 || r.V2 || !r.InfoDictionaryValid || !r.RawInfoHashComputed || r.FileCount != 1 || r.TotalBytes != 1024 || r.PayloadVerified {
		t.Fatalf("%+v %v", r, err)
	}
	first := sha256.Sum256([]byte("synthetic block A"))
	second := sha256.Sum256([]byte("synthetic block B"))
	layer := append(append([]byte{}, first[:]...), second[:]...)
	rootHash := sha256.Sum256(layer)
	v2 := bencode(map[string]any{"info": map[string]any{"file tree": map[string]any{"synthetic.mkv": map[string]any{"": map[string]any{"length": int64(32768), "pieces root": rootHash[:]}}}, "meta version": 2, "name": "synthetic", "piece length": 16384}, "piece layers": map[string]any{string(rootHash[:]): layer}})
	r, err = Inspect(v2)
	if err != nil || r.V1 || !r.V2 || !r.PieceLayersChecked || r.FileCount != 1 || r.TotalBytes != 32768 {
		t.Fatalf("%+v %v", r, err)
	}
	layer[len(layer)-1] ^= 1
	bad := bencode(map[string]any{"info": map[string]any{"file tree": map[string]any{"synthetic.mkv": map[string]any{"": map[string]any{"length": int64(32768), "pieces root": rootHash[:]}}}, "meta version": 2, "name": "synthetic", "piece length": 16384}, "piece layers": map[string]any{string(rootHash[:]): layer}})
	if _, err := Inspect(bad); err == nil {
		t.Fatal("incorrect piece-layer root accepted")
	}
}

func TestBencodeMalformedAndResourceLimits(t *testing.T) {
	cases := [][]byte{[]byte("<html>synthetic-secret-login</html>"), []byte("d4:infod1:ai1e1:ai2eee"), []byte("d4:infod1:bi1e1:ai2eee"), []byte("d4:infoi1ee"), []byte("d4:info04:fakee"), []byte("99999999:a"), []byte("d4:infoi-0ee"), []byte("d4:infoi01ee"), []byte("d4:infoi+1ee"), []byte("d4:infod4:name3:abc"), append(syntheticV1(), 'x'), bytes.Repeat([]byte{'x'}, MaxBody+1)}
	for i, b := range cases {
		if _, err := Inspect(b); err == nil {
			t.Fatalf("malformed case %d accepted", i)
		}
	}
	deep := append(bytes.Repeat([]byte{'l'}, MaxDepth+2), bytes.Repeat([]byte{'e'}, MaxDepth+2)...)
	if _, err := Inspect(deep); err == nil {
		t.Fatal("deep input accepted")
	}
	items := append([]byte{'l'}, bytes.Repeat([]byte("0:"), MaxItems+1)...)
	items = append(items, 'e')
	if _, err := Inspect(items); err == nil {
		t.Fatal("item flood accepted")
	}
	badCount := bencode(map[string]any{"info": map[string]any{"length": 32769, "name": "synthetic", "piece length": 16384, "pieces": bytes.Repeat([]byte{'x'}, 20)}})
	if _, err := Inspect(badCount); err == nil {
		t.Fatal("piece count mismatch accepted")
	}
}

func TestV2PaddingLayerAndHybridLayout(t *testing.T) {
	zero := make([]byte, 64)
	padding := sha256.Sum256(zero) // a missing 32KiB piece covers two zero leaf hashes
	a, b, c := sha256.Sum256([]byte("A")), sha256.Sum256([]byte("B")), sha256.Sum256([]byte("C"))
	left := sha256.Sum256(append(append([]byte{}, a[:]...), b[:]...))
	right := sha256.Sum256(append(append([]byte{}, c[:]...), padding[:]...))
	rootHash := sha256.Sum256(append(append([]byte{}, left[:]...), right[:]...))
	layer := append(append(append([]byte{}, a[:]...), b[:]...), c[:]...)
	encoded := bencode(map[string]any{"info": map[string]any{"file tree": map[string]any{"synthetic": map[string]any{"": map[string]any{"length": int64(65537), "pieces root": rootHash[:]}}}, "meta version": 2, "name": "synthetic", "piece length": 32768}, "piece layers": map[string]any{string(rootHash[:]): layer}})
	if _, err := Inspect(encoded); err != nil {
		t.Fatalf("padding vector: %v", err)
	}
	firstRoot, secondRoot := bytes.Repeat([]byte{'a'}, 32), bytes.Repeat([]byte{'b'}, 32)
	tree := map[string]any{"first": map[string]any{"": map[string]any{"length": 100, "pieces root": firstRoot}}, "second": map[string]any{"": map[string]any{"length": 100, "pieces root": secondRoot}}}
	info := map[string]any{"file tree": tree, "files": []any{map[string]any{"length": 100, "path": []any{"first"}}, map[string]any{"attr": "p", "length": 16284, "path": []any{".pad", "16284"}}, map[string]any{"length": 100, "path": []any{"second"}}}, "meta version": 2, "name": "synthetic", "piece length": 16384, "pieces": bytes.Repeat([]byte{'x'}, 40)}
	hybrid := bencode(map[string]any{"info": info, "piece layers": map[string]any{}})
	summary, err := Inspect(hybrid)
	if err != nil || !summary.V1 || !summary.V2 || summary.FileCount != 2 || summary.TotalBytes != 200 {
		t.Fatalf("hybrid %+v %v", summary, err)
	}
	tree["different-name"] = tree["first"]
	delete(tree, "first")
	if _, err := Inspect(bencode(map[string]any{"info": info, "piece layers": map[string]any{}})); err == nil {
		t.Fatal("hybrid mismatched paths accepted")
	}
}

func TestMetainfoHTTPBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			fmt.Fprint(w, syntheticAuth+syntheticTable)
			return
		}
		w.Write(bytes.Repeat([]byte{'x'}, MaxBody+1))
	}))
	defer server.Close()
	r := ProbeSite(context.Background(), integrations.Site{ID: "synthetic", Kind: "ubits", BaseURL: server.URL, Cookie: "synthetic", Query: "synthetic"})
	if r.Status != "body_limit" || r.ProtectedMaterialAvailable {
		t.Fatalf("%+v", r)
	}
}

const syntheticAuth = `<a href="logout.php">Exit</a>`
const syntheticTable = `<table class="torrents"><tr><td><a href="details.php?id=101">Synthetic title</a><a href="download.php?id=101">download</a></td></tr><tr><td><a href="details.php?id=102">Second candidate</a><a href="download.php?id=102">download</a></td></tr></table>`
const syntheticGrid = `<input id='searchinput' name='keyword'><div class='torrents-table flex flex-col items-center'><div class='torrent-table-sub-info'><div class='torrent-table-for-spider-info'><a href='details.php?id=101'>Synthetic title</a></div><div class='torrent-manage'><a href='download.php?id=101&amp;passkey=syntheticSecretPasskey'>download</a></div></div></div>`

func TestFourHTMLKindsOnlyFetchOneCandidate(t *testing.T) {
	for _, kind := range []string{"hhanclub", "ubits", "jiayuan", "1ptba"} {
		t.Run(kind, func(t *testing.T) {
			searches, downloads := 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "synthetic-cookie" || r.Header.Get("x-api-key") != "" || r.Method != http.MethodGet {
					t.Error("credentials or method")
				}
				switch r.URL.Path {
				case "/torrents.php":
					searches++
					key := "search"
					if kind == "hhanclub" {
						key = "keyword"
					}
					if r.URL.Query().Get(key) != "synthetic & query" || r.URL.Query().Get("page") != "0" {
						t.Error("query")
					}
					shape := syntheticTable
					if kind == "hhanclub" {
						shape = syntheticGrid
					}
					fmt.Fprint(w, syntheticAuth+shape)
				case "/download.php":
					downloads++
					if r.URL.Query().Get("id") != "101" {
						t.Error("not first candidate")
					}
					if kind == "hhanclub" && r.URL.Query().Get("passkey") != "syntheticSecretPasskey" {
						t.Error("explicit same-origin passkey missing")
					}
					w.Write(syntheticV1())
				default:
					t.Error("unexpected request")
				}
			}))
			defer server.Close()
			r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: kind, BaseURL: server.URL, Cookie: "synthetic-cookie", Query: "synthetic & query"}, server.Client())
			if r.Status != "ok" || !r.ProtectedMaterialAvailable || searches != 1 || downloads != 1 || r.Requests != 2 {
				t.Fatalf("%+v counts %d/%d", r, searches, downloads)
			}
			encoded, _ := json.Marshal(r)
			for _, secret := range []string{"synthetic-cookie", "syntheticSecretPasskey", "synthetic-secret-filename", "secret-passkey", server.URL, "tracker.example.invalid"} {
				if bytes.Contains(encoded, []byte(secret)) {
					t.Fatalf("report leaks secret marker")
				}
			}
		})
	}
}

func TestHhanPasskeyRemainsHTTPSBoundedAndSiteScoped(t *testing.T) {
	for _, query := range []string{"id=101", "id=101&amp;passkey=", "id=101&amp;passkey=synthetic&amp;extra=1", "id=101&amp;passkey=one&amp;passkey=two", "id=102&amp;passkey=synthetic", "id=101&amp;passkey=%3Cbad%3E", "id=101&amp;passkey=" + strings.Repeat("x", 129)} {
		t.Run(query, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				shape := strings.Replace(syntheticGrid, "id=101&amp;passkey=syntheticSecretPasskey", query, 1)
				fmt.Fprint(w, syntheticAuth+shape)
			}))
			defer server.Close()
			r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "hhanclub", BaseURL: server.URL, Cookie: "synthetic-cookie", Query: "synthetic"}, server.Client())
			if r.Status != "unverified" || requests != 1 || r.ProtectedMaterialAvailable {
				t.Fatalf("%+v", r)
			}
		})
	}
	requests := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; fmt.Fprint(w, syntheticAuth+syntheticGrid) }))
	defer plain.Close()
	r := ProbeSite(context.Background(), integrations.Site{ID: "synthetic", Kind: "hhanclub", BaseURL: plain.URL, Cookie: "synthetic", Query: "synthetic"})
	if r.Status != "unverified" || requests != 1 {
		t.Fatalf("HTTP passkey policy %+v", r)
	}
}

func TestHhanPasskeyNeverSentOutsideOriginAndNeverRedirected(t *testing.T) {
	outsideCalls := 0
	outside := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outsideCalls++; w.Write(syntheticV1()) }))
	defer outside.Close()
	for _, redirect := range []bool{false, true} {
		requests := 0
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.URL.Path == "/download.php" {
				w.Header().Set("Location", outside.URL+"/download.php?id=101&passkey=syntheticSecretPasskey")
				w.WriteHeader(302)
				return
			}
			shape := syntheticGrid
			if !redirect {
				shape = strings.Replace(shape, "download.php?id=101", outside.URL+"/download.php?id=101", 1)
			}
			fmt.Fprint(w, syntheticAuth+shape)
		}))
		r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "hhanclub", BaseURL: server.URL, Cookie: "synthetic-cookie", Query: "synthetic"}, server.Client())
		server.Close()
		want, wantRequests := "unverified", 1
		if redirect {
			want, wantRequests = "redirect_blocked", 2
		}
		if r.Status != want || requests != wantRequests || outsideCalls != 0 || r.ProtectedMaterialAvailable {
			t.Fatalf("%+v requests=%d outside=%d", r, requests, outsideCalls)
		}
	}
}

func TestUnsafePrivateOrCrossOriginLinksNeverFetched(t *testing.T) {
	for _, href := range []string{"https://evil.example.invalid/download.php?id=101", "download.php?id=101&amp;passkey=synthetic-secret", "download.php?id=101&amp;id=102", "magnet:?xt=urn:btih:synthetic"} {
		t.Run(href, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				fmt.Fprint(w, syntheticAuth+strings.ReplaceAll(syntheticTable, "download.php?id=101", href))
			}))
			defer server.Close()
			r := ProbeSite(context.Background(), integrations.Site{ID: "synthetic", Kind: "ubits", BaseURL: server.URL, Cookie: "synthetic-secret", Query: "synthetic"})
			if r.Status != "unverified" || r.ProtectedMaterialAvailable || requests != 1 {
				t.Fatalf("%+v requests=%d", r, requests)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body []byte, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}

func TestMTeamTokenAndNoCredentialsOnDownloadURL(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Host == "api.m-team.cc" && req.URL.Path != "/download.php" {
			if req.Header.Get("x-api-key") != "synthetic-api-secret" || req.Header.Get("Cookie") != "" {
				t.Error("API auth")
			}
			switch req.URL.Path {
			case "/api/torrent/search":
				return response([]byte(`{"code":"0","data":{"data":[{"id":"101"},{"id":"102"}]}}`), 200), nil
			case "/api/torrent/genDlToken":
				data, _ := io.ReadAll(req.Body)
				q, _ := url.ParseQuery(string(data))
				if q.Get("id") != "101" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Error("token form")
				}
				return response([]byte(`{"code":"0","data":"https://download.m-team.cc/download.php?token=synthetic-url-secret"}`), 200), nil
			}
		}
		if req.URL.Host != "download.m-team.cc" || req.Header.Get("x-api-key") != "" || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" || req.Header.Get("Referer") != "" {
			t.Error("token URL policy")
		}
		return response(syntheticV1(), 200), nil
	})}
	r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic-api-secret", Cookie: "must-not-send", Query: "synthetic"}, client)
	if r.Status != "ok" || !r.ProtectedMaterialAvailable || requests != 3 {
		t.Fatalf("%+v", r)
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"synthetic-api-secret", "synthetic-url-secret", "must-not-send", "m-team.cc"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("secret leak")
		}
	}
}

func TestMTeamUnsafeTokenURLIsUnverified(t *testing.T) {
	for _, raw := range []string{"https://evil.example.invalid/a", "https://m-team.cc.evil.invalid/a", "http://download.m-team.cc/a", "https://user:secret@download.m-team.cc/a", "https://download.m-team.cc:8443/a"} {
		requests := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if strings.HasSuffix(req.URL.Path, "search") {
				return response([]byte(`{"code":0,"data":{"data":[{"id":101}]}}`), 200), nil
			}
			body, _ := json.Marshal(map[string]any{"code": 0, "data": raw})
			return response(body, 200), nil
		})}
		r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic", Query: "synthetic"}, client)
		if r.Status != "unverified" || r.ProtectedMaterialAvailable || requests != 2 {
			t.Fatalf("%+v", r)
		}
	}
}

func TestMTeamOneControlledCredentialFreeRedirect(t *testing.T) {
	cases := []struct {
		name, location, want string
		requests             int
		chain                bool
	}{
		{"HTTPS-family", "https://download.m-team.io/next?token=synthetic-location-secret", "ok", 4, false},
		{"exact-live-provider-CDN", "https://fr1.halomt.com:443/next?sign=synthetic-location-secret", "ok", 4, false},
		{"unobserved-CDN-sibling", "https://fr2.halomt.com/next", "redirect_blocked", 3, false},
		{"CDN-suffix-trick", "https://fr1.halomt.com.evil.invalid/next", "redirect_blocked", 3, false},
		{"relative-location", "/next?token=synthetic-location-secret", "ok", 4, false},
		{"cross-domain", "https://evil.invalid/next", "redirect_blocked", 3, false},
		{"suffix-trick", "https://m-team.cc.evil.invalid/next", "redirect_blocked", 3, false},
		{"HTTP-downgrade", "http://download.m-team.io/next", "redirect_blocked", 3, false},
		{"userinfo", "https://secret@download.m-team.io/next", "redirect_blocked", 3, false},
		{"nonstandard-port", "https://download.m-team.io:8443/next", "redirect_blocked", 3, false},
		{"fragment", "https://download.m-team.io/next#secret", "redirect_blocked", 3, false},
		{"missing-location", "", "redirect_blocked", 3, false},
		{"second-redirect", "https://download.m-team.io/next", "redirect_blocked", 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if requests <= 2 && req.Header.Get("x-api-key") != "synthetic-api-secret" {
					t.Error("missing API key")
				}
				switch requests {
				case 1:
					return response([]byte(`{"code":0,"data":{"data":[{"id":101}]}}`), 200), nil
				case 2:
					return response([]byte(`{"code":0,"data":"https://download.m-team.cc/first?token=synthetic-url-secret"}`), 200), nil
				default:
					for _, header := range []string{"x-api-key", "Cookie", "Authorization", "Referer"} {
						if req.Header.Get(header) != "" {
							t.Errorf("credential header %s on material request", header)
						}
					}
					if requests == 3 {
						r := response(nil, 302)
						r.Header.Set("Location", tc.location)
						r.Header.Set("Set-Cookie", "synthetic-response-cookie=secret")
						return r, nil
					}
					if tc.chain {
						r := response(nil, 302)
						r.Header.Set("Location", "https://download.m-team.cc/third")
						return r, nil
					}
					return response(syntheticV1(), 200), nil
				}
			})}
			r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic-api-secret", Cookie: "synthetic-cookie-secret", Query: "synthetic"}, client)
			if r.Status != tc.want || requests != tc.requests || r.Requests != tc.requests || r.ProtectedMaterialAvailable != (tc.want == "ok") {
				t.Fatalf("%+v requests=%d", r, requests)
			}
			if tc.chain && (r.Reason != "redirect_limit" || r.ControlledRedirects != 1) {
				t.Fatalf("redirect chain %+v", r)
			}
			b, _ := json.Marshal(r)
			for _, secret := range []string{"synthetic-api-secret", "synthetic-location-secret", "synthetic-url-secret", "synthetic-response-cookie", tc.location} {
				if secret != "" && bytes.Contains(b, []byte(secret)) {
					t.Fatal("redirect report leaks private marker")
				}
			}
		})
	}
}

func TestMTeamAPIRedirectRemainsBlocked(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		r := response(nil, 307)
		r.Header.Set("Location", "https://api.m-team.io/api/torrent/search")
		return r, nil
	})}
	r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "mteam", BaseURL: "https://api.m-team.cc", APIKey: "synthetic", Query: "synthetic"}, client)
	if r.Status != "redirect_blocked" || requests != 1 || r.ControlledRedirects != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestExactCDNIsMaterialOnlyNotAPIOrigin(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) { requests++; return response(nil, 200), nil })}
	r := probe(context.Background(), integrations.Site{ID: "synthetic", Kind: "mteam", BaseURL: "https://fr1.halomt.com", APIKey: "synthetic-api-secret", Query: "synthetic"}, client)
	if r.Status != "unverified" || r.Reason != "api_origin_not_allowlisted" || requests != 0 {
		t.Fatalf("CDN accepted API key: %+v", r)
	}
	for _, raw := range []string{"https://fr1.halomt.com/a", "https://fr1.halomt.com:443/a"} {
		u, _ := url.Parse(raw)
		if !allowedMTeamMaterialURL(u) || allowedMTeamAPI(u) {
			t.Fatal("exact material host policy")
		}
	}
	for _, raw := range []string{"https://fr2.halomt.com/a", "https://fr1.halomt.com.evil.invalid/a", "https://halomt.com/a", "http://fr1.halomt.com/a", "https://fr1.halomt.com:8443/a", "https://secret@fr1.halomt.com/a"} {
		u, _ := url.Parse(raw)
		if allowedMTeamMaterialURL(u) {
			t.Fatal("broadened material host policy")
		}
	}
}

func TestHTTPFailureHTMLAndRedirectNeverBecomeMaterial(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{{"auth", "secret", "authentication_failed", 401}, {"limit", "secret", "rate_limited", 429}, {"HTML", "<html>secret login</html>", "invalid_metainfo", 200}, {"password", "<input type='password'>", "authentication_failed", 200}, {"redirect", "", "redirect_blocked", 302}} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path == "/torrents.php" {
					fmt.Fprint(w, syntheticAuth+syntheticTable)
					return
				}
				if tc.code == 302 {
					w.Header().Set("Location", "https://evil.invalid/")
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			r := ProbeSite(context.Background(), integrations.Site{ID: "synthetic", Kind: "ubits", BaseURL: server.URL, Cookie: "synthetic", Query: "synthetic"})
			if r.Status != tc.want || r.ProtectedMaterialAvailable || requests != 2 {
				t.Fatalf("%+v requests=%d", r, requests)
			}
		})
	}
}

func TestSyntheticFixtureFile(t *testing.T) {
	b, err := os.ReadFile("../../testdata/protocol/synthetic-torrent-v1.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(b); err != nil {
		t.Fatal(err)
	}
}
