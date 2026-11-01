package app

// Actual installed-worker coverage. The coordinator supplies an already-built
// owned fixture; these tests never build, set environment, proxy, or CA policy.
// Synthetic peers implement fixed native protocols, not a replacement HostCaller.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

const (
	ownedCommandWorkerID  = "OwnedDownloaderCommands"
	ownedCommandQBID      = "owned-command-qb"
	ownedCommandTRID      = "owned-command-tr"
	ownedCommandHash      = "0123456789012345678901234567890123456789"
	ownedCommandMagnet    = "magnet:?xt=urn:btih:" + ownedCommandHash
	ownedCommandDirectory = "/synthetic/no-real-download"
	ownedCommandUsername  = "owned-synthetic-user"
	ownedCommandPassword  = "owned-synthetic-password"
	ownedCommandSession   = "owned-synthetic-session"
)

type ownedCommandWorkerCalls struct {
	Total, Begin, Append, Submit, Abort, Invoke, Other, Succeeded, Failed int
	InputJSONBytes                                                        int64 `json:"input_json_bytes"`
	OutputJSONBytes                                                       int64 `json:"output_json_bytes"`
	AppendPayloadBytes                                                    int64 `json:"append_payload_bytes"`
	MaxInputJSONBytes                                                     int   `json:"max_input_json_bytes"`
}

type ownedCommandWorkerAttempt struct {
	Index        int
	Kind, Op     string
	DownloaderID string `json:"downloader_id"`
	OK           bool
	Result       json.RawMessage
	ResultKind   string `json:"result_kind"`
	SourceTruthy *bool  `json:"source_truthy"`
	ErrorCode    string `json:"error_code"`
	Generated    *struct {
		Kind   string
		Length int
		SHA256 string
	}
	HostCalls ownedCommandWorkerCalls `json:"host_calls"`
}

type ownedCommandWorkerResult struct {
	PID, Invocation, Requested, Completed, Succeeded, Failed int
	InitCount                                                int  `json:"init_count"`
	HadPrevious                                              bool `json:"had_previous"`
	OK                                                       bool
	Interrupted                                              string
	Results                                                  []ownedCommandWorkerAttempt
	PreviousProbe                                            *struct {
		Label                 string
		Available             bool
		SourceInvocation      int `json:"source_invocation"`
		Kind, Operation       string
		PrivateFrameOperation string `json:"private_frame_operation"`
		CallbackAttempts      int    `json:"callback_attempts"`
		InputJSONBytes        int    `json:"input_json_bytes"`
		OutputJSONBytes       int    `json:"output_json_bytes"`
		Denied                *bool
		ErrorCode             string `json:"error_code"`
	} `json:"previous_probe"`
}

type ownedCommandPeerObservation struct {
	Requests, Unexpected, FailedSelected int
	Counts                               map[string]int
	Selected                             []string
	MultipartFiles                       int
	MultipartBytes                       int64
	MultipartSHA256                      string
}

type ownedCommandPeers struct {
	mu              sync.Mutex
	qb, tr          *httptest.Server
	version         string
	failNextRecheck bool
	falseNextAdd    bool
	observation     ownedCommandPeerObservation
}

func (p *ownedCommandPeers) snapshot() ownedCommandPeerObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.observation
	out.Counts = map[string]int{}
	for key, count := range p.observation.Counts {
		out.Counts[key] = count
	}
	out.Selected = append([]string{}, p.observation.Selected...)
	return out
}

func (p *ownedCommandPeers) reject(w http.ResponseWriter) {
	p.observation.Unexpected++
	http.Error(w, "unexpected owned protocol request", http.StatusBadRequest)
}

