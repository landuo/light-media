package app

import (
	"context"
	"encoding/json"
	"errors"
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

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// Actual independent SDK worker/App/SQLite/native transport to an owned peer.
// The source oracle observed a peer, not a downloader daemon, and TT's later
// tuple.hashString source exception belongs to the business adapter.
func TestSourceAddQBLabelsActualSDKSourcePeer(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Labels       []string
			NetworkPOSTs int `json:"network_POSTs_to_owned_peer"`
			PeerResponse struct {
				Status int
				Body   string
			} `json:"peer_response"`
			Trace []struct {
				Event  string
				Fields []struct{ Name, Value string } `json:"non_file_fields"`
			}
		}
	}
	raw, err := os.ReadFile("../../testdata/plugin-source-add-labels-20261105/source-add-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("independent actual source peer oracle missing", err)
	}
	a, object := sourceDownloaderFixture(t)
	var mu sync.Mutex
	expected := []string{}
	status := 200
	body := "Ok."
	var writes atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			io.WriteString(w, "2.11.4")
			return
		}
		if r.URL.Path != "/api/v2/torrents/add" {
			t.Error("unexpected native route", r.URL.Path)
			return
		}
		writes.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		want := append([]string{}, expected...)
		responseStatus, responseBody := status, body
		mu.Unlock()
		got := r.MultipartForm.Value["labels"]
		if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, want) {
			t.Errorf("source labels protocol changed got%q want%q", got, want)
		}
		if len(r.MultipartForm.Value["tags"]) != 0 {
			t.Error("QB labels reinterpreted as tags")
		}
		w.WriteHeader(responseStatus)
		io.WriteString(w, responseBody)
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}}}
	a.cfgMu.Unlock()
	for i, sample := range oracle.Cases {
		if sample.NetworkPOSTs != 1 {
			continue
		}
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			want := []string{}
			for _, step := range sample.Trace {
				if step.Event == "peer.received.POST" {
					for _, field := range step.Fields {
						if field.Name == "labels" {
							want = append(want, field.Value)
						}
					}
				}
			}
			if !reflect.DeepEqual(want, sample.Labels) {
				t.Fatal("source expectation differs from captured wire")
			}
			mu.Lock()
			expected, status, body = want, sample.PeerResponse.Status, sample.PeerResponse.Body
			mu.Unlock()
			before := writes.Load()
			var out plugins.DownloaderSourceMutationResultV1
			request := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), Paused: true}, QBLabels: sample.Labels}
			if err := hostDownloaderCall(t, object, "source_add", request, &out); err != nil {
				t.Fatal("source native outcome lost", err)
			}
			accepted := sample.PeerResponse.Status == 200 && sample.PeerResponse.Body == "Ok."
			nativeError := sample.PeerResponse.Status != 200
			outcome := "rejected"
			if accepted {
				outcome = "confirmed"
			} else if nativeError {
				outcome = "unknown"
			} // preserve existing native POST receipt
			if out.Accepted != accepted || out.NativeError != nativeError || out.Outcome != outcome || out.FailureCode != "" || writes.Load()-before != 1 {
				t.Fatal("source labels changed native receipt/count", out, writes.Load()-before)
			}
		})
	}
}

