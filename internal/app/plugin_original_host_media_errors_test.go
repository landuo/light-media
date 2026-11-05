package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func TestOriginalHostMediaSourceErrorIdentityActualSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/http-oracle.json")
	var oracle struct {
		Commit     string
		Identities map[string]struct {
			ID   string `json:"module_id"`
			Name string `json:"module_name"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Identities) != 4 {
		t.Fatal("fixed module identities missing", err)
	}
	a, cookie := installOwnedInputFixture(t)
	for source, want := range oracle.Identities {
		for _, method := range []string{"recognize_media", "async_recognize_media"} {
			ctx := context.WithValue(context.Background(), originalHostMediaNativeSourceKey{}, source)
			result, err := a.originalCatalogRecognizeMethod(ctx, method, map[string]any{"source": source}, func() (json.RawMessage, error) { return nil, errors.New("OWNED_PRIVATE_EXCEPTION") })
			if err != nil || originalModuleResultTruthy(result) {
				t.Fatal("source Chain must report failure and return None", err, string(result))
			}
			a.wg.Wait()
			events := ownedInputEvents(t, a, cookie, "OwnedInput")
			if len(events) == 0 {
				t.Fatal("actual SDK error observer missing")
			}
			last := events[len(events)-1]
			var event struct {
				Type string
				Data struct {
					ID     string `json:"module_id"`
					Name   string `json:"module_name"`
					Method string `json:"module_method"`
				}
			}
			if json.Unmarshal(last, &event) != nil || event.Type != "system.error" || event.Data.ID != want.ID || event.Data.Name != want.Name || event.Data.Method != method || strings.Contains(string(last), "OWNED_PRIVATE_EXCEPTION") {
				t.Fatal("error attributed to wrong module or leaked exception", string(last))
			}
		}
	}
}

func TestOriginalHostMediaEpisodeArraySourceBounds(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/episode-array-source.json")
	var source struct {
		Commit                     string
		Count, Length, First, Last int
		JSONBytes                  int `json:"json_bytes"`
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || source.Count != 100001 {
		t.Fatal("fixed source large count missing", err)
	}
	rows, err := originalHostMediaEpisodeList(source.Count)
	if err != nil || len(rows) != source.Length || rows[0] != int64(source.First) || rows[len(rows)-1] != int64(source.Last) {
		t.Fatal("arbitrary episode cap", err, len(rows))
	}
	encoded, err := json.Marshal(rows)
	if err != nil || len(encoded) != source.JSONBytes {
		t.Fatal("source JSON size differs", err, len(encoded), source.JSONBytes)
	}
	_, err = originalHostMediaEpisodeList(plugins.MaxPayloadBytes)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "PLUGIN_VALUE_TOO_LARGE" {
		t.Fatal("response larger than transport should fail before allocation", err)
	}
}

func TestOriginalHostMediaDoubanMalformedPeoplePropagates(t *testing.T) {
	// Fixed DoubanApi._handle_response calls resp.json without suppressing its
	// exception; DoubanModule.douban_info also lets a credits failure escape.
	a := newTestApp(t)
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/douban-oracle.json")
	var oracle struct {
		Cases []struct{ Input json.RawMessage }
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) == 0 {
		t.Fatal(err)
	}
	calls := 0
	e := catalog.NewExplorer(nil, imageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := string(oracle.Cases[0].Input)
		if strings.HasSuffix(r.URL.Path, "/celebrities") {
			body = "{"
		}
		return &http.Response{StatusCode: 500, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	defer e.CloseIdleConnections()
	result, err := a.originalHostMediaRecognizeDouban(context.Background(), e, map[string]any{"doubanid": "901", "mtype": "电影"})
	if err == nil || len(result) != 0 || calls != 2 {
		t.Fatal("non-200 JSON/credits exception changed", string(result), err, calls)
	}
}
