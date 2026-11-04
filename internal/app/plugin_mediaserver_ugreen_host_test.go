package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/plugins/mediaservervendors"
)

// Public standard-library test material, not a user key. The same exact bytes
// and RSA>=2048 relationship have already been exercised by the API/HTTP gates.
//
//go:embed testdata/ugreen-host-public-go-testcert.go.txt
var ownedUgreenHostPublicKeySource []byte

func ownedUgreenHostKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	if fmt.Sprintf("%x", sha256.Sum256(ownedUgreenHostPublicKeySource)) != "812c69f79fc71384de1d93fb1058ab20e5b58ab2860dea72167681048d14a12e" {
		t.Fatal("public fixture key source drift")
	}
	matches := regexp.MustCompile(`(?s)-----BEGIN RSA TESTING KEY-----\n.*?-----END RSA TESTING KEY-----`).FindAll(ownedUgreenHostPublicKeySource, -1)
	if len(matches) != 1 {
		t.Fatal("one public fixture key required")
	}
	block, _ := pem.Decode(bytes.ReplaceAll(matches[0], []byte("RSA TESTING KEY"), []byte("PRIVATE KEY")))
	if block == nil {
		t.Fatal("public fixture PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	private, ok := key.(*rsa.PrivateKey)
	if !ok || private.N.BitLen() < 2048 {
		t.Fatal("public fixture RSA size")
	}
	return private
}
func ownedUgreenHostRSA(key *rsa.PrivateKey, encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(ciphertext) != key.Size() {
		return "", errors.New("owned single RSA block required")
	}
	plain, err := rsa.DecryptPKCS1v15(nil, key, ciphertext)
	return string(plain), err
}
func ownedUgreenHostGCM(key string) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("owned API UUID AES key required")
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func ownedUgreenHostQuery(key, encoded string) (string, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return "", err
	}
	aead, err := ownedUgreenHostGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("owned GCM frame too short")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	return string(plain), err
}
func ownedUgreenHostEncryptedResponse(key string, data any) ([]byte, error) {
	raw, err := json.Marshal(map[string]any{"code": 200, "msg": "owned success", "data": data})
	if err != nil {
		return nil, err
	}
	aead, err := ownedUgreenHostGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nil, nonce, raw, nil)
	return json.Marshal(map[string]string{"encrypt_resp_body": base64.StdEncoding.EncodeToString(append(nonce, sealed...))})
}

type ownedUgreenHostWire struct {
	mu                           sync.Mutex
	address, mediaPath, clientID string
	key                          *rsa.PrivateKey
	calls                        []string
	faults                       []string
}

