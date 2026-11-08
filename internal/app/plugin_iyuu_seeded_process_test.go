package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/sites"
)

const (
	seededIYUUAPI     = "2025.iyuu.cn"
	seededIYUUTracker = "hhanclub.net"
	seededIYUUCA      = "/etc/ssl/certs/ca-certificates.crt"
	seededIYUUToken   = "owned-seeded-iyuu-token"
)

// The Linux test uses the production worker and host. Its controlled peer
// inputs exercise the installed 2.22 auto_seed and check_recheck_service
// call order, the pinned helper's API request envelope, and TorrentHelper's
// 429 classification. No download daemon or tracker receives these requests.
func TestIYUUSeededLinuxSourceWorker(t *testing.T) {
	worker := os.Getenv("SOURCE_IYUU_WORKER")
	if worker == "" || os.Getenv("SOURCE_IYUU_SEEDED_CA") != seededIYUUCA || runtime.GOOS != "linux" {
		t.Skip("requires the Linux worker, an isolated loopback network and a writable synthetic trust bundle")
	}
	for _, host := range []string{seededIYUUAPI, seededIYUUTracker} {
		addresses, err := net.DefaultResolver.LookupIPAddr(t.Context(), host)
		if err != nil || len(addresses) == 0 {
			t.Fatal("owned fixture host did not resolve", host, err)
		}
		for _, address := range addresses {
			if !address.IP.IsLoopback() {
				t.Fatal("owned fixture host must resolve exclusively to loopback", host)
			}
		}
	}

	fixture := newSeededIYUUPeer(t, 24)
	fixture.startTLS(t)
	qb := httptest.NewServer(http.HandlerFunc(fixture.qb))
	t.Cleanup(qb.Close)
	app := newTestApp(t)
	cookie := setupTestAdmin(t, app)
	ctx := t.Context()
	settings := app.Settings()
	settings.Sites = []SiteConfig{{
		Config:  sites.Config{ID: "owned-tracker", Name: "Owned tracker", BaseURL: "https://" + seededIYUUTracker, Cookie: "owned-tracker=proof"},
		Enabled: true,
	}}
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{
		ID: "owned-qb", Kind: "qbittorrent", BaseURL: qb.URL, Username: "owned", Password: "owned",
	}, Name: "Owned qB"}}
	if err := app.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	manifest := installOwnedResourceProcessWorker(t, app, cookie, worker)
	if manifest.PluginID != "IYUUAutoSeed" || manifest.Version != "2.22" {
		t.Fatal("unexpected worker identity")
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	path := "/api/plugins/" + manifest.PluginID
	oldSuccess := seededIYUUCache(10000)
	oldRetryable := seededIYUUCache(20000)
	oldPermanent := seededIYUUCache(30000)
	config, err := json.Marshal(map[string]any{
		"enabled": true, "cron": "13 3 * * mon", "token": seededIYUUToken, "downloaders": []string{"Owned qB"},
		"onlyonce": true, "skipverify": false, "labelsafterseed": "owned-label",
		"success_caches": oldSuccess, "error_caches": oldRetryable, "permanent_error_caches": oldPermanent,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+path, bytes.NewReader(config))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	result, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(result.Body)
	result.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	assertOriginalRegistrySuccess(t, result.StatusCode, body)

	deadline := time.Now().Add(20 * time.Second)
	var persisted struct {
		OnlyOnce  bool     `json:"onlyonce"`
		Success   []string `json:"success_caches"`
		Retryable []string `json:"error_caches"`
		Permanent []string `json:"permanent_error_caches"`
	}
	for time.Now().Before(deadline) {
		stored, err := app.pluginStore.Config(ctx, manifest.PluginID)
		if err == nil && json.Unmarshal(stored.Value, &persisted) == nil &&
			len(persisted.Success) == len(oldSuccess) && persisted.Success[len(oldSuccess)-1] == fixture.addedHash &&
			len(persisted.Retryable) == len(oldRetryable) && persisted.Retryable[len(oldRetryable)-1] == fixture.failedHash {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	trace := fixture.snapshot()
	logs, logErr := app.queryLogs(ctx, map[string]string{
		"plugin_instance_id": manifest.PluginID, "from": "0001-01-01T00:00:00Z", "limit": "500",
	})
	if persisted.OnlyOnce || !reflect.DeepEqual(persisted.Success, append(oldSuccess[1:], fixture.addedHash)) ||
		!reflect.DeepEqual(persisted.Retryable, append(oldRetryable[1:], fixture.failedHash)) || !reflect.DeepEqual(persisted.Permanent, oldPermanent) {
		messages := []any{}
		for _, row := range logs.Entries {
			if row["event"] == "plugin_message" {
				messages = append(messages, row["message"])
			}
		}
		t.Fatalf("source success/transient failure caches diverged: onlyonce=%t sizes=%d/%d/%d, peer=%+v, plugin log=%v, log error=%v",
			persisted.OnlyOnce, len(persisted.Success), len(persisted.Retryable), len(persisted.Permanent), trace, messages, logErr)
	}
	expectedWrites := []string{"add", "removeTags", "deleteTags", "recheck", "start"}
	if !reflect.DeepEqual(trace.Writes, expectedWrites) || trace.Completed != 1 || trace.Existing != len(fixture.existing) ||
		!reflect.DeepEqual(trace.API, []string{"/reseed/sites/index", "/reseed/sites/reportExisting", "/reseed/index/index"}) ||
		!reflect.DeepEqual(trace.Tracker, []string{"42", "43"}) {
		t.Fatalf("seeded source peer call order or once-only effects diverged: %+v", trace)
	}
	history, err := app.pluginStore.Data(ctx, manifest.PluginID, fixture.sourceHash)
	var rows []struct {
		Downloader string
		Torrents   []string
	}
	if err != nil || json.Unmarshal(history.Value, &rows) != nil || len(rows) != 1 ||
		rows[0].Downloader != "Owned qB" || !reflect.DeepEqual(rows[0].Torrents, []string{fixture.addedHash}) {
		t.Fatal("source success history was not durably written to the initial hash", err)
	}
	if logErr != nil {
		t.Fatal(logErr)
	}
	callIDs := map[string]bool{}
	for _, row := range logs.Entries {
		if row["event"] == "plugin_message" && row["operation"] != "init" {
			if id, _ := row["plugin_call_id"].(string); id != "" {
				callIDs[id] = true
			}
		}
	}
	if len(callIDs) < 2 {
		t.Fatal("the owned seeded pass did not renew its host call scope")
	}
	t.Logf("actual Linux host/worker against owned peers: %d existing lookups; late %v once each; tracker 429 transient cache and one durable success history; three 10k cache lists kept; %d observed log call IDs",
		trace.Existing, trace.Writes, len(callIDs))
}

func seededIYUUCache(offset int) []string {
	out := make([]string, 10000)
	for i := range out {
		out[i] = fmt.Sprintf("%040x", offset+i)
	}
	return out
}

type seededIYUUTrace struct {
	API, Tracker, Writes []string
	Completed, Existing  int
}

type seededIYUUPeer struct {
	t *testing.T

	sourceHash, addedHash, failedHash string
	torrent                           []byte
	existing                          map[string]bool
	candidates                        []map[string]any

	mu       sync.Mutex
	trace    seededIYUUTrace
	tag      string
	addState string
}

func newSeededIYUUPeer(t *testing.T, existing int) *seededIYUUPeer {
	t.Helper()
	info := "d6:lengthi1e4:name8:demo.mkv12:piece lengthi16384e6:pieces20:01234567890123456789e"
	tracker := "https://" + seededIYUUTracker + "/announce"
	digest := sha1.Sum([]byte(info))
	p := &seededIYUUPeer{
		t: t, sourceHash: fmt.Sprintf("%040x", 100), failedHash: fmt.Sprintf("%040x", 200),
		addedHash: hex.EncodeToString(digest[:]), existing: map[string]bool{},
		torrent: []byte(fmt.Sprintf("d8:announce%d:%s4:info%se", len(tracker), tracker, info)),
	}
	for i := 1; i <= existing; i++ {
		hash := fmt.Sprintf("%040x", i)
		p.existing[hash] = true
		p.candidates = append(p.candidates, map[string]any{"sid": 1, "torrent_id": i, "info_hash": hash})
	}
	p.candidates = append(p.candidates,
		map[string]any{"sid": 1, "torrent_id": 42, "info_hash": p.addedHash},
		map[string]any{"sid": 1, "torrent_id": 43, "info_hash": p.failedHash})
	return p
}

func (p *seededIYUUPeer) startTLS(t *testing.T) {
	t.Helper()
	file, err := os.Stat(seededIYUUCA)
	if err != nil || !file.Mode().IsRegular() || file.Size() != 0 {
		t.Fatal("mount a fresh empty file over the Linux test trust bundle")
	}
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	root := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Owned IYUU test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, &root, &root, rootPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		DNSNames:    []string{seededIYUUAPI, seededIYUUTracker},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &root, public, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seededIYUUCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:443")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(p.https), TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}},
	}}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = server.Close() })
}

