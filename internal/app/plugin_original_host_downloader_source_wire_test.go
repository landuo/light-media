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

func TestSourceAddWireActualSDKMatchesIndependentVendorCorpus(t *testing.T) {
	var oracle struct {
		Cases []struct {
			InputType  string   `json:"input_type"`
			WireValues []string `json:"wire_values"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-source-add-wire-20261105/source-wire.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 33 {
		t.Fatal("fixed vendor33 wire corpus unavailable", err)
	}
	a, object := sourceDownloaderFixture(t)
	var mu sync.Mutex
	var expected []string
	var writes atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			io.WriteString(w, "2.11.4")
			return
		}
		if r.URL.Path != "/api/v2/torrents/add" {
			t.Error(r.URL.Path)
			return
		}
		writes.Add(1)
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		want := append([]string{}, expected...)
		mu.Unlock()
		got := r.MultipartForm.Value["skip_checking"]
		if len(got) != len(want) || len(got) > 1 || len(got) == 1 && got[0] != want[0] {
			t.Errorf("actual SDK/native wire got%q want%q", got, want)
		}
		io.WriteString(w, "Ok.")
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}}}
	a.cfgMu.Unlock()
	for i, sample := range oracle.Cases {
		t.Run(fmt.Sprintf("%02d-%s", i, sample.InputType), func(t *testing.T) {
			mu.Lock()
			expected = append([]string{}, sample.WireValues...)
			mu.Unlock()
			r := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), Paused: true}}
			if len(sample.WireValues) == 1 {
				v := sample.WireValues[0]
				r.SkipCheckingWire = &v
			}
			var out plugins.DownloaderSourceMutationResultV1
			if err := hostDownloaderCall(t, object, "source_add", r, &out); err != nil || out.NativeError || !out.Accepted || out.Outcome != "confirmed" {
				t.Fatal("actual source wire changed result", out, err)
			}
		})
	}
	if writes.Load() != 33 {
		t.Fatal("corpus was not sent exactly once per actual source call", writes.Load())
	}
}
func TestSourceAddWireActualSDKKeepsRejectionAndUnknownFacts(t *testing.T) {
	for _, mode := range []string{"rejected", "confirmed", "unknown", "confirmed_revoked", "unknown_revoked"} {
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
				if !reflect.DeepEqual(r.MultipartForm.Value["skip_checking"], []string{"false"}) {
					t.Error("source text coerced/duplicated")
				}
				if strings.HasSuffix(mode, "_revoked") {
					a.cfgMu.Lock()
					v := false
					a.config.Downloaders[0].Enabled = &v
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
			wire := "false"
			r := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true}, SkipCheckingWire: &wire}
			var out plugins.DownloaderSourceMutationResultV1
			if err := hostDownloaderCall(t, object, "source_add", r, &out); err != nil {
				t.Fatal("source wire effect receipt lost", out, err)
			}
			unknown := strings.HasPrefix(mode, "unknown")
			want := "confirmed"
			accepted := true
			if unknown {
				want = "unknown"
				accepted = false
			} else if mode == "rejected" {
				want = "rejected"
				accepted = false
			}
			code := ""
			if strings.HasSuffix(mode, "_revoked") {
				code = "CALL_CHANGED_AFTER_MUTATION"
			}
			if out.Outcome != want || out.Accepted != accepted || out.NativeError != unknown || out.FailureCode != code || writes.Load() != 1 {
				t.Fatal("wire fallback/guard/unknown facts changed", out, writes.Load())
			}
		})
	}
}
func TestSourceAddWireDoesNotBypassOrdinaryOrTransmissionBoundary(t *testing.T) {
	a, object := sourceDownloaderFixture(t)
	var calls, ordinaryWrites atomic.Int32
	var allowOrdinary atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !allowOrdinary.Load() {
			t.Error("rejected wire request reached daemon")
			return
		}
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			io.WriteString(w, "2.11.4")
			return
		}
		ordinaryWrites.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		if !reflect.DeepEqual(r.MultipartForm.Value["skip_checking"], []string{"False"}) {
			t.Error("ordinary bool API bytes changed", r.MultipartForm.Value["skip_checking"])
		}
		io.WriteString(w, "Ok.")
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: peer.URL}}}
	a.cfgMu.Unlock()
	wire := "0"
	r := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true}, SkipCheckingWire: &wire}
	if err := hostDownloaderCall(t, object, "source_add", r, nil); err == nil {
		t.Fatal("TR accepted QB-only wire")
	}
	a.cfgMu.Lock()
	a.config.Downloaders[0].Kind = "qbittorrent"
	a.cfgMu.Unlock()
	if err := hostDownloaderCall(t, object, "resource", map[string]any{"method": plugins.DownloaderAddCapability, "value": r}, nil); err == nil {
		t.Fatal("ordinary API accepted source wire field")
	}
	flag := false
	r.SkipChecking = &flag
	if err := hostDownloaderCall(t, object, "resource", map[string]any{"method": plugins.DownloaderSourceAddCapability, "value": r}, nil); err == nil {
		t.Fatal("raw worker bypassed bool/wire conflict")
	}
	scope := originalBaseCallScope{installation: object.installation, object: object, validate: func(context.Context) error { return nil }}
	raw := json.RawMessage("{\"downloader_id\":\"owned\",\"skip_checking_wire\":\"\xff\"}")
	if _, err := a.originalHostDownloaderSourceResource(context.Background(), scope, plugins.DownloaderSourceAddCapability, raw); err == nil {
		t.Fatal("raw non-UTF8 wire input accepted")
	}
	var checks atomic.Int32
	sentinel := errors.New("owned source-wire transient scope rejection")
	scope.validate = func(context.Context) error {
		if checks.Add(1) == 3 {
			return sentinel
		}
		return nil
	}
	r.SkipChecking = nil
	raw, _ = json.Marshal(r)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	if out, err := a.pluginOriginalResourceHost(context.Background(), scope, identity, plugins.DownloaderSourceAddCapability, raw); len(out) != 0 || !errors.Is(err, sentinel) || checks.Load() < 4 {
		t.Fatal("wire bypassed causal transient guard", string(out), err, checks.Load())
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.pluginOriginalResourceHost(cancelled, scope, identity, plugins.DownloaderSourceAddCapability, raw); !errors.Is(err, context.Canceled) {
		t.Fatal("wire bypassed cancellation", err)
	}
	if calls.Load() != 0 {
		t.Fatal("type/parameter/guard rejection dispatched native request")
	}
	allowOrdinary.Store(true)
	r.SkipCheckingWire = nil
	r.SkipChecking = &flag
	var ordinary plugins.DownloaderMutationV1
	if err := hostDownloaderCall(t, object, "legacy_add", r.DownloaderAddRequestV1, &ordinary); err != nil || !ordinary.Accepted || ordinary.Outcome != "confirmed" || ordinaryWrites.Load() != 1 {
		t.Fatal("ordinary bool API result changed", ordinary, err, ordinaryWrites.Load())
	}
}