func TestSourceAddQBLabelsActualSDKKeepsUnknownAndGuardFacts(t *testing.T) {
	for _, mode := range []string{"confirmed", "rejected", "unknown", "confirmed_revoked", "unknown_revoked"} {
		t.Run(mode, func(t *testing.T) {
			a, object := sourceDownloaderFixture(t)
			var writes atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/app/webapiVersion" {
					io.WriteString(w, "2.11.4")
					return
				}
				writes.Add(1)
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(r.MultipartForm.Value["labels"], []string{"a", "b"}) {
					t.Error("labels lost/merged/reordered")
				}
				if strings.HasSuffix(mode, "_revoked") {
					a.cfgMu.Lock()
					enabled := false
					a.config.Downloaders[0].Enabled = &enabled
					a.cfgMu.Unlock()
				}
				if strings.HasPrefix(mode, "unknown") {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				if mode == "rejected" {
					io.WriteString(w, "Fails.")
				} else {
					io.WriteString(w, "Ok.")
				}
			}))
			defer peer.Close()
			a.cfgMu.Lock()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}}}
			a.cfgMu.Unlock()
			request := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true}, QBLabels: []string{"a", "b"}}
			var out plugins.DownloaderSourceMutationResultV1
			if err := hostDownloaderCall(t, object, "source_add", request, &out); err != nil {
				t.Fatal("labels discarded actual mutation fact", err)
			}
			unknown := strings.HasPrefix(mode, "unknown")
			accepted := !unknown && mode != "rejected"
			outcome := "confirmed"
			if unknown {
				outcome = "unknown"
			} else if !accepted {
				outcome = "rejected"
			}
			code := ""
			if strings.HasSuffix(mode, "_revoked") {
				code = "CALL_CHANGED_AFTER_MUTATION"
			}
			if out.Accepted != accepted || out.NativeError != unknown || out.Outcome != outcome || out.FailureCode != code || writes.Load() != 1 {
				t.Fatal("labels lost receipt or retried mutation", out, writes.Load())
			}
		})
	}
}

func TestSourceAddQBLabelsCannotBypassTRLegacyOrScope(t *testing.T) {
	a, object := sourceDownloaderFixture(t)
	var calls, legacyWrites atomic.Int32
	var legacy atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !legacy.Load() {
			t.Error("rejected labels reached native peer")
			return
		}
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			io.WriteString(w, "2.11.4")
			return
		}
		legacyWrites.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		if len(r.MultipartForm.Value["labels"]) != 0 || !reflect.DeepEqual(r.MultipartForm.Value["skip_checking"], []string{"False"}) || !reflect.DeepEqual(r.MultipartForm.Value["tags"], []string{"ordinary"}) {
			t.Error("ordinary API field bytes changed", r.MultipartForm.Value)
		}
		io.WriteString(w, "Ok.")
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: peer.URL}}}
	a.cfgMu.Unlock()
	request := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true}, QBLabels: []string{"a", "b"}}
	if err := hostDownloaderCall(t, object, "source_add", request, nil); err == nil {
		t.Fatal("TR accepted QB-only labels")
	}
	a.cfgMu.Lock()
	a.config.Downloaders[0].Kind = "qbittorrent"
	a.cfgMu.Unlock()
	if err := hostDownloaderCall(t, object, "resource", map[string]any{"method": plugins.DownloaderAddCapability, "value": request}, nil); err == nil {
		t.Fatal("ordinary API accepted source-only labels")
	}
	scope := originalBaseCallScope{installation: object.installation, object: object}
	var checks atomic.Int32
	sentinel := errors.New("owned labels transient scope rejection")
	scope.validate = func(context.Context) error {
		if checks.Add(1) == 3 {
			return sentinel
		}
		return nil
	}
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	raw, _ := json.Marshal(request)
	if out, err := a.pluginOriginalResourceHost(context.Background(), scope, identity, plugins.DownloaderSourceAddCapability, raw); len(out) != 0 || !errors.Is(err, sentinel) || checks.Load() < 4 {
		t.Fatal("labels swallowed transient guard", string(out), err, checks.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.pluginOriginalResourceHost(ctx, scope, identity, plugins.DownloaderSourceAddCapability, raw); !errors.Is(err, context.Canceled) {
		t.Fatal("labels bypassed cancellation", err)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected labels reached daemon", calls.Load())
	}
	legacy.Store(true)
	flag := false
	ordinary := plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true, SkipChecking: &flag, Tags: []string{"ordinary"}}
	var out plugins.DownloaderMutationV1
	if err := hostDownloaderCall(t, object, "legacy_add", ordinary, &out); err != nil || !out.Accepted || out.Outcome != "confirmed" || legacyWrites.Load() != 1 {
		t.Fatal("ordinary API receipt changed", out, err, legacyWrites.Load())
	}
}
