package app

// One selected call per existing S01/S03/S13 SDK fixture, over one self-signed
// owned peer. This adds HTTPS assembly evidence, not a new source oracle.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// Reuse the ordinary manifest inspection/hash/acquisition/install path, with
// only these two pre-existing commands; S03 uses its existing install helper.
func privateTLSInstallFiniteWorker(t *testing.T, a *App, cookie *http.Cookie, family string) *originalPluginInstance {
	t.Helper()
	var environment string
	var expected plugins.Manifest
	switch family {
	case "query":
		environment = "OWNED_TRANSMISSION_QUERY_FIXTURE_BINARY"
		expected = ownedTransmissionQueryManifest(true)
	case "final":
		environment = "OWNED_TR_FINAL_TLS_FIXTURE_BINARY"
		expected = plugins.Manifest{
			SchemaVersion: 1, PluginID: "OwnedTRFinal", Name: "Owned TR final commands SDK fixture", Version: "1.0",
			Runtime: "go-executable", Entry: "owned-tr-final-fixture", HostAPIMin: 1, HostAPIMax: 1,
			Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
			Capabilities: []string{plugins.TransmissionSetTagsCapability, plugins.TransmissionChangeCapability},
			APIs:         []plugins.HTTPAPI{{Key: "tr_final", Path: "/tr-final", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
		}
	default:
		t.Fatal("unselected owned worker")
	}
	index, payload := originalMarketOwnedIndex(t, environment)
	if index.Releases[0].Manifest.Validate() != nil || !reflect.DeepEqual(index.Releases[0].Manifest, expected) {
		t.Fatal("actual finite worker manifest changed")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits, unexpected atomic.Int32
	peer := pluginPrivateOwnedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.IsAbs() || r.URL.RawQuery != "" {
			unexpected.Add(1)
			w.WriteHeader(400)
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
			w.WriteHeader(404)
		}
	}))
	index.Releases[0].Artifacts[0].URL = peer.URL + "/worker"
	body, _ := json.Marshal(map[string]string{"plugin_id": expected.PluginID, "repo_url": peer.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(expected.PluginID)
	if !ok {
		t.Fatal("actual finite worker absent")
	}
	object, ok := value.(*originalPluginInstance)
	if !ok || !object.external || object.hostContract != pluginResourcesSDKContract || !reflect.DeepEqual(object.manifest, expected) {
		t.Fatal("finite worker substituted")
	}
	t.Cleanup(func() {
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Error("finite owned artifact route/count changed")
		}
	})
	return object
}

// Compare owned business state separately from the mandatory HTTP audit log.
// Values stay in memory and are never printed, decrypted or exported.
func privateTLSNonAuditState(t *testing.T, a *App) map[string][]string {
	t.Helper()
	out := instanceReadTables(t, a)
	rows, err := a.Store.DB.Query(`SELECT kind,id,payload,updated_at FROM records WHERE kind <> 'audit' ORDER BY kind,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out["owned-non-audit-records"] = []string{}
	for rows.Next() {
		var kind, id, updated string
		var payload []byte
		if err := rows.Scan(&kind, &id, &payload, &updated); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal([]any{kind, id, payload, updated})
		if err != nil {
			t.Fatal(err)
		}
		out["owned-non-audit-records"] = append(out["owned-non-audit-records"], string(raw))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(a.Settings())
	if err != nil {
		t.Fatal(err)
	}
	out["owned-settings"] = []string{string(settings)}
	return out
}

type privateTLSAuditRow struct{ Payload, Updated string }

func privateTLSAuditRows(t *testing.T, a *App) map[string]privateTLSAuditRow {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT id,payload,updated_at FROM records WHERE kind='audit' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]privateTLSAuditRow{}
	for rows.Next() {
		var id string
		var value privateTLSAuditRow
		if err := rows.Scan(&id, &value.Payload, &value.Updated); err != nil {
			t.Fatal(err)
		}
		out[id] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPluginPrivateTLSFiniteInstalledSDK(t *testing.T) {
	global := http.DefaultTransport.(*http.Transport)
	baseline := global.Clone()
	baseline.CloseIdleConnections()
	globalTLS := global.TLSClientConfig
	environment := os.Environ()
	var mu sync.Mutex
	calls := map[string][]string{}
	material := make([]byte, 128)
	for i := range material {
		material[i] = byte(i % 251)
	}
	digest := sha256.Sum256(material)
	materialSHA := hex.EncodeToString(digest[:])
	added := map[string]any{"id": json.Number("9007199254740993"), "hashString": ownedCommandHash, "name": "owned synthetic torrent", "extra": map[string]any{"falseValue": false, "empty": []any{}, "large": json.Number("9007199254740995")}}
	peer := originalRequestUtilsOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reject := func() { t.Error("unexpected fixed owned TR TLS protocol"); w.WriteHeader(400) }
		user, password, authenticated := r.BasicAuth()
		if r.Method != "POST" || r.URL.IsAbs() || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" || !authenticated || user != ownedCommandUsername || password != ownedCommandPassword {
			reject()
			return
		}
		if r.URL.Path != "/query/rpc" && r.URL.Path != "/add/rpc" && r.URL.Path != "/final/rpc" {
			reject()
			return
		}
		var frame struct {
			Method    string          `json:"method"`
			Arguments map[string]any  `json:"arguments"`
			Tag       json.RawMessage `json:"tag"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if decoder.Decode(&frame) != nil || decoder.Decode(new(any)) != io.EOF {
			reject()
			return
		}
		mu.Lock()
		calls[r.URL.Path] = append(calls[r.URL.Path], frame.Method)
		mu.Unlock()
		session := "owned-private-tls" + r.URL.Path
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			if frame.Method != "session-get" {
				reject()
				return
			}
			w.Header().Set("X-Transmission-Session-Id", session)
			w.WriteHeader(409)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != session {
			reject()
			return
		}
		args := map[string]any{}
		switch frame.Method {
		case "session-get":
			if len(frame.Arguments) != 0 {
				reject()
				return
			}
			args = map[string]any{"version": "owned", "rpc-version": 17}
		case "torrent-get":
			if r.URL.Path != "/query/rpc" || len(frame.Arguments) != 2 || !reflect.DeepEqual(frame.Arguments["ids"], []any{json.Number("7")}) {
				reject()
				return
			}
			fields, ok := frame.Arguments["fields"].([]any)
			if !ok || len(fields) == 0 || len(frame.Tag) == 0 {
				reject()
				return
			}
			args = map[string]any{"torrents": []any{
				map[string]any{"id": 7, "hashString": ownedCommandHash, "name": "owned TLS complete", "status": 6, "labels": []string{"owned"}},
			}}
		case "torrent-add":
			if r.URL.Path != "/add/rpc" || !reflect.DeepEqual(frame.Arguments, map[string]any{"metainfo": base64.StdEncoding.EncodeToString(material), "paused": false}) {
				reject()
				return
			}
			args = map[string]any{"torrent-added": added}
		case "torrent-set":
			want := map[string]any{"ids": []any{strings.Repeat("a", 40)}, "uploadLimited": true, "uploadLimit": json.Number("0")}
			if r.URL.Path != "/final/rpc" || !reflect.DeepEqual(frame.Arguments, want) {
				reject()
				return
			}
		default:
			reject()
			return
		}
		reply := map[string]any{"result": "success", "arguments": args}
		if len(frame.Tag) > 0 {
			reply["tag"] = frame.Tag
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	cfg := a.Settings()
	cfg.PluginMarkets, cfg.PluginLocalRepoPaths, cfg.PluginAutoReload = []string{}, []string{}, false
	yes := true
	cfg.Downloaders = []DownloaderConfig{}
	for _, entry := range []struct{ id, path string }{{"owned-private-query", "/query/rpc"}, {ownedCommandTRID, "/add/rpc"}, {"owned-tr-final-tr", "/final/rpc"}} {
		cfg.Downloaders = append(cfg.Downloaders, DownloaderConfig{Config: download.Config{ID: entry.id, Kind: "transmission", BaseURL: peer.URL + entry.path, Username: ownedCommandUsername, Password: ownedCommandPassword}, SharedPaths: true, Enabled: &yes})
	}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	query := privateTLSInstallFiniteWorker(t, a, cookie, "query")
	ownedCommandWorkerInstall(t, a, cookie)
	final := privateTLSInstallFiniteWorker(t, a, cookie, "final")
	t.Cleanup(a.closeOriginalSessions)
	before := ownedDownloaderChanges(t, a)
	beforeBusiness := privateTLSNonAuditState(t, a)
	beforeAudit := privateTLSAuditRows(t, a)
	// Existing S01 convenience SDK entry, not the older narrow Tasks DTO.
	queried := ownedTransmissionQueryRead(t, query, json.RawMessage(`{"downloader_id":"owned-private-query","ids":[7],"query_method":"get_completed_torrents"}`))
	if queried.Result.Error || len(queried.Result.Torrents) != 1 || queried.Result.DownloaderID != "owned-private-query" || string(queried.Result.Torrents[0].Fields["id"]) != "7" || string(queried.Result.Torrents[0].Fields["status"]) != "6" {
		t.Fatal("actual completed-query SDK TLS projection changed")
	}
	// A direct read has no HTTP success audit and must still be entirely read-only.
	ownedDownloaderNoWrites(t, a, before)
	// One small typed []byte through the already accepted material/v1 upload,
	// private host claim and selected reader-body Add. No daemon parses/downloads it.
	operation := map[string]any{"kind": "transmission", "op": "add", "downloader_id": ownedCommandTRID, "args": map[string]any{}, "synthetic_content": map[string]any{"length": len(material)}}
	uploaded := ownedMaterialWorkerQuery(t, a, cookie, false, operation)
	if !uploaded.OK || uploaded.Succeeded != 1 || uploaded.Failed != 0 || len(uploaded.Grants) != 1 || len(uploaded.MaterialBegins) != 1 {
		t.Fatal("actual typed material SDK Add failed")
	}
	attempt := uploaded.Results[0]
	ownedCommandAssertTR(t, attempt)
	c, grant := attempt.HostCalls, uploaded.Grants[0]
	if c.Total != 2 || c.Begin != 1 || c.Submit != 1 || c.Append != 0 || c.Abort != 0 || c.Invoke != 0 || c.Other != 0 || c.Succeeded != 2 || c.Failed != 0 || uploaded.MaterialBegins[0] != 1 || grant == nil || grant.Contract != plugins.MaterialContract || grant.Capability != plugins.TransmissionAddCapability || grant.Direction != string(plugins.MaterialToHost) || grant.Kind != "bytes" || grant.Total != int64(len(material)) || grant.SHA256 != materialSHA || grant.ChunkBytes < 1 || grant.ChunkBytes > plugins.MaterialFrameBytes || attempt.Generated == nil || attempt.Generated.Length != len(material) || attempt.Generated.SHA256 != materialSHA {
		t.Fatal("actual material grant/callback ownership changed")
	}
	// Existing S13 command accepts this exact fixed argument set. Do not add a
	// general RPC or new SDK capability just for this integration check.
	input := `{"operations":[{"kind":"transmission","op":"change","downloader_id":"owned-tr-final-tr","hash_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","upload_limit":"0","download_limit":null,"ratio_limit":null,"seeding_time_limit":null}],"continue_on_error":true}`
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedTRFinal/tr-final", input, cookie, "")
	var changed struct {
		PID, InitCount, Requested, Completed, Succeeded, Failed int
		OK                                                      bool
		Results                                                 []struct {
			OK           bool
			Result       json.RawMessage
			ResultKind   string                                 `json:"result_kind"`
			SourceTruthy *bool                                  `json:"source_truthy"`
			ErrorCode    string                                 `json:"error_code"`
			HostCalls    struct{ Total, Succeeded, Failed int } `json:"host_calls"`
		}
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &changed) != nil || !changed.OK || changed.PID <= 0 || changed.PID == os.Getpid() || changed.Requested != 1 || changed.Completed != 1 || changed.Succeeded != 1 || changed.Failed != 0 || len(changed.Results) != 1 {
		t.Fatal("actual registered S13 SDK TLS change failed", response.Code)
	}
	result := changed.Results[0]
	if !result.OK || string(result.Result) != "true" || result.ResultKind != "source_bool" || result.SourceTruthy == nil || !*result.SourceTruthy || result.ErrorCode != "" || result.HostCalls.Total != 1 || result.HostCalls.Succeeded != 1 || result.HostCalls.Failed != 0 || queried.PID == uploaded.PID || queried.PID == changed.PID || uploaded.PID == changed.PID {
		t.Fatal("actual S13 result/caller identity changed")
	}
	_ = final // Returned object was independently checked as an installed resources worker.
	for _, raw := range [][]byte{response.Body.Bytes(), attempt.Result} {
		for _, private := range []string{ownedCommandUsername, ownedCommandPassword, "owned-private-tls", peer.URL} {
			if bytes.Contains(raw, []byte(private)) {
				t.Fatal("private transport data leaked into worker report")
			}
		}
	}
	// The Add and Change use two successful authenticated HTTP APIs. The
	// existing middleware synchronously writes one sanitized audit per call.
	afterAudit := privateTLSAuditRows(t, a)
	if ownedDownloaderChanges(t, a) != before+2 || len(afterAudit) != len(beforeAudit)+2 || !reflect.DeepEqual(privateTLSNonAuditState(t, a), beforeBusiness) {
		t.Fatal("two HTTP audit writes or immutable non-audit state differ")
	}
	newRequests := map[string]bool{}
	for id, audit := range afterAudit {
		if previous, exists := beforeAudit[id]; exists {
			if audit != previous {
				t.Fatal("prior audit record changed")
			}
			continue
		}
		var fields map[string]json.RawMessage
		var value struct {
			Actor, Operation, Time string
			RequestID              string `json:"request_id"`
			HTTPStatus             int    `json:"http_status"`
		}
		if json.Unmarshal([]byte(audit.Payload), &fields) != nil || len(fields) != 5 || json.Unmarshal([]byte(audit.Payload), &value) != nil || value.Actor != "admin" || value.Operation != "POST.plugins" || value.HTTPStatus != 200 || value.Time == "" || audit.Updated == "" || !strings.HasPrefix(value.RequestID, "req_") || newRequests[value.RequestID] {
			t.Fatal("actual HTTP audit payload changed")
		}
		newRequests[value.RequestID] = true
	}
	if len(newRequests) != 2 {
		t.Fatal("missing two distinct HTTP audit records")
	}
	var legacyRuns int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&legacyRuns); err != nil || legacyRuns != 0 {
		t.Fatal("SDK mutation used old plugin runs")
	}
	mu.Lock()
	exact := reflect.DeepEqual(calls["/query/rpc"], []string{"session-get", "session-get", "torrent-get"}) && reflect.DeepEqual(calls["/add/rpc"], []string{"session-get", "session-get", "torrent-add"}) && reflect.DeepEqual(calls["/final/rpc"], []string{"session-get", "session-get", "torrent-set"}) && len(calls) == 3
	mu.Unlock()
	if !exact || http.DefaultTransport != global || global.TLSClientConfig != globalTLS || (globalTLS != nil && globalTLS.InsecureSkipVerify) || !reflect.DeepEqual(environment, os.Environ()) {
		t.Fatal("fixed TLS dispatch/global/environment boundary changed")
	}
	t.Logf("three existing SDK workers PID %d/%d/%d; completed query, typed Add, ChangeTorrent each one selected HTTPS request; exact 9 native requests", queried.PID, uploaded.PID, changed.PID)
}