func (p *ownedCommandPeers) qbHandler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.observation.Requests++
	p.observation.Counts["qb:"+r.URL.Path]++
	r.Body = http.MaxBytesReader(w, r.Body, 24<<20)
	if r.URL.RawQuery != "" {
		p.reject(w)
		return
	}
	if r.URL.Path == "/api/v2/auth/login" {
		if r.Method != "POST" || r.ParseForm() != nil || r.PostForm.Get("username") != ownedCommandUsername || r.PostForm.Get("password") != ownedCommandPassword || len(r.PostForm) != 2 {
			p.reject(w)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: ownedCommandSession, Path: "/"})
		_, _ = io.WriteString(w, "Ok.")
		return
	}
	cookie, err := r.Cookie("SID")
	if err != nil || cookie.Value != ownedCommandSession {
		p.reject(w)
		return
	}
	if r.URL.Path == "/api/v2/app/webapiVersion" {
		if r.Method != "GET" {
			p.reject(w)
			return
		}
		_, _ = io.WriteString(w, p.version)
		return
	}
	if r.Method != "POST" {
		p.reject(w)
		return
	}
	switch r.URL.Path {
	case "/api/v2/torrents/add":
		if !p.qbAdd(w, r) {
			return
		}
		p.observation.Selected = append(p.observation.Selected, "qb:add")
		if p.falseNextAdd {
			p.falseNextAdd = false
			_, _ = io.WriteString(w, "Fails.")
		} else {
			_, _ = io.WriteString(w, `{"added_torrent_ids":[9007199254740993]}`)
		}
	case "/api/v2/torrents/start", "/api/v2/torrents/recheck", "/api/v2/torrents/delete":
		if r.ParseForm() != nil || r.PostForm.Get("hashes") != ownedCommandHash {
			p.reject(w)
			return
		}
		operation := strings.TrimPrefix(r.URL.Path, "/api/v2/torrents/")
		wantFields := 1
		if operation == "delete" {
			wantFields = 2
			if r.PostForm.Get("deleteFiles") != "False" {
				p.reject(w)
				return
			}
		}
		if len(r.PostForm) != wantFields {
			p.reject(w)
			return
		}
		p.observation.Selected = append(p.observation.Selected, "qb:"+operation)
		if operation == "recheck" && p.failNextRecheck {
			p.failNextRecheck = false // The next distinct operation sees repaired peer state.
			p.observation.FailedSelected++
			http.Error(w, "owned selected failure", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "Ok.")
	default:
		p.reject(w)
	}
}

// Stream synthetic multipart material into a digest. Never persist or log it.
func (p *ownedCommandPeers) qbAdd(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
		reader, err := r.MultipartReader()
		if err != nil {
			p.reject(w)
			return false
		}
		fields := map[string]string{}
		files := 0
		var count int64
		var digest string
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				p.reject(w)
				return false
			}
			if part.FileName() != "" {
				files++
				if part.FormName() != "torrent__0" || part.FileName() != "torrent__0" || part.Header.Get("Content-Type") != "" {
					_ = part.Close()
					p.reject(w)
					return false
				}
				hash := sha256.New()
				count, err = io.Copy(hash, io.LimitReader(part, (16<<20)+1))
				digest = hex.EncodeToString(hash.Sum(nil))
				if err != nil || count > 16<<20 {
					_ = part.Close()
					p.reject(w)
					return false
				}
			} else {
				value, err := io.ReadAll(io.LimitReader(part, 4097))
				_, duplicate := fields[part.FormName()]
				if err != nil || len(value) > 4096 || duplicate {
					_ = part.Close()
					p.reject(w)
					return false
				}
				fields[part.FormName()] = string(value)
			}
			if part.Close() != nil {
				p.reject(w)
				return false
			}
		}
		want := map[string]string{"autoTMM": "False", "sequentialDownload": "False", "firstLastPiecePrio": "False"}
		if files != 1 || !reflect.DeepEqual(fields, want) {
			p.reject(w)
			return false
		}
		p.observation.MultipartFiles, p.observation.MultipartBytes, p.observation.MultipartSHA256 = files, count, digest
		return true
	}
	if r.ParseForm() != nil {
		p.reject(w)
		return false
	}
	want := map[string][]string{
		"urls": {ownedCommandMagnet}, "autoTMM": {"False"}, "sequentialDownload": {"False"}, "firstLastPiecePrio": {"False"},
		"savepath": {ownedCommandDirectory}, "tags": {"owned-tag"}, "paused": {"True"}, "stopped": {"True"},
	}
	if !reflect.DeepEqual(map[string][]string(r.PostForm), want) {
		p.reject(w)
		return false
	}
	return true
}

