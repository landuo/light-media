package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func originalCenterTestEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"MP_SERVER_HOST", "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "PROXY_HOST"} {
		t.Setenv(key, "")
	}
}
func originalCenterComparableJSON(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if e := d.Decode(&value); e != nil {
		t.Fatalf("not JSON: %q %v", raw, e)
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch v := v.(type) {
		case json.Number:
			r, ok := new(big.Rat).SetString(string(v))
			if !ok {
				t.Fatal("invalid JSON number")
			}
			return struct{ Number string }{r.RatString()}
		case []any:
			for i, x := range v {
				v[i] = normalize(x)
			}
		case map[string]any:
			for k, x := range v {
				v[k] = normalize(x)
			}
		}
		return v
	}
	return normalize(value)
}

func TestOriginalPluginCenterFullSourceHTTP(t *testing.T) {
	originalCenterTestEnvironment(t)
	var reference struct {
		Source       string `json:"source_sha"`
		Observations []struct {
			Name         string `json:"case"`
			Method, Path string
			RequestBody  *string `json:"request_body"`
			RemoteBody   string  `json:"remote_body"`
			RemoteStatus int     `json:"remote_status"`
			Status       int
			ContentType  string `json:"content_type"`
			Body         string
			PeerCalls    []json.RawMessage `json:"peer_calls"`
		}
	}
	raw, e := os.ReadFile("../../testdata/plugin-server-20261030/source-http.json")
	if e != nil || json.Unmarshal(raw, &reference) != nil || reference.Source != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(reference.Observations) != 230 {
		t.Fatal("fixed complete HTTP source observations", e)
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	index := guideMarketIndex("Owned", "https://owned.invalid/not-downloaded")
	manifest, _ := json.Marshal(index.Releases[0].Manifest)
	if e = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: "Owned", PluginID: "Owned", Version: "2.0", ManifestSchemaVersion: 1, Manifest: manifest}); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	responseBody := "{}"
	responseStatus := 200
	calls := []map[string]any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := map[string]any{"method": r.Method, "path": r.URL.RequestURI(), "uid": r.Header.Get("X-MoviePilot-User-Uid") != ""}
		if r.Method == http.MethodPost {
			d := json.NewDecoder(r.Body)
			d.UseNumber()
			var body any
			if e := d.Decode(&body); e != nil {
				t.Error(e)
			}
			call["body"] = body
		}
		mu.Lock()
		calls = append(calls, call)
		body, status := responseBody, responseStatus
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(peer.URL)
	share := true
	settings.PluginStatisticShare = &share
	if e = a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	for _, c := range reference.Observations {
		t.Run(c.Name, func(t *testing.T) {
			mu.Lock()
			responseBody, responseStatus = c.RemoteBody, c.RemoteStatus
			before := len(calls)
			mu.Unlock()
			payload := ""
			if c.RequestBody != nil {
				payload = *c.RequestBody
			}
			result := callTest(t, a.Handler(), c.Method, "/api/plugins/"+c.Path, payload, cookie, "")
			mu.Lock()
			gotCalls := append([]map[string]any{}, calls[before:]...)
			mu.Unlock()
			actualWire, _ := json.Marshal(gotCalls)
			wantWire, _ := json.Marshal(c.PeerCalls)
			if !reflect.DeepEqual(originalCenterComparableJSON(t, actualWire), originalCenterComparableJSON(t, wantWire)) {
				t.Errorf("peer protocol differs: got %s want %s", actualWire, wantWire)
			}
			if result.Code != c.Status {
				t.Errorf("status got%d want%d: %s", result.Code, c.Status, result.Body.String())
				return
			}
			if c.ContentType == "text/plain; charset=utf-8" {
				if result.Header().Get("Content-Type") != c.ContentType || result.Body.String() != c.Body {
					t.Errorf("source HTTP500 wire changed: %q %q", result.Header().Get("Content-Type"), result.Body.String())
				}
				return
			}
			if !strings.HasPrefix(result.Header().Get("Content-Type"), "application/json") {
				t.Error("JSON content type missing")
			}
			actual := result.Body.Bytes()
			if result.Code == 200 {
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(actual, &envelope) != nil || len(envelope.Data) == 0 {
					t.Fatal("native success adapter missing")
				}
				actual = envelope.Data
			}
			if !reflect.DeepEqual(originalCenterComparableJSON(t, actual), originalCenterComparableJSON(t, []byte(c.Body))) {
				t.Errorf("source response differs: got%s want%s", actual, c.Body)
			}
		})
	}
}

