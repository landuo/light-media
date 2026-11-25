package apprise

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"testing"
)

func TestSourceGateway199(t *testing.T) {
	type observation struct {
		Method, URL string
		Headers     map[string]string
		Body        string
		Verify      bool
		Timeout     [2]float64
	}
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Status                 int
			Added, Result          bool
			Requests               []observation
		}
	}
	raw, err := os.ReadFile("testdata/gateway-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing fixed-source observations", err)
	}
	uuid4 := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	sourceID := ""
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			observed := []observation{}
			client := New(func(_ context.Context, request Request) (Response, error) {
				headers := http.Header{}
				for _, pair := range request.HeaderOrder {
					headers.Set(pair[0], pair[1])
				}
				if len(request.Auth) != 0 {
					basic, err := httpBasicAuth(request.Auth)
					if err != nil {
						t.Fatal(err)
					}
					headers.Set("Authorization", basic)
				}
				id := headers.Get("X-Apprise-ID")
				if !uuid4.MatchString(id) || (sourceID != "" && sourceID != id) {
					t.Fatal("expected the same process UUID4 for all gateway requests")
				}
				sourceID = id
				headers.Set("X-Apprise-ID", "{uuid4}")
				headerValues := map[string]string{}
				for name := range headers {
					headerValues[name] = headers.Get(name)
				}
				observed = append(observed, observation{
					Method: request.Method, URL: request.URL, Headers: headerValues,
					Body: string(request.Data), Verify: request.Verify, Timeout: request.Timeout,
				})
				return Response{Status: row.Status}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v; want %v", added, err, row.Added)
			}
			sent, err := client.Notify(t.Context(), row.Title, row.Body, nil)
			if err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v; want %v", sent, err, row.Result)
			}
			for i, want := range row.Requests {
				normalized := make(map[string]string, len(want.Headers))
				for name, value := range want.Headers {
					normalized[http.CanonicalHeaderKey(name)] = value
				}
				row.Requests[i].Headers = normalized
			}
			if !reflect.DeepEqual(observed, row.Requests) {
				t.Fatalf("requests = %#v; want %#v", observed, row.Requests)
			}
		})
	}
}