func (p *ownedCommandPeers) trHandler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.observation.Requests++
	r.Body = http.MaxBytesReader(w, r.Body, 24<<20)
	username, password, ok := r.BasicAuth()
	if r.Method != "POST" || r.URL.Path != "/transmission/rpc" || r.URL.RawQuery != "" || !ok || username != ownedCommandUsername || password != ownedCommandPassword {
		p.reject(w)
		return
	}
	var request struct {
		Method    string         `json:"method"`
		Arguments map[string]any `json:"arguments"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Arguments == nil {
		p.reject(w)
		return
	}
	p.observation.Counts["tr:"+request.Method]++
	switch request.Method {
	case "session-get", "torrent-add", "torrent-start", "torrent-verify", "torrent-remove":
	default:
		p.reject(w)
		return
	}
	if r.Header.Get("X-Transmission-Session-Id") != ownedCommandSession {
		if request.Method != "session-get" || len(request.Arguments) != 0 {
			p.reject(w)
			return
		}
		w.Header().Set("X-Transmission-Session-Id", ownedCommandSession)
		w.WriteHeader(http.StatusConflict)
		return
	}
	if request.Method == "session-get" {
		if len(request.Arguments) != 0 {
			p.reject(w)
			return
		}
		_, _ = io.WriteString(w, `{"result":"success","arguments":{"rpc-version":17}}`)
		return
	}
	if request.Method == "torrent-add" {
		want := map[string]any{"filename": ownedCommandMagnet, "download-dir": ownedCommandDirectory, "paused": true, "labels": []any{"owned-tag"}}
		if !reflect.DeepEqual(request.Arguments, want) {
			p.reject(w)
			return
		}
		p.observation.Selected = append(p.observation.Selected, "tr:torrent-add")
		_, _ = io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"id":9007199254740993,"hashString":"`+ownedCommandHash+`","name":"owned synthetic torrent","extra":{"falseValue":false,"empty":[],"large":9007199254740995}}}}`)
		return
	}
	want := map[string]any{"ids": []any{json.Number("101")}}
	if request.Method == "torrent-remove" {
		want["delete-local-data"] = false
	}
	if !reflect.DeepEqual(request.Arguments, want) {
		p.reject(w)
		return
	}
	p.observation.Selected = append(p.observation.Selected, "tr:"+request.Method)
	_, _ = io.WriteString(w, `{"result":"success","arguments":{}}`)
}

// Bind only the explicitly owned IPv4 endpoint before Start; fail rather than
// probing IPv6 or changing a route after any denial.
func ownedCommandTCP4Unstarted(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
}
func ownedCommandTCP4Peer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	peer := ownedCommandTCP4Unstarted(t, handler)
	peer.Start()
	return peer
}

func newOwnedCommandPeers(t *testing.T) *ownedCommandPeers {
	t.Helper()
	p := &ownedCommandPeers{version: "2.11.0", observation: ownedCommandPeerObservation{Counts: map[string]int{}}}
	p.qb = ownedCommandTCP4Peer(t, http.HandlerFunc(p.qbHandler))
	p.tr = ownedCommandTCP4Peer(t, http.HandlerFunc(p.trHandler))
	t.Cleanup(func() {
		p.qb.Close()
		p.tr.Close()
		if p.snapshot().Unexpected != 0 {
			t.Error("unexpected owned native protocol requests")
		}
	})
	return p
}

func ownedCommandWorkerInstall(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	index, payload := originalMarketOwnedIndex(t, "OWNED_DOWNLOADER_COMMAND_WORKER")
	manifest := index.Releases[0].Manifest
	wantCaps := []string{plugins.QBittorrentAddCapability, plugins.QBittorrentStartCapability, plugins.QBittorrentRecheckCapability, plugins.QBittorrentDeleteKeepFilesCapability, plugins.TransmissionAddCapability, plugins.TransmissionStartCapability, plugins.TransmissionRecheckCapability, plugins.TransmissionDeleteKeepFilesCapability}
	if manifest.Validate() != nil || manifest.PluginID != ownedCommandWorkerID || manifest.Runtime != "go-executable" || !reflect.DeepEqual(manifest.Capabilities, wantCaps) || len(manifest.APIs) != 1 || manifest.APIs[0].Key != "commands" || manifest.APIs[0].Path != "/commands" || manifest.APIs[0].Auth != "bear" || manifest.APIs[0].Body != "json" || !manifest.APIs[0].BodyRequired || !reflect.DeepEqual(manifest.APIs[0].Methods, []string{"POST"}) {
		t.Fatal("actual command worker manifest differs")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits, unexpected atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			unexpected.Add(1)
			http.Error(w, "owned read-only artifact", 405)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	})
	peer := ownedCommandTCP4Unstarted(t, handler)
	index.Releases[0].Artifacts[0].URL = "http://" + peer.Listener.Addr().String() + "/worker"
	peer.Start()
	t.Cleanup(func() {
		peer.Close()
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Errorf("owned artifact request counts %d/%d/%d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
		}
	})
	body, _ := json.Marshal(map[string]string{"plugin_id": ownedCommandWorkerID, "repo_url": peer.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	_ = ownedCommandWorkerObject(t, a)
	installation, err := a.pluginStore.Installation(context.Background(), ownedCommandWorkerID)
	if err != nil || !installation.Installed {
		t.Fatal("actual command worker SQLite installation absent")
	}
}

func ownedCommandWorkerObject(t *testing.T, a *App) *originalPluginInstance {
	t.Helper()
	loaded, ok := a.pluginRunningInstances.Load(ownedCommandWorkerID)
	if !ok {
		t.Fatal("actual command worker not loaded")
	}
	object, ok := loaded.(*originalPluginInstance)
	if !ok || !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("not an ordinary resources-contract external worker")
	}
	return object
}