func TestOriginalPluginCenterSourceV1V2Wire(t *testing.T) {
	originalCenterTestEnvironment(t)
	var reference struct {
		Source       string `json:"source_sha"`
		Observations []struct {
			RequestBodyHex string `json:"request_body_hex"`
			RemoteBodyHex  string `json:"remote_body_hex"`

			Version            string
			RequestContentType *string `json:"request_content_type"`
			Allow, Location    *string

			Name         string `json:"case"`
			Method, Path string
			RequestBody  *string `json:"request_body"`
			RemoteBody   string  `json:"remote_body"`
			RemoteStatus int     `json:"remote_status"`
			Status       int
			ContentType  string `json:"content_type"`
			Body         string
			PeerCalls    []json.RawMessage `json:"peer_calls"`
		}
	}
	raw, e := os.ReadFile("../../testdata/plugin-server-20261030/source-wire.json")
	if e != nil || json.Unmarshal(raw, &reference) != nil || reference.Source != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(reference.Observations) != 660 {
		t.Fatal("fixed complete HTTP source observations", e)
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	index := guideMarketIndex("Owned", "https://owned.invalid/not-downloaded")
	manifest, _ := json.Marshal(index.Releases[0].Manifest)
	if e = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: "Owned", PluginID: "Owned", Version: "2.0", ManifestSchemaVersion: 1, Manifest: manifest}); e != nil {
		t.Fatal(e)
	}
	originalCenterSetList(t, a, []string{"Owned", "space +中", "punct:@!$&'()*+,;=[]~"})
	var mu sync.Mutex
	responseBody := "{}"
	responseStatus := 200
	calls := []map[string]any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := map[string]any{"method": r.Method, "path": r.URL.RequestURI(), "uid": r.Header.Get("X-MoviePilot-User-Uid") != ""}
		if r.Method == http.MethodPost {
			d := json.NewDecoder(r.Body)
			d.UseNumber()
			var body any
			if e := d.Decode(&body); e != nil {
				t.Error(e)
			}
			call["body"] = body
		}
		mu.Lock()
		calls = append(calls, call)
		body, status := responseBody, responseStatus
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(peer.URL)
	share := true
	settings.PluginStatisticShare = &share
	if e = a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	for _, c := range reference.Observations {
		t.Run(c.Name, func(t *testing.T) {
			mu.Lock()
			responseBody, responseStatus = c.RemoteBody, c.RemoteStatus
			if c.RemoteBodyHex != "" {
				raw, e := hex.DecodeString(c.RemoteBodyHex)
				if e != nil {
					t.Fatal(e)
				}
				responseBody = string(raw)
			}
			before := len(calls)
			mu.Unlock()
			payload := ""
			if c.RequestBody != nil {
				payload = *c.RequestBody
			}
			if c.RequestBodyHex != "" {
				raw, e := hex.DecodeString(c.RequestBodyHex)
				if e != nil {
					t.Fatal(e)
				}
				payload = string(raw)
			}
			request := httptest.NewRequest(c.Method, "http://owned-asgi/api/"+c.Version+"/plugin/"+c.Path, strings.NewReader(payload))
			request.AddCookie(cookie)
			contentType := "application/json"
			if c.RequestContentType != nil {
				contentType = *c.RequestContentType
			}
			request.Header.Set("Content-Type", contentType)
			result := httptest.NewRecorder()
			a.Handler().ServeHTTP(result, request)
			mu.Lock()
			gotCalls := append([]map[string]any{}, calls[before:]...)
			mu.Unlock()
			actualWire, _ := json.Marshal(gotCalls)
			wantWire, _ := json.Marshal(c.PeerCalls)
			if !reflect.DeepEqual(originalCenterComparableJSON(t, actualWire), originalCenterComparableJSON(t, wantWire)) {
				t.Errorf("peer protocol differs: got %s want %s", actualWire, wantWire)
			}
			if result.Code != c.Status {
				t.Errorf("status got%d want%d: %s", result.Code, c.Status, result.Body.String())
				return
			}
			for name, want := range map[string]*string{"Allow": c.Allow, "Location": c.Location} {
				expected := ""
				if want != nil {
					expected = *want
				}
				if result.Header().Get(name) != expected {
					t.Errorf("%s got%q want%q", name, result.Header().Get(name), expected)
				}
			}
			if result.Header().Get("Content-Type") != c.ContentType {
				t.Errorf("Content-Type got%q want%q", result.Header().Get("Content-Type"), c.ContentType)
			}
			if c.Body == "" || c.ContentType == "text/plain; charset=utf-8" {
				if result.Body.String() != c.Body {
					t.Errorf("exact wire body got%q want%q", result.Body.String(), c.Body)
				}
				return
			}

			actual := result.Body.Bytes()

			if !reflect.DeepEqual(originalCenterComparableJSON(t, actual), originalCenterComparableJSON(t, []byte(c.Body))) {
				t.Errorf("source response differs: got%s want%s", actual, c.Body)
			}
		})
	}
}
