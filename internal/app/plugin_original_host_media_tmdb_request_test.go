package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalHostTMDBRequestPinnedSourceSnapshotFreshRateAndRetry(t *testing.T) {
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case    string `json:"case"`
			Replies []*struct {
				Status    int               `json:"status"`
				Body      json.RawMessage   `json:"body"`
				Headers   map[string]string `json:"headers"`
				Exception string            `json:"exception"`
			} `json:"replies"`
			Calls []struct {
				Fresh bool `json:"fresh"`
			} `json:"calls"`
			Observations []struct {
				Value json.RawMessage `json:"value"`
				Error bool            `json:"error"`
			} `json:"observations"`
			Requests int       `json:"requests"`
			Sleeps   []float64 `json:"sleeps"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/recognition-provider-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 10 {
		t.Fatal("fixed source provider oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			wall, mono := 1000.0, 10.0
			sleeps := []float64{}
			a, err := New(Options{DataDir: t.TempDir(), HostMediaRecognition: &HostMediaRecognitionConfiguration{Locale: "zh", Source: "themoviedb", MetaCacheExpire: big.NewInt(1), WallClock: func() float64 { return wall }, MonotonicClock: func() float64 { return mono }, Sleep: func(ctx context.Context, seconds float64) error {
				sleeps = append(sleeps, seconds)
				wall += seconds
				mono += seconds
				return ctx.Err()
			}}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			requests := 0
			originalHostMediaInstallOwnedTransport(t, a, func(*http.Request) (int, string) { t.Fatal("transport seam not replaced"); return 500, "" })
			a.catMu.Lock()
			a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: a.Settings().TMDB.APIKey}, imageTransport(func(request *http.Request) (*http.Response, error) {
				if requests >= len(sample.Replies) {
					t.Error("unexpected source retry", requests)
					return nil, errors.New("no owned reply")
				}
				reply := sample.Replies[requests]
				requests++
				if reply == nil {
					return nil, io.ErrUnexpectedEOF
				}
				if reply.Exception == "generic" {
					return nil, errors.New("owned generic request failure")
				}
				status := reply.Status
				if status == 0 {
					status = 200
				}
				body := string(reply.Body)
				if strings.HasPrefix(body, `"`) {
					if json.Unmarshal(reply.Body, &body) != nil {
						t.Fatal("owned raw body")
					}
				}
				headers := http.Header{"Content-Type": {"application/json"}}
				for key, value := range reply.Headers {
					headers.Set(key, value)
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			a.catMu.Unlock()
			for index, call := range sample.Calls {
				ctx := context.Background()
				if call.Fresh {
					ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
				}
				body, err := a.originalHostMediaTMDBResponse(ctx, "search", "owned-source-cache-key", func() (catalog.SourceRecognitionResponse, error) {
					return a.catalog().SourceRecognitionSearchResponse(ctx, "multi", "Owned", "", "zh")
				})
				if (err != nil) != sample.Observations[index].Error {
					t.Fatal("source provider exception/success", index, err, string(body), sample.Observations[index])
				}
				if err == nil {
					value, err := decodeSourceJSON(body)
					if err != nil {
						t.Fatal(err)
					}
					observed, _ := json.Marshal(sourceMap(value)["results"])
					if !originalBaseJSONEqual(observed, sample.Observations[index].Value) {
						t.Fatal("source provider result", index, string(sample.Observations[index].Value), string(observed))
					}
				}
			}
			if requests != sample.Requests || !reflect.DeepEqual(sleeps, sample.Sleeps) {
				t.Fatal("source physical request/fresh/rate order", sample.Requests, requests, sample.Sleeps, sleeps)
			}
		})
	}
}