func ownedCommandWorkerFixture(t *testing.T, p *ownedCommandPeers) (*App, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	cfg := a.Settings()
	cfg.PluginMarkets, cfg.PluginLocalRepoPaths = []string{}, []string{}
	cfg.PluginAutoReload = false
	cfg.AllowedRoots, cfg.Roots = []string{t.TempDir()}, []Root{}
	enabled := true
	cfg.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: ownedCommandQBID, Kind: "qb", BaseURL: p.qb.URL, Username: ownedCommandUsername, Password: ownedCommandPassword}, SharedPaths: true, Enabled: &enabled},
		{Config: download.Config{ID: ownedCommandTRID, Kind: "transmission", BaseURL: p.tr.URL + "/transmission/rpc", Username: ownedCommandUsername, Password: ownedCommandPassword}, SharedPaths: true, Enabled: &enabled},
	}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal("save owned command fixture settings failed")
	}
	cookie := setupTestAdmin(t, a)
	ownedCommandWorkerInstall(t, a, cookie)
	t.Cleanup(a.closeOriginalSessions)
	return a, cookie
}

func ownedCommandOperation(kind, operation string) map[string]any {
	id, ids := ownedCommandQBID, any([]string{ownedCommandHash})
	if kind == "transmission" {
		id, ids = ownedCommandTRID, []int{101}
	}
	out := map[string]any{"kind": kind, "op": operation, "downloader_id": id}
	if operation == "add" {
		labelKey := "tag"
		if kind == "transmission" {
			labelKey = "labels"
		}
		out["args"] = map[string]any{"content": ownedCommandMagnet, "download_dir": ownedCommandDirectory, "is_paused": true, labelKey: []string{"owned-tag"}}
	} else {
		out["ids"] = ids
	}
	return out
}

func ownedCommandWorkerInput(t *testing.T, probe bool, operations ...map[string]any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"operations": operations, "continue_on_error": true, "probe_previous": probe})
	if err != nil {
		t.Fatal("marshal owned command input failed")
	}
	return body
}

func ownedCommandWorkerDecode(t *testing.T, raw json.RawMessage, requested int) ownedCommandWorkerResult {
	t.Helper()
	for _, forbidden := range []string{ownedCommandUsername, ownedCommandPassword, ownedCommandSession, ownedCommandMagnet, ownedCommandDirectory, `"data":`, `"handle":`} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("fixture report exposed input material, handle or credentials")
		}
	}
	var out ownedCommandWorkerResult
	if json.Unmarshal(raw, &out) != nil || out.PID <= 0 || out.PID == os.Getpid() || out.InitCount < 1 || out.Requested != requested || out.Completed != requested || len(out.Results) != requested || out.Interrupted != "" {
		t.Fatal("actual command worker observation incomplete")
	}
	for index, result := range out.Results {
		if !(result.Kind == "qbittorrent" && result.DownloaderID == ownedCommandQBID || result.Kind == "transmission" && result.DownloaderID == ownedCommandTRID) {
			t.Fatal("actual worker result selected a non-owned downloader")
		}
		if result.Index != index || result.HostCalls.Total != result.HostCalls.Begin+result.HostCalls.Append+result.HostCalls.Submit+result.HostCalls.Abort+result.HostCalls.Invoke+result.HostCalls.Other || result.HostCalls.Total != result.HostCalls.Succeeded+result.HostCalls.Failed {
			t.Fatal("callback observation counts inconsistent")
		}
	}
	return out
}