func (p *seededIYUUPeer) https(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.Host {
	case seededIYUUAPI:
		p.trace.API = append(p.trace.API, r.URL.Path)
		if r.Header.Get("Token") != seededIYUUToken {
			p.t.Error("source IYUU header missing")
		}
		var data any
		switch r.URL.Path {
		case "/reseed/sites/index":
			if r.Method != http.MethodGet {
				p.t.Error("site index did not use GET")
			}
			data = map[string]any{"sites": []map[string]any{{"id": 1, "base_url": "https://" + seededIYUUTracker, "download_page": "/download.php?id={}"}}}
		case "/reseed/sites/reportExisting":
			var input struct {
				IDs []int `json:"sid_list"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil || !reflect.DeepEqual(input.IDs, []int{1}) {
				p.t.Error("source site report envelope diverged")
			}
			data = map[string]any{"sid_sha1": "synthetic-site-report"}
		case "/reseed/index/index":
			var input struct {
				Hash, SHA1, Version string
				SiteHash            string `json:"sid_sha1"`
			}
			hashes := fmt.Sprintf(`["%s"]`, p.sourceHash)
			digest := sha1.Sum([]byte(hashes))
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil ||
				input.Hash != hashes || input.SHA1 != hex.EncodeToString(digest[:]) || input.Version != "8.2.0" || input.SiteHash != "synthetic-site-report" {
				p.t.Error("source reseed index envelope diverged")
			}
			data = map[string]any{p.sourceHash: map[string]any{"torrent": p.candidates}}
		default:
			p.t.Error("unexpected owned IYUU API request", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	case seededIYUUTracker:
		id := r.URL.Query().Get("id")
		p.trace.Tracker = append(p.trace.Tracker, id)
		if r.Method != http.MethodGet || r.URL.Path != "/download.php" || r.URL.Query().Get("https") != "1" ||
			r.Header.Get("Cookie") != "owned-tracker=proof" || r.Header.Get("Token") != "" {
			p.t.Error("source tracker request lost its bound cookie or URL")
		}
		switch id {
		case "42":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(p.torrent)
		case "43":
			http.Error(w, "synthetic rate limit", http.StatusTooManyRequests)
		default:
			p.t.Error("unexpected owned tracker material lookup")
			http.NotFound(w, r)
		}
	default:
		p.t.Error("unexpected owned TLS virtual host")
		http.NotFound(w, r)
	}
}

func (p *seededIYUUPeer) qb(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	const prefix = "/api/v2/"
	route := strings.TrimPrefix(r.URL.Path, prefix)
	if route == "auth/login" {
		http.SetCookie(w, &http.Cookie{Name: "owned-qb-session", Value: "proof", Path: "/"})
		_, _ = io.WriteString(w, "Ok.")
		return
	}
	if _, err := r.Cookie("owned-qb-session"); err != nil {
		p.t.Error("source downloader lost its session cookie")
	}
	if route == "app/webapiVersion" {
		_, _ = io.WriteString(w, "2.11.4")
		return
	}
	if route == "torrents/add" {
		p.trace.Writes = append(p.trace.Writes, "add")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			p.t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("torrent__0")
		if err != nil {
			p.t.Error("paused addition missing metainfo", err)
			return
		}
		actual, readErr := io.ReadAll(file)
		file.Close()
		tags := strings.Split(r.FormValue("tags"), ",")
		if readErr != nil || !bytes.Equal(actual, p.torrent) || len(tags) != 2 || tags[0] != "owned-label" ||
			len(tags[1]) != 10 || r.FormValue("paused") != "True" || r.FormValue("stopped") != "True" ||
			r.FormValue("skip_checking") != "False" || r.FormValue("savepath") != "/owned/source" {
			p.t.Error("source paused addition did not preserve material, directory, tags and checking options")
		}
		if len(tags) == 2 {
			p.tag = tags[1]
		}
		p.addState = "stoppedDL"
		_, _ = io.WriteString(w, "Ok.")
		return
	}
	if err := r.ParseForm(); err != nil {
		p.t.Error(err)
		return
	}
	hashes := r.Form.Get("hashes")
	switch route {
	case "torrents/info":
		rows := []map[string]any{}
		switch {
		case r.Form.Get("filter") == "completed":
			p.trace.Completed++
			rows = append(rows, seededIYUUTorrent(p.sourceHash, "uploading", "source", 1))
		case p.existing[hashes]:
			p.trace.Existing++
			rows = append(rows, seededIYUUTorrent(hashes, "stoppedDL", "", 0))
		case p.addState != "" && (hashes == "" || hashes == p.addedHash):
			progress := 0
			if p.addState != "stoppedDL" {
				progress = 1
			}
			rows = append(rows, seededIYUUTorrent(p.addedHash, p.addState, "owned-label,"+p.tag, progress))
		}
		_ = json.NewEncoder(w).Encode(rows)
	case "torrents/removeTags", "torrents/deleteTags":
		action := strings.TrimPrefix(route, "torrents/")
		p.trace.Writes = append(p.trace.Writes, action)
		if r.Form.Get("tags") != p.tag || action == "removeTags" && hashes != p.addedHash || action == "deleteTags" && hashes != "" {
			p.t.Error("source tag cleanup changed the selected task or tag")
		}
	case "torrents/recheck":
		p.trace.Writes = append(p.trace.Writes, "recheck")
		if hashes != p.addedHash || p.addState != "stoppedDL" {
			p.t.Error("source recheck did not follow the paused add")
		}
		p.addState = "stoppedUP"
	case "torrents/start":
		p.trace.Writes = append(p.trace.Writes, "start")
		if hashes != p.addedHash || p.addState != "stoppedUP" {
			p.t.Error("source start was not gated by the ready checked state")
		}
		p.addState = "uploading"
	default:
		p.t.Error("unexpected owned qB operation", route)
		http.NotFound(w, r)
	}
}

func seededIYUUTorrent(hash, state, tags string, progress int) map[string]any {
	return map[string]any{
		"hash": hash, "state": state, "tags": tags, "save_path": "/owned/source", "category": "owned",
		"progress": progress, "amount_left": 0, "availability": 1, "total_size": 1, "completed": 1,
	}
}

func (p *seededIYUUPeer) snapshot() seededIYUUTrace {
	p.mu.Lock()
	defer p.mu.Unlock()
	trace := p.trace
	trace.API = append([]string(nil), trace.API...)
	trace.Tracker = append([]string(nil), trace.Tracker...)
	trace.Writes = append([]string(nil), trace.Writes...)
	return trace
}
