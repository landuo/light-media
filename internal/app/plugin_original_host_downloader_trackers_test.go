package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/download"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHostDownloaderTrackerFixedSourcePrimitivesAndPrivateText(t *testing.T) {
	var oracle struct {
		Cases []struct {
			URL   string `json:"url"`
			Group string `json:"group"`
			Valid bool   `json:"valid"`
		} `json:"cases"`
		Messages []string `json:"messages"`
		Texts    []struct {
			Message  string `json:"message"`
			NonEmpty bool   `json:"nonempty"`
			NonBlank bool   `json:"nonblank"`
			Matches  []bool `json:"matches"`
			Stripped []bool `json:"stripped_matches"`
		} `json:"message_cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/tracker-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) < 10 {
		t.Fatal("fixed tracker primitive oracle unavailable", err)
	}
	a := newTestApp(t)
	endpoints := []plugins.DownloaderTrackerEndpointV1{}
	for i, scenario := range oracle.Cases {
		group, valid := originalTrackerSourceGroup(scenario.URL)
		if valid != scenario.Valid || valid && group != scenario.Group {
			t.Fatal("fixed URL grouping changed at source case", i)
		}
		endpoint := a.originalTrackerEndpoint(scenario.URL)
		if strings.Contains(endpoint.Display, "OWNED_") || strings.Contains(endpoint.Display, "passkey") || strings.Contains(endpoint.Display, "user:") {
			t.Fatal("tracker endpoint leaked private component", i)
		}
		endpoints = append(endpoints, endpoint)
	}
	for i, left := range oracle.Cases {
		for j, right := range oracle.Cases {
			if left.Valid && right.Valid && (endpoints[i].ComparisonKey == endpoints[j].ComparisonKey) != (left.Group == right.Group) {
				t.Fatal("opaque source equality changed", i, j)
			}
		}
	}
	for i, scenario := range oracle.Texts {
		out := a.originalTrackerText(scenario.Message, oracle.Messages, []string{"OWNED_A"})
		if out.NonEmpty != scenario.NonEmpty || out.NonBlank != scenario.NonBlank || !reflect.DeepEqual(out.Matches, scenario.Matches) || !reflect.DeepEqual(out.StrippedMatches, scenario.Stripped) {
			t.Fatal("exact pre-redaction message comparison changed", i)
		}
		if strings.Contains(out.Display, "OWNED_A") {
			t.Fatal("private query/message text leaked", i)
		}
	}
}

func TestHostDownloaderTrackersActualSDKTaskIsolationCloneAndRestart(t *testing.T) {
	a, cookie, object := hostDownloaderFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.URL.Path != "/api/v2/torrents/trackers" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer OWNED_HOST_KEY" || len(r.Form) != 1 {
			t.Error("tracker read changed endpoint, scope or host credentials")
		}
		key := "OWNED_PRIVATE_A"
		if r.Form.Get("hash") == ownedDownloaderOtherHash {
			key = "OWNED_PRIVATE_B"
		} else if r.Form.Get("hash") != ownedDownloaderHash {
			t.Error("tracker query widened exact task")
		}
		rows := []map[string]any{{"url": "https://tracker.invalid/announce?passkey=" + key, "status": 2, "msg": "Success", "tier": 0}, {"url": "udp://tracker.invalid/announce?passkey=" + key, "status": 4, "msg": " rejected " + key + " ", "tier": 1}, {"url": "https://owned-user:OWNED_URI_PASSWORD@tracker.invalid:8443/announce?token=" + key, "status": 4, "msg": "bad passkey=" + key, "tier": 2}, {"url": "** [DHT] **", "status": 0, "msg": "", "tier": -1}}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL, APIKey: "OWNED_HOST_KEY"}}}
	ownedDownloaderSave(t, a, settings)
	request := func(instance *originalPluginInstance, hash string) plugins.DownloaderTrackersV1 {
		t.Helper()
		var out plugins.DownloaderTrackersV1
		err := hostDownloaderCall(t, instance, "trackers", plugins.DownloaderTrackersRequestV1{DownloaderID: "owned", Hash: hash, ErrorMessages: []string{"Success", "rejected OWNED_PRIVATE_A", " rejected OWNED_PRIVATE_A ", " rejected OWNED_PRIVATE_B "}}, &out)
		if err != nil {
			t.Fatal("actual SDK tracker callback failed", err)
		}
		encoded, _ := json.Marshal(out)
		for _, secret := range []string{"OWNED_PRIVATE_A", "OWNED_PRIVATE_B", "OWNED_URI_PASSWORD", "owned-user", "OWNED_HOST_KEY", "passkey=", "token="} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("tracker response exposed private source", secret)
			}
		}
		return out
	}
	before := ownedDownloaderChanges(t, a)
	first := request(object, strings.ToUpper(ownedDownloaderHash))
	second := request(object, ownedDownloaderOtherHash)
	if len(first.Trackers) != 4 || len(second.Trackers) != 4 || first.Hash != ownedDownloaderHash || second.Hash != ownedDownloaderOtherHash || first.Trackers[0].Endpoint.ComparisonKey != second.Trackers[0].Endpoint.ComparisonKey || first.Trackers[1].Endpoint.ComparisonKey == second.Trackers[1].Endpoint.ComparisonKey {
		t.Fatal("HTTP/UDP task grouping or exact identity changed")
	}
	if !first.Trackers[1].Message.Matches[2] || !first.Trackers[1].Message.StrippedMatches[1] || !second.Trackers[1].Message.Matches[3] {
		t.Fatal("private text matching was performed after redaction")
	}
	ownedDownloaderNoWrites(t, a, before)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+object.installation.PluginID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(object.installation.PluginID + "twin")
	if !ok {
		t.Fatal("tracker clone not loaded")
	}
	cloned := request(value.(*originalPluginInstance), ownedDownloaderHash)
	if !reflect.DeepEqual(cloned, first) {
		t.Fatal("clone changed shared-host source tracker equality facts")
	}
	dir := a.Dir
	if a.Close() != nil {
		t.Fatal("close failed")
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, ok = cold.pluginRunningInstances.Load(object.installation.PluginID)
	if !ok {
		t.Fatal("cold tracker source not loaded")
	}
	restarted := request(value.(*originalPluginInstance), ownedDownloaderHash)
	if !reflect.DeepEqual(restarted, first) || calls.Load() != 4 {
		t.Fatal("opaque source comparisons changed across restart or read replayed", calls.Load())
	}
}

func TestHostDownloaderTransmissionTrackerAliasesActualSDK(t *testing.T) {
	a, _, object := hostDownloaderFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&env)
		var method string
		_ = json.Unmarshal(env["method"], &method)
		if method == "session_get" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, env["id"])
			return
		}
		if method != "torrent_get" {
			http.Error(w, "unexpected", 400)
			return
		}
		var args struct {
			IDs []string `json:"ids"`
		}
		_ = json.Unmarshal(env["params"], &args)
		if !reflect.DeepEqual(args.IDs, []string{ownedDownloaderHash}) {
			t.Error("TR tracker exact query widened")
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"torrents":[{"hash_string":"%s","error":2,"error_string":" rejected OWNED_PRIVATE_TR ","tracker_stats":[{"announce":"udp://tracker.invalid/a?passkey=OWNED_PRIVATE_TR","last_announce_result":" rejected OWNED_PRIVATE_TR ","last_announce_succeeded":false,"has_announced":true,"tier":1}],"trackers":[{"announce":"https://tracker.invalid/a?passkey=OWNED_PRIVATE_TR","tier":2}],"tracker_list":["https://tracker.invalid/b?passkey=OWNED_PRIVATE_TR"]}]}}`, env["id"], ownedDownloaderHash)
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, settings)
	var out plugins.DownloaderTrackersV1
	if err := hostDownloaderCall(t, object, "trackers", plugins.DownloaderTrackersRequestV1{DownloaderID: "owned", Hash: ownedDownloaderHash, ErrorMessages: []string{"rejected OWNED_PRIVATE_TR", " rejected OWNED_PRIVATE_TR "}}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.TaskFound || out.TaskErrorCode == nil || *out.TaskErrorCode != "2" || out.TaskErrorText == nil || !out.TaskErrorText.StrippedMatches[0] || !out.TrackerStatsPresent || len(out.TrackerStats) != 1 || !out.TrackersPresent || len(out.Trackers) != 1 || !out.TrackerListPresent || len(out.TrackerList) != 1 {
		t.Fatal("TR native aliases/source properties lost", out)
	}
	row := out.TrackerStats[0]
	if row.LastAnnounceSucceeded == nil || *row.LastAnnounceSucceeded || row.HasAnnounced == nil || !*row.HasAnnounced || row.Message == nil || !row.Message.Matches[1] {
		t.Fatal("source TR flags or exact message match lost")
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "OWNED_PRIVATE_TR") || strings.Contains(string(encoded), "passkey=") {
		t.Fatal("TR errorString or tracker URI leaked")
	}
}

func TestHostDownloaderTrackerScopeRejectsAmbiguousAndRevoked(t *testing.T) {
	a, _, object := hostDownloaderFixture(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, `[]`) }))
	defer server.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, settings)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	callback, revoke := object.originalBaseSessionHost(context.Background(), "trackers", nil)
	for _, raw := range []string{`null`, `{"downloader_id":"owned","hash":"all","error_messages":[]}`, `{"downloader_id":"owned","hash":"` + ownedDownloaderHash + `"}`, `{"downloader_id":"owned","hash":"` + ownedDownloaderHash + `","error_messages":[],"url":"https://foreign.invalid"}`} {
		if result, err := callback(context.Background(), identity, plugins.DownloaderTrackersCapability, json.RawMessage(raw)); err == nil || len(result) != 0 {
			t.Fatal("ambiguous tracker read accepted")
		}
	}
	revoke()
	if _, err := callback(context.Background(), identity, plugins.DownloaderTrackersCapability, json.RawMessage(`{"downloader_id":"owned","hash":"`+ownedDownloaderHash+`","error_messages":[]}`)); err == nil {
		t.Fatal("revoked tracker scope accepted")
	}
	if hits.Load() != 0 {
		t.Fatal("rejected tracker request reached daemon")
	}
}