func ownedCommandWorkerQuery(t *testing.T, a *App, cookie *http.Cookie, probe bool, operations ...map[string]any) ownedCommandWorkerResult {
	t.Helper()
	body := ownedCommandWorkerInput(t, probe, operations...)
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+ownedCommandWorkerID+"/commands", string(body), cookie, "")
	if response.Code != http.StatusOK {
		t.Fatalf("actual registered command API HTTP %d", response.Code)
	}
	return ownedCommandWorkerDecode(t, response.Body.Bytes(), len(operations))
}

func ownedCommandWorkerWarm(t *testing.T, before, after ownedCommandWorkerResult) {
	t.Helper()
	if before.PID != after.PID || before.InitCount != after.InitCount || after.Invocation != before.Invocation+1 || !after.HadPrevious {
		t.Fatal("actual worker PID/init/invocation continuity failed")
	}
}

func ownedCommandWorkerProbe(t *testing.T, out ownedCommandWorkerResult, kind, operation, privateOperation string) {
	t.Helper()
	probe := out.PreviousProbe
	if probe == nil || probe.Label != "previous_invocation_host_caller_denied" || !probe.Available || probe.SourceInvocation != out.Invocation-1 || probe.Kind != kind || probe.Operation != operation || probe.PrivateFrameOperation != privateOperation || probe.CallbackAttempts != 1 || probe.Denied == nil || !*probe.Denied || probe.ErrorCode != "invalid_host_call" || probe.InputJSONBytes <= 0 || probe.OutputJSONBytes != 0 {
		t.Fatal("previous known-successful frame was not denied by its expired worker callback")
	}
}

func ownedCommandAssertQB(t *testing.T, attempt ownedCommandWorkerAttempt, success bool, ids []string) {
	t.Helper()
	var result struct {
		Success bool     `json:"success"`
		IDs     []string `json:"ids"`
	}
	if !attempt.OK || attempt.ErrorCode != "" || attempt.ResultKind != "qbittorrent_source_tuple" || attempt.SourceTruthy == nil || !*attempt.SourceTruthy || json.Unmarshal(attempt.Result, &result) != nil || result.Success != success || !reflect.DeepEqual(result.IDs, ids) {
		t.Fatal("qB source tuple result/truth differs")
	}
}

func ownedCommandAssertTR(t *testing.T, attempt ownedCommandWorkerAttempt) {
	t.Helper()
	var value any
	decoder := json.NewDecoder(bytes.NewReader(attempt.Result))
	decoder.UseNumber()
	if !attempt.OK || attempt.ResultKind != "transmission_native_json_not_python_torrent" || attempt.SourceTruthy != nil || decoder.Decode(&value) != nil {
		t.Fatal("TR native JSON result absent or mislabeled")
	}
	want := map[string]any{"id": json.Number("9007199254740993"), "hashString": ownedCommandHash, "name": "owned synthetic torrent", "extra": map[string]any{"falseValue": false, "empty": []any{}, "large": json.Number("9007199254740995")}}
	if !reflect.DeepEqual(value, want) {
		t.Fatal("TR original id/hashString/extra native fields changed")
	}
}

func ownedCommandAssertControl(t *testing.T, attempt ownedCommandWorkerAttempt) {
	t.Helper()
	if !attempt.OK || !bytes.Equal(attempt.Result, []byte("true")) || attempt.ResultKind != "source_bool" || attempt.SourceTruthy != nil || attempt.HostCalls.Total != 1 || attempt.HostCalls.Invoke != 1 || attempt.HostCalls.Failed != 0 {
		t.Fatal("actual SDK control result/callback differs")
	}
}

func ownedCommandPeerDelta(t *testing.T, before, after ownedCommandPeerObservation, requests int, selected ...string) {
	t.Helper()
	if after.Unexpected != 0 || after.Requests-before.Requests != requests || len(after.Selected) < len(before.Selected) || !reflect.DeepEqual(after.Selected[len(before.Selected):], selected) {
		t.Fatalf("unexpected native dispatch delta: requests=%d selected=%d; want %d/%d", after.Requests-before.Requests, len(after.Selected)-len(before.Selected), requests, len(selected))
	}
}

