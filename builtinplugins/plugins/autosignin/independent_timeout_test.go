package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sdk "light-media/builtinplugins/internal/sdk"
	"math"
	"os"
	"strings"
	"testing"
)

func TestIndependentSourceConsumerRawTimeoutErrors(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-oracles/independent-source-site-consumer-review-20261106/timeout-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Rows []struct {
			Input     string `json:"timeout_json"`
			Effective string `json:"effective_timeout_json"`
			Result    *string
			Error     *struct{ Class, Message string }
			Wire      int `json:"physical_requests"`
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Rows) != 17 {
		t.Fatal("actualsource rows", e)
	}
	for _, c := range source.Rows {
		t.Run(c.Input, func(t *testing.T) {
			timeout, e := sourceRestoreSavedJSON([]byte(c.Input))
			switch c.Input {
			case "NaN":
				timeout, e = math.NaN(), nil
			case "Infinity":
				timeout, e = math.Inf(1), nil
			case "-Infinity":
				timeout, e = math.Inf(-1), nil
			}
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			var request sdk.SiteSourceHTTPRequestV1
			host := func(_ context.Context, method string, in json.RawMessage) (json.RawMessage, error) {
				if method != sdk.SitesSourceRequestCapability {
					t.Fatal("unexpectedcallback", method)
				}
				calls++
				if e := json.Unmarshal(in, &request); e != nil {
					t.Fatal(e)
				}
				if request.Request.TimeoutSeconds != nil && *request.Request.TimeoutSeconds < 0 {
					return nil, errors.New("host rejects negative numeric timeout")
				}
				return json.Marshal(sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{StatusCode: 200, Content: []byte("owned-body"), ContentType: "text/plain; charset=UTF-8"}})
			}
			ref := sdk.SiteSourceReferenceV1{CustomOrdinal: new(int), Snapshot: strings.Repeat("a", 64)}
			site := Object{"id": json.Number("7"), "url": "http://127.0.0.1/", "ua": nil, "cookie": nil, "timeout": timeout, "_host_source_reference": ref}
			transport := sdkSiteTransport{backend: sdkBackend{ctx: context.Background(), host: host}, site: site}
			var text string
			var got error
			kind := "returned"
			func() {
				defer func() {
					if x := recover(); x != nil {
						switch v := x.(type) {
						case *boundaryFailure:
							kind = "boundary"
							got = v.cause
						case *sourceException:
							kind = "source"
							got = v.cause
						case error:
							kind = "panic"
							got = v
						default:
							kind = "panic"
							got = fmt.Errorf("%v", v)
						}
					}
				}()
				text, got = BasePageSource(context.Background(), site, "http://127.0.0.1/owned", SiteVisitHooks{HTTP: transport})
			}()
			if c.Error == nil {
				if got != nil || c.Result == nil || text != *c.Result || calls != 1 {
					t.Fatalf("source validtimeout got=%q %v kind=%s callbackcalls=%d source=%v", text, got, kind, calls, c.Result)
				}
				return
			}
			if got == nil || kind == "boundary" || got.Error() != c.Error.Message {
				t.Fatalf("source %s:%s with%d physicalHTTP; targettext=%q error=%v kind=%s callbackcalls=%d numericTimeout=%v", c.Error.Class, c.Error.Message, c.Wire, text, got, kind, calls, request.Request.TimeoutSeconds)
			}
		})
	}
}