func (w *ownedUgreenHostWire) count() int     { w.mu.Lock(); defer w.mu.Unlock(); return len(w.calls) }
func (w *ownedUgreenHostWire) client() string { w.mu.Lock(); defer w.mu.Unlock(); return w.clientID }
func (w *ownedUgreenHostWire) serve(out http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	refuse := func(reason string) {
		w.faults = append(w.faults, reason)
		http.Error(out, "owned protocol rejected", 400)
	}
	sequence := []string{
		"POST /ugreen/v1/verify/check", "POST /ugreen/v1/verify/login", "GET /ugreen/v1/user/current/user",
		"GET /ugreen/v1/video/homepage/media_list", "GET /ugreen/v1/video/poster_wall/media_lib/get_folder", "GET /ugreen/v1/video/media_lib/scan", "GET /ugreen/v1/video/media_lib/scan",
		"GET /ugreen/v1/user/current/user", "GET /ugreen/v1/video/homepage/media_list", "GET /ugreen/v1/video/poster_wall/media_lib/get_folder", "GET /ugreen/v1/video/media_lib/scan", "GET /ugreen/v1/video/media_lib/scan",
	}
	n := len(w.calls)
	if n >= len(sequence) || r.Host != w.address || r.TLS == nil || r.ProtoMajor != 1 || r.URL.IsAbs() || r.Method+" "+r.URL.EscapedPath() != sequence[n] || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
		refuse("outside exact twelve requests")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	_ = r.Body.Close()
	if err != nil || len(body) > 16384 {
		refuse("owned request body boundary")
		return
	}
	if r.Header.Get("User-Agent") != "python-requests/2.34.2" || r.Header.Get("Accept-Encoding") != "gzip, deflate" || r.Header.Get("Connection") != "keep-alive" {
		refuse("actual Session defaults")
		return
	}
	client := r.Header.Get("Client-Id")
	if n == 0 {
		w.clientID = client
	}
	if client == "" || client != w.clientID {
		refuse("warm/cold imported client identity")
		return
	}
	cookie := "owned=cookie-A"
	if n == 0 {
		cookie = ""
	}
	if r.Header.Get("Cookie") != cookie {
		refuse("real CookieJar/export/import")
		return
	}
	var payload []byte
	if n < 2 {
		if r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Ugreen-Security-Code") != "" {
			refuse("login envelope")
			return
		}
		var input map[string]json.RawMessage
		if json.Unmarshal(body, &input) != nil || string(input["username"]) != `"owned-user"` {
			refuse("synthetic username")
			return
		}
		if n == 0 {
			if len(input) != 1 {
				refuse("check body")
				return
			}
			public := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&w.key.PublicKey)})
			out.Header().Set("x-rsa-token", base64.StdEncoding.EncodeToString(public))
			out.Header().Set("Set-Cookie", "owned=cookie-A; Path=/; Secure; HttpOnly")
			payload = []byte(`{"code":200,"msg":"owned check","data":{}}`)
		} else {
			var encrypted string
			if len(input) != 5 || json.Unmarshal(input["password"], &encrypted) != nil || string(input["keepalive"]) != "true" || string(input["otp"]) != "true" || string(input["is_simple"]) != "true" {
				refuse("login body")
				return
			}
			password, err := ownedUgreenHostRSA(w.key, encrypted)
			if err != nil || password != "owned-password" {
				refuse("real RSA password")
				return
			}
			payload = []byte(`{"code":200,"msg":"owned login","data":{"token_id":"owned-token-A","staticToken":"owned-static-A","is_ugk":true}}`)
		}
	} else {
		if len(body) != 0 {
			refuse("GET body")
			return
		}
		key, err := ownedUgreenHostRSA(w.key, r.Header.Get("X-Ugreen-Security-Code"))
		if err != nil {
			refuse("real RSA AES key")
			return
		}
		token, err := ownedUgreenHostRSA(w.key, r.Header.Get("X-Ugreen-Token"))
		if err != nil || token != "owned-token-A" {
			refuse("real RSA token")
			return
		}
		digest := md5.Sum([]byte(token))
		if r.Header.Get("X-Ugreen-Security-Key") != hex.EncodeToString(digest[:]) {
			refuse("token digest")
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 1 || len(query["encrypt_query"]) != 1 {
			refuse("encrypted query carrier")
			return
		}
		plain, err := ownedUgreenHostQuery(key, query.Get("encrypt_query"))
		if err != nil {
			refuse("real AES query")
			return
		}
		expected := ""
		var data any
		switch r.URL.Path {
		case "/ugreen/v1/user/current/user":
			data = map[string]any{"uid": "owned-uid", "name": "Owned user"}
		case "/ugreen/v1/video/homepage/media_list":
			data = map[string]any{"media_lib_info_list": []any{map[string]any{"media_lib_set_id": 11, "media_name": "Owned movie", "video_count": 1}}}
		case "/ugreen/v1/video/poster_wall/media_lib/get_folder":
			expected = "page=1&page_size=100&sort_type=1&order_type=1"
			data = map[string]any{"folder_arr": []any{map[string]any{"media_lib_set_id": 11, "path": w.mediaPath}}, "is_last_page": true}
		case "/ugreen/v1/video/media_lib/scan":
			mode := 2
			if n == 6 || n == 11 {
				mode = 3
			}
			expected = fmt.Sprintf("op_type=2&media_lib_set_id=11&media_lib_scan_type=%d", mode)
			data = map[string]any{}
		default:
			refuse("unknown encrypted operation")
			return
		}
		if plain != expected {
			refuse("actual ordered refresh query")
			return
		}
		payload, err = ownedUgreenHostEncryptedResponse(key, data)
		if err != nil {
			refuse("owned encrypted response")
			return
		}
	}
	w.calls = append(w.calls, sequence[n])
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	out.WriteHeader(http.StatusOK)
	_, _ = out.Write(payload)
}

