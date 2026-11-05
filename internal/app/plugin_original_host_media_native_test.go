package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func originalHostMediaInstallOwnedTransport(t *testing.T, a *App, respond func(*http.Request) (int, string)) {
	t.Helper()
	settings := a.Settings()
	settings.TMDB = TMDBConfig{APIKey: "owned-host-recognize-key"}
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
	proxy, err := a.proxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	a.catMu.Lock()
	a.catCreds = settings.TMDB
	a.catProxy = proxy
	a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" {
			t.Error("recognition left fixed catalog origin", r.URL.String())
		}
		code, body := respond(r)
		if code == 0 {
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	a.catMu.Unlock()
}

func TestOriginalHostMediaNativeIDDisambiguationActualSDKFixedSource(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	var oracle struct {
		Disambiguation []struct {
			Case     string          `json:"case"`
			TV       json.RawMessage `json:"tv"`
			Movie    json.RawMessage `json:"movie"`
			Meta     map[string]any  `json:"meta"`
			Type     string          `json:"mtype"`
			Order    []string        `json:"lookup_order"`
			Selected *string         `json:"selected_type"`
		} `json:"disambiguation"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/storage-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Disambiguation) != 8 {
		t.Fatal("fixed TMDB source oracle", err)
	}
	for _, sample := range oracle.Disambiguation {
		t.Run(sample.Case, func(t *testing.T) {
			wire := func(raw json.RawMessage, kind string) string {
				var info map[string]any
				if json.Unmarshal(raw, &info) != nil {
					t.Fatal("source metadata object")
				}
				info["media_type"] = kind
				body, _ := json.Marshal(info)
				return string(body)
			}
			var mu sync.Mutex
			calls := []string{}
			originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/3/tv/123":
					calls = append(calls, "电视剧")
					if string(sample.TV) == "null" {
						return 404, `{"success":false,"status_code":34}`
					}
					return 200, wire(sample.TV, "tv")
				case "/3/movie/123":
					calls = append(calls, "电影")
					if string(sample.Movie) == "null" {
						return 404, `{"success":false,"status_code":34}`
					}
					return 200, wire(sample.Movie, "movie")
				default:
					t.Error("unexpected metadata endpoint", r.URL.Path)
					return 404, `{}`
				}
			})
			noCache := false
			result, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, plugins.MediaRecognizeRequest{Source: "themoviedb", TMDBID: 123, Type: sample.Type, Meta: sample.Meta, Cache: &noCache})
			if err != nil {
				t.Fatal("actual SDK native lookup", err)
			}
			mu.Lock()
			order := append([]string{}, calls...)
			mu.Unlock()
			if !reflect.DeepEqual(order, sample.Order) {
				t.Fatal("fixed lookup order", sample.Order, order)
			}
			if sample.Selected == nil {
				if string(result) != "null" {
					t.Fatal("ambiguous source unexpectedly selected", string(result))
				}
				return
			}
			var info map[string]any
			if json.Unmarshal(result, &info) != nil || info["type"] != *sample.Selected {
				t.Fatal("fixed source native selected type", string(result), *sample.Selected)
			}
		})
	}
}

func TestOriginalHostMediaNativeEpisodeGroupsActualSDKFixedSource(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	var oracle struct {
		Groups []struct {
			Case           string          `json:"case"`
			Selected       string          `json:"selected"`
			ProviderGroups json.RawMessage `json:"provider_groups"`
			Result         map[string]any  `json:"result"`
		} `json:"groups"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/storage-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Groups) != 2 {
		t.Fatal("fixed group source oracle", err)
	}
	for _, sample := range oracle.Groups {
		t.Run(sample.Case, func(t *testing.T) {
			originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/3/tv/123":
					return 200, `{"id":123,"name":"Owned group title","original_name":"Owned group title","first_air_date":"2024-01-01","episode_groups":{"results":[{"id":"automatic","type":6}]}}`
				case "/3/tv/episode_group/selected", "/3/tv/episode_group/automatic":
					return 200, `{"groups":` + string(sample.ProviderGroups) + `}`
				default:
					t.Error("unexpected group metadata endpoint", r.URL.Path)
					return 404, `{}`
				}
			})
			noCache := false
			result, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, plugins.MediaRecognizeRequest{Source: "themoviedb", TMDBID: 123, Type: "tv", EpisodeGroup: sample.Selected, Cache: &noCache})
			if err != nil {
				t.Fatal("actual SDK group recognition", err)
			}
			var info map[string]any
			if json.Unmarshal(result, &info) != nil {
				t.Fatal("group JSON", string(result))
			}
			for key, expected := range sample.Result {
				if !reflect.DeepEqual(info[key], expected) {
					t.Fatal("fixed source group field", key, expected, info[key], string(result))
				}
			}
		})
	}
}

func TestOriginalHostManagerSerialSelfRejectAndDeferredSelfReloadActualSDK(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	result, err := ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: "OwnedHostServices", Action: "reload"})
	if err != nil {
		t.Fatal("self reload admission", err)
	}
	var control plugins.PluginControlResult
	if json.Unmarshal(result, &control) != nil || !control.Accepted || control.Completed {
		t.Fatal("self reload falsely complete", string(result))
	}
	a.wg.Wait()
	newObject, ok := a.pluginRunningInstances.Load("OwnedHostServices")
	if !ok || newObject == object {
		t.Fatal("self reload not actually applied")
	}
	if _, err := object.callOriginalMethod(context.Background(), "get_state", nil); err == nil {
		t.Fatal("old self object still executable")
	}
	serial := os.Getenv("OWNED_HOST_SERVICES_SERIAL_FIXTURE_BINARY")
	if serial == "" {
		t.Fatal("separately built serial worker required")
	}
	response := callTest(t, a.Handler(), "DELETE", "/api/plugins/OwnedHostServices", "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installOwnedResourceProcessWorker(t, a, cookie, serial)
	loaded, ok := a.pluginRunningInstances.Load("OwnedHostServices")
	if !ok {
		t.Fatal("serial object missing")
	}
	if _, err := ownedHostServicesCall(t, loaded.(*originalPluginInstance), plugins.PluginManagerReadCapability, plugins.PluginManagerRequest{IncludeState: true}); err == nil {
		t.Fatal("serial self read must reject instead of waiting for itself")
	}
}