func TestSharedDownloaderCommandsActualWorkerRecoveryAndScopes(t *testing.T) {
	peers := newOwnedCommandPeers(t)
	a, cookie := ownedCommandWorkerFixture(t, peers)
	operations := []map[string]any{}
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, op := range []string{"add", "start", "recheck", "delete_keep_files"} {
			operations = append(operations, ownedCommandOperation(kind, op))
		}
	}
	baseline := peers.snapshot()
	unauthorized := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+ownedCommandWorkerID+"/commands", string(ownedCommandWorkerInput(t, false, operations...)), nil, "")
	if unauthorized.Code != http.StatusUnauthorized || peers.snapshot().Requests != baseline.Requests {
		t.Fatal("unauthenticated API dispatched owned downloader work")
	}
	first := ownedCommandWorkerQuery(t, a, cookie, false, operations...)
	if !first.OK || first.HadPrevious || first.Invocation != 1 || first.Succeeded != 8 || first.Failed != 0 {
		for _, result := range first.Results {
			t.Logf("operation=%s/%s sdk_ok=%v error_code=%s callbacks=%d callback_failures=%d", result.Kind, result.Op, result.OK, result.ErrorCode, result.HostCalls.Total, result.HostCalls.Failed)
		}
		t.Fatal("fresh eight-operation actual worker result failed")
	}
	ownedCommandAssertQB(t, first.Results[0], true, []string{"9007199254740993"})
	ownedCommandAssertTR(t, first.Results[4])
	for index, result := range first.Results {
		if result.HostCalls.Total != 1 || result.HostCalls.Invoke != 1 || result.HostCalls.Failed != 0 {
			t.Fatal("small actual SDK operation did not use exactly one invoke")
		}
		if index != 0 && index != 4 {
			ownedCommandAssertControl(t, result)
		}
	}
	afterFirst := peers.snapshot()
	ownedCommandPeerDelta(t, baseline, afterFirst, 22, "qb:add", "qb:start", "qb:recheck", "qb:delete", "tr:torrent-add", "tr:torrent-start", "tr:torrent-verify", "tr:torrent-remove")
	if afterFirst.Counts["qb:/api/v2/auth/login"] != 4 || afterFirst.Counts["qb:/api/v2/app/webapiVersion"] != 2 || afterFirst.Counts["tr:session-get"] != 8 {
		t.Fatal("normal auth/version/session negotiation changed")
	}

	peers.mu.Lock()
	peers.falseNextAdd = true
	peers.mu.Unlock()
	nullQB := ownedCommandOperation("qbittorrent", "add")
	nullQB["args"] = map[string]any{"content": nil}
	emptyQB := ownedCommandOperation("qbittorrent", "add")
	emptyQB["args"] = map[string]any{}
	emptyQB["synthetic_content"] = map[string]any{"length": 0}
	nullTR := ownedCommandOperation("transmission", "add")
	nullTR["args"] = map[string]any{"content": nil}
	falseResults := ownedCommandWorkerQuery(t, a, cookie, true, ownedCommandOperation("qbittorrent", "add"), nullQB, emptyQB, nullTR)
	ownedCommandWorkerWarm(t, first, falseResults)
	ownedCommandWorkerProbe(t, falseResults, "transmission", "delete_keep_files", "invoke")
	if !falseResults.OK || falseResults.Succeeded != 4 || falseResults.Failed != 0 {
		t.Fatal("source false/None became SDK failure")
	}
	for index := 0; index < 3; index++ {
		ownedCommandAssertQB(t, falseResults.Results[index], false, []string{})
	}
	empty := falseResults.Results[2]
	zero := sha256.Sum256(nil)
	if empty.Generated == nil || empty.Generated.Length != 0 || empty.Generated.SHA256 != hex.EncodeToString(zero[:]) || empty.HostCalls.Total != 2 || empty.HostCalls.Begin != 1 || empty.HostCalls.Submit != 1 || empty.HostCalls.Append != 0 || empty.HostCalls.Invoke != 0 || falseResults.Results[1].HostCalls.Invoke != 1 || falseResults.Results[1].Generated != nil {
		t.Fatal("None and typed empty bytes were conflated")
	}
	if !bytes.Equal(falseResults.Results[3].Result, []byte("null")) || !falseResults.Results[3].OK || falseResults.Results[3].ResultKind != "transmission_native_json_not_python_torrent" {
		t.Fatal("TR source None was fabricated as an object")
	}
	afterFalse := peers.snapshot()
	// Only the new add may dispatch: the stale prior invoke and false/None
	// branches must cause zero additional owned HTTP requests.
	ownedCommandPeerDelta(t, afterFirst, afterFalse, 3, "qb:add")

	peers.mu.Lock()
	peers.failNextRecheck = true
	peers.mu.Unlock()
	recovery := ownedCommandWorkerQuery(t, a, cookie, false, ownedCommandOperation("qbittorrent", "recheck"), ownedCommandOperation("qbittorrent", "start"))
	ownedCommandWorkerWarm(t, falseResults, recovery)
	if recovery.OK || recovery.Failed != 1 || recovery.Succeeded != 1 || recovery.Results[0].OK || recovery.Results[0].ErrorCode != "host_operation_failed" || len(recovery.Results[0].Result) != 0 || recovery.Results[0].HostCalls.Total != 1 || recovery.Results[0].HostCalls.Failed != 1 {
		t.Fatal("selected HTTP 500 became source false/success or lost explicit error")
	}
	ownedCommandAssertControl(t, recovery.Results[1])
	afterRecovery := peers.snapshot()
	ownedCommandPeerDelta(t, afterFalse, afterRecovery, 5, "qb:recheck", "qb:start")
	if afterRecovery.FailedSelected != 1 {
		t.Fatal("selected failing mutation was retried")
	}

	valid := ownedCommandWorkerQuery(t, a, cookie, true, ownedCommandOperation("qbittorrent", "recheck"))
	ownedCommandWorkerWarm(t, recovery, valid)
	ownedCommandWorkerProbe(t, valid, "qbittorrent", "start", "invoke")
	ownedCommandAssertControl(t, valid.Results[0])
	if !valid.OK {
		t.Fatal("fresh callback failed after stale callback denial")
	}
	ownedCommandPeerDelta(t, afterRecovery, peers.snapshot(), 2, "qb:recheck")
	t.Logf("actual registered HTTP worker: pid=%d init=%d invocations=%d; eight SDK operations, tuple/None/empty bytes, one selected failure and same-worker recovery; stale probe adds zero owned HTTP dispatches", valid.PID, valid.InitCount, valid.Invocation)
}