// This is a new composition test, not a replacement original oracle or a pure
// RecordStore test: ordinary factory/App/SQL/real worker/actual TLS all execute.
func TestOriginalUgreenAppSealedSessionRefreshAndCold(t *testing.T) {
	if !t.Run("owned_sealed_sync_failure_equal_retry_and_cold", ownedUgreenHostStoreFailure) {
		return
	}
	environment := append([]string(nil), os.Environ()...)
	t.Cleanup(func() {
		if !reflect.DeepEqual(environment, os.Environ()) {
			t.Error("UG App root changed environment")
		}
	})
	if _, present := os.LookupEnv("PLEXAPI_CONFIG_PATH"); present {
		t.Fatal("owned default profile requires no external Plex config path")
	}
	mediaPath := t.TempDir()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wire := &ownedUgreenHostWire{address: listener.Addr().String(), mediaPath: mediaPath, key: ownedUgreenHostKey(t)}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(wire.serve)}}
	peer.StartTLS()
	t.Cleanup(peer.Close)
	target, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	route, err := networkproxy.RequestsEnvironmentProxy(environment, target)
	if err != nil || route != nil {
		t.Fatal("normal existing route does not admit owned peer; no fallback")
	}
	repo, indexes, downloads := ownedNativeMediaArtifact(t)
	dir := t.TempDir()
	var active *App
	t.Cleanup(func() {
		if active != nil {
			_ = active.Close()
		}
	})
	open := func() *App {
		builder, err := NewOriginalMediaServerFactoryAssembly(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		a, err := New(Options{DataDir: dir, MediaServerFactories: builder})
		if err != nil {
			t.Fatal(err)
		}
		active = a
		if a.originalMediaServerAssembly == nil || len(a.originalMediaServerAssembly.definitions) != 6 {
			t.Fatal("ordinary six concrete factories absent")
		}
		return a
	}
	a := open()
	settings := a.Settings()
	empty := ""
	settings.PluginMarkets, settings.PluginLocalRepoPaths = []string{}, []string{}
	settings.PluginServerURL = &empty
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	a.Start(context.Background())
	install, _ := json.Marshal(map[string]string{"plugin_id": "OwnedMediaInstanceFixture", "repo_url": repo})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(install), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	workerFor := func(app *App) *originalPluginInstance {
		var p *originalPluginInstance
		ownedNativeMediaWait(t, func() bool {
			value, ok := app.pluginRunningInstances.Load("OwnedMediaInstanceFixture")
			if !ok {
				return false
			}
			p = value.(*originalPluginInstance)
			p.sessionMu.Lock()
			defer p.sessionMu.Unlock()
			return p.session != nil
		})
		if !p.external || p.hostContract != pluginResourcesSDKContract {
			t.Fatal("actual installed SDK worker absent")
		}
		return p
	}
	worker := workerFor(a)
	config, _ := json.Marshal([]any{map[string]any{"name": "ug-owned", "type": "ugreen", "enabled": true, "config": map[string]any{"host": peer.URL, "username": "owned-user", "password": "owned-password", "scan_mode": 2}}})
	response = callTest(t, a.Handler(), "POST", "/api/system/setting/MediaServers", string(config), cookie, "")
	if response.Code != 200 || bytes.Contains(response.Body.Bytes(), []byte("owned-password")) {
		t.Fatal("ordinary explicit producer or response redaction")
	}
	vendorFor := func(app *App) *mediaservervendors.Ugreen {
		var v *mediaservervendors.Ugreen
		ownedNativeMediaWait(t, func() bool {
			manager := app.originalMediaServerAssembly.manager
			if len(manager.Running()) != 6 {
				return false
			}
			owner := manager.RunningModule("UgreenModule")
			if owner == nil {
				return false
			}
			instances := owner.Instances()
			if len(instances) != 1 {
				return false
			}
			var ok bool
			v, ok = instances[0].Vendor.(*mediaservervendors.Ugreen)
			return ok
		})
		return v
	}
	warmVendor := vendorFor(a)
	if wire.count() != 3 {
		t.Fatal("warm ordinary constructor must perform check/login/current exactly", wire.count())
	}
	readCache := func(app *App) ([]byte, map[string]json.RawMessage) {
		var sealed []byte
		if err := app.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind=? AND id=?`, originalUgreenSessionKind, originalUgreenSessionKey).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"owned-password", "owned-token-A", "owned-static-A", "cookie-A", "BEGIN RSA PUBLIC KEY"} {
			if bytes.Contains(sealed, []byte(private)) {
				t.Fatal("private session row not sealed")
			}
		}
		raw, err := app.pluginStore.unsealBound(originalUgreenSessionKind, "system", originalUgreenSessionKey, sealed, 0)
		if err != nil {
			t.Fatal(err)
		}
		var records map[string]json.RawMessage
		if json.Unmarshal(raw, &records) != nil || len(records) != 1 {
			t.Fatal("one persisted Ugreen session")
		}
		sum := sha256.Sum256([]byte(strings.ToLower(peer.URL) + "|owned-user"))
		entry := records[hex.EncodeToString(sum[:])]
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &fields) != nil {
			t.Fatal("source cache key/entry")
		}
		digest := sha256.Sum256([]byte("owned-password"))
		for name, want := range map[string]string{"host": peer.URL, "username": "owned-user", "password_digest": hex.EncodeToString(digest[:]), "token": "owned-token-A", "static_token": "owned-static-A", "client_id": wire.client()} {
			var got string
			if json.Unmarshal(fields[name], &got) != nil || got != want {
				t.Fatal("actual persisted session field", name)
			}
		}
		var cookies map[string]string
		if json.Unmarshal(fields["cookies"], &cookies) != nil || !reflect.DeepEqual(cookies, map[string]string{"owned": "cookie-A"}) {
			t.Fatal("actual persisted exported CookieJar")
		}
		var updated int64
		if json.Unmarshal(fields["updated_at"], &updated) != nil || updated <= 0 {
			t.Fatal("real cache save timestamp")
		}
		return append([]byte(nil), sealed...), fields
	}
	sealedWarm, _ := readCache(a)
	refresh := func(p *originalPluginInstance, input any) ownedMediaRefreshResponse {
		var out ownedMediaRefreshResponse
		ownedNativeMediaCall(t, p, "refresh", input, &out)
		if out.PID <= 0 || out.Instance.Reference == "" || out.Result.Kind != "bool" || out.Result.Boolean == nil || !*out.Result.Boolean || out.Result.Library != nil {
			t.Fatal("real worker refresh did not return true bool")
		}
		return out
	}
	itemPath, _ := json.Marshal(filepath.Join(mediaPath, "owned.mkv"))
	item := plugins.MediaServerRefreshItem{Title: json.RawMessage(`"Owned"`), Year: json.RawMessage(`2026`), Type: plugins.MediaServerRefreshTypeMovie, Category: json.RawMessage(`null`), TargetPath: itemPath}
	root := refresh(worker, map[string]any{"name": "ug-owned", "root": true})
	itemResult := refresh(worker, map[string]any{"reference": root.Instance.Reference, "items": []plugins.MediaServerRefreshItem{item}, "options": plugins.MediaServerRefreshOptions{ScanMode: json.RawMessage(`3`)}})
	if itemResult.PID != root.PID || itemResult.Instance.Reference != root.Instance.Reference || wire.count() != 7 {
		t.Fatal("warm actual retained refresh request sequence", wire.count())
	}
	warmState := warmVendor.Snapshot()
	if string(warmState.Libraries) == "{}" || string(warmState.LibraryPaths) == "{}" {
		t.Fatal("real warm library cache absent")
	}
	sealedAfterWarm, _ := readCache(a)
	if !bytes.Equal(sealedWarm, sealedAfterWarm) {
		t.Fatal("refresh unexpectedly rewrote session")
	}
	warmAssembly := a.originalMediaServerAssembly
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	if warmAssembly.current(context.Background()) == nil || wire.count() != 7 {
		t.Fatal("App stop kept current or issued logout")
	}
	cold := open()
	sealedBeforeStart, _ := readCache(cold)
	if !bytes.Equal(sealedWarm, sealedBeforeStart) {
		t.Fatal("cold construction changed sealed session before start")
	}
	cold.Start(context.Background())
	coldVendor := vendorFor(cold)
	coldWorker := workerFor(cold)
	if coldVendor == warmVendor || wire.count() != 8 {
		t.Fatal("cold must restore session with only current-user", wire.count())
	}
	if state := coldVendor.Snapshot(); string(state.Libraries) != "{}" || string(state.LibraryPaths) != "{}" {
		t.Fatal("cold wrapper reused warm cache object")
	}
	coldRoot := refresh(coldWorker, map[string]any{"name": "ug-owned", "root": true})
	coldItems := refresh(coldWorker, map[string]any{"reference": coldRoot.Instance.Reference, "items": []plugins.MediaServerRefreshItem{item}, "options": plugins.MediaServerRefreshOptions{ScanMode: json.RawMessage(`3`)}})
	if coldRoot.PID == root.PID || coldItems.PID != coldRoot.PID || coldRoot.Instance.Reference == root.Instance.Reference || wire.count() != 12 {
		t.Fatal("cold actual SDK/session/refresh closure", wire.count())
	}
	sealedCold, _ := readCache(cold)
	if !bytes.Equal(sealedWarm, sealedCold) {
		t.Fatal("successful restore/refresh resaved the original session")
	}
	if indexes.Load() != 1 || downloads.Load() != 1 {
		t.Fatal("cold reinstalled owned artifact", indexes.Load(), downloads.Load())
	}
	if err = cold.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if len(wire.calls) != 12 || len(wire.faults) != 0 {
		t.Fatal("bounded real UG requests", len(wire.calls), wire.faults)
	}
	t.Logf("actual App generations=2 workers=%d,%d Ugreen TLS requests=12 artifact GETs=2; sealed nonempty cache preserved and cold imported", root.PID, coldRoot.PID)
}

// A separate owned SQLite database isolates the source SYNC-cache failure
// ordering from the twelve actual App/TLS requests. No fake Store/driver and
// no fault on the main App, all records, or any user database is used.
func ownedUgreenHostStoreFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var active *Store
	t.Cleanup(func() {
		if active != nil {
			_ = active.Close()
		}
	})
	open := func() (*Store, *PluginStore, *originalUgreenSessionStore) {
		store, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		active = store
		plugin, err := NewPluginStore(ctx, store)
		if err != nil {
			t.Fatal(err)
		}
		cache, err := newOriginalUgreenSessionStore(ctx, plugin)
		if err != nil {
			t.Fatal(err)
		}
		return store, plugin, cache
	}
	store, _, cache := open()
	oldValue := json.RawMessage(`{"owned":{"token":"owned-old"}}`)
	newValue := json.RawMessage(`{"owned":{"token":"owned-new"}}`)
	if err := cache.Set(ctx, oldValue); err != nil {
		t.Fatal(err)
	}
	readDisk := func(db *Store) []byte {
		var payload []byte
		if err := db.DB.QueryRow(`SELECT payload FROM records WHERE kind=? AND id=?`, originalUgreenSessionKind, originalUgreenSessionKey).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(payload, []byte("owned-old")) || bytes.Contains(payload, []byte("owned-new")) {
			t.Fatal("fault fixture row not sealed")
		}
		return payload
	}
	sealedOld := readDisk(store)
	if _, err := store.DB.Exec(`CREATE TRIGGER owned_ugreen_cache_abort BEFORE UPDATE ON records WHEN OLD.kind='source_ugreen_sessions_v1' AND OLD.id='UgreenSessionCache' BEGIN SELECT RAISE(ABORT,'owned Ugreen cache abort'); END`); err != nil {
		t.Fatal(err)
	}
	err := cache.Set(ctx, newValue)
	source, ok := err.(*mediaserverruntime.SourceError)
	if !ok || source == nil || source.Class != "UgreenSessionDatabaseError" {
		t.Fatal("real SQL failure was not returned as source ordinary error")
	}
	driver, ok := source.Cause.(interface{ Code() int })
	if !ok || driver.Code() != 1811 {
		t.Fatal("not actual owned SQLite trigger ABORT")
	}
	hot, err := cache.Get(ctx)
	if err != nil || !sharedSystemConfigEqual(hot, newValue) || !bytes.Equal(readDisk(store), sealedOld) {
		t.Fatal("sync hot-first/old-disk ordering")
	}
	if len(hot) == 0 {
		t.Fatal("hot snapshot absent")
	}
	hot[0] = '!'
	detached, err := cache.Get(ctx)
	if err != nil || !sharedSystemConfigEqual(detached, newValue) {
		t.Fatal("cache Get did not detach")
	}
	var before, after int64
	if err = store.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = cache.Set(ctx, newValue); err != nil {
		t.Fatal("equal retry must return success", err)
	}
	if err = store.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before || !bytes.Equal(readDisk(store), sealedOld) {
		t.Fatal("equal hot retry incorrectly repaired old disk")
	}
	// The rejecting trigger remains active throughout the successful equal retry:
	// another UPDATE would still fail. Reads/transaction rollback are not zero SQL.
	if _, err = store.DB.Exec(`DROP TRIGGER owned_ugreen_cache_abort`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	coldStore, _, cold := open()
	if cold == cache {
		t.Fatal("new private cache object required")
	}
	value, err := cold.Get(ctx)
	if err != nil || !sharedSystemConfigEqual(value, oldValue) || !bytes.Equal(readDisk(coldStore), sealedOld) {
		t.Fatal("same-database cold reconstruction must read old sealed value")
	}
	if err = coldStore.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
}