func ownedCommandExpectedDigest(length int) string {
	hash := sha256.New()
	block := make([]byte, 64<<10)
	for offset := 0; offset < length; {
		count := min(len(block), length-offset)
		for index := 0; index < count; index++ {
			block[index] = byte((offset + index) % 251)
		}
		_, _ = hash.Write(block[:count])
		offset += count
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func TestSharedDownloaderCommandsActualWorkerPagedAndCold(t *testing.T) {
	peers := newOwnedCommandPeers(t)
	peers.mu.Lock()
	peers.version = "2.14.0"
	peers.mu.Unlock()
	a, cookie := ownedCommandWorkerFixture(t, peers)
	const size = 16 << 20
	large := ownedCommandOperation("qbittorrent", "add")
	large["args"] = map[string]any{}
	large["synthetic_content"] = map[string]any{"length": size}
	input := ownedCommandWorkerInput(t, false, large)
	object := ownedCommandWorkerObject(t, a)
	baseline := peers.snapshot()
	// Instrument the exact installed live object, never Runner.Run/fake host.
	// Registered HTTP acceptance is tested separately above and below. This
	// ordinary snapshot call exposes real per-call physical wire counters.
	run, err := object.callOriginalMethod(context.Background(), "snapshot", input)
	if err != nil {
		t.Fatal("actual installed-object 16MiB call failed")
	}
	first := ownedCommandWorkerDecode(t, run.Result, 1)
	ownedCommandAssertQB(t, first.Results[0], true, []string{"9007199254740993"})
	if !first.OK || first.Invocation != 1 || first.HadPrevious {
		t.Fatal("fresh actual paged worker observation differs")
	}
	attempt := first.Results[0]
	wantDigest := ownedCommandExpectedDigest(size)
	if attempt.Generated == nil || attempt.Generated.Kind != "deterministic_bytes_i_mod_251" || attempt.Generated.Length != size || attempt.Generated.SHA256 != wantDigest {
		t.Fatal("actual worker generated bytes differ")
	}
	calls := attempt.HostCalls
	if calls.Total != 2 || calls.Begin != 1 || calls.Append != 0 || calls.Submit != 1 || calls.Abort != 0 || calls.Invoke != 0 || calls.Other != 0 || calls.Failed != 0 || calls.AppendPayloadBytes != 0 || calls.InputJSONBytes <= 0 || calls.InputJSONBytes >= 128<<10 || calls.MaxInputJSONBytes >= 128<<10 || calls.OutputJSONBytes <= 0 {
		t.Fatal("actual negotiated upload ordinary-control counters differ")
	}
	if run.HostCalls != 2 || run.ReadWireBytes <= size || run.SentWireBytes <= 0 || run.ReadChunks != 0 {
		t.Fatal("actual installed-object physical pipe counters absent")
	}
	afterLarge := peers.snapshot()
	ownedCommandPeerDelta(t, baseline, afterLarge, 3, "qb:add")
	if afterLarge.MultipartFiles != 1 || afterLarge.MultipartBytes != size || afterLarge.MultipartSHA256 != wantDigest {
		t.Fatal("owned native multipart bytes differ from actual worker material")
	}
	t.Logf("actual installed-object pipe (not HTTP-route counter): callbacks=%d sent_wire_bytes=%d read_wire_bytes=%d sent_chunks=%d read_chunks=%d; native_multipart_bytes=%d sha256=%s", run.HostCalls, run.SentWireBytes, run.ReadWireBytes, run.SentChunks, run.ReadChunks, afterLarge.MultipartBytes, afterLarge.MultipartSHA256)

	warm := ownedCommandWorkerQuery(t, a, cookie, true, ownedCommandOperation("qbittorrent", "start"))
	ownedCommandWorkerWarm(t, first, warm)
	ownedCommandWorkerProbe(t, warm, "qbittorrent", "add", "upload_material_begin")
	ownedCommandAssertControl(t, warm.Results[0])
	if !warm.OK {
		t.Fatal("warm control failed after stale upload-begin probe")
	}
	ownedCommandPeerDelta(t, afterLarge, peers.snapshot(), 3, "qb:start")
	installation, err := a.pluginStore.Installation(context.Background(), ownedCommandWorkerID)
	if err != nil || !installation.Installed {
		t.Fatal("pre-close installation absent")
	}
	saved := a.Settings().Downloaders
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal("owned app close failed")
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal("same owned SQLite directory reopen failed")
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal("cold plugin initialization failed")
	}
	retained := cold.Settings().Downloaders
	if !reflect.DeepEqual(saved, retained) {
		t.Fatal("cold enabled owned downloader settings changed")
	}
	reinstalled, err := cold.pluginStore.Installation(context.Background(), ownedCommandWorkerID)
	if err != nil || !reinstalled.Installed || reinstalled.PluginID != installation.PluginID || reinstalled.Version != installation.Version || reinstalled.Generation != installation.Generation {
		t.Fatal("cold installed artifact identity changed")
	}
	_ = ownedCommandWorkerObject(t, cold)
	login := callTest(t, cold.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, candidate := range login.Result().Cookies() {
		if candidate.Name == "lm_session" {
			coldCookie = candidate
		}
	}
	if login.Code != http.StatusOK || coldCookie == nil {
		t.Fatal("cold ordinary login failed")
	}
	beforeColdCall := peers.snapshot()
	reopened := ownedCommandWorkerQuery(t, cold, coldCookie, true, ownedCommandOperation("transmission", "start"))
	ownedCommandAssertControl(t, reopened.Results[0])
	if !reopened.OK || reopened.PID == first.PID || reopened.Invocation != 1 || reopened.HadPrevious || reopened.InitCount != first.InitCount || reopened.PreviousProbe == nil || reopened.PreviousProbe.Available || reopened.PreviousProbe.CallbackAttempts != 0 {
		t.Fatal("cold worker PID/init/invocation state did not start fresh")
	}
	ownedCommandPeerDelta(t, beforeColdCall, peers.snapshot(), 3, "tr:torrent-start")
	t.Logf("actual cold reopen: old_pid=%d new_pid=%d init=%d invocation=%d; installation/settings retained; no old-live-handle claim", first.PID, reopened.PID, reopened.InitCount, reopened.Invocation)
}
