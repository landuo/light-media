package apprise

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSourceBark199(t *testing.T) {
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
			Statuses               []int
			Added, Result          bool
			Waits                  []float64
			Requests               []observation
		}
	}
	raw, err := os.ReadFile("testdata/bark-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing fixed-source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			observed := []observation{}
			times := []time.Time{}
			client := New(func(_ context.Context, request Request) (Response, error) {
				index := len(observed)
				if index >= len(row.Statuses) {
					t.Fatal("unexpected Bark transport call")
				}
				times = append(times, time.Now())
				headers := map[string]string{}
				for _, pair := range request.HeaderOrder {
					headers[http.CanonicalHeaderKey(pair[0])] = pair[1]
				}
				if len(request.Auth) != 0 {
					basic, err := httpBasicAuth(request.Auth)
					if err != nil {
						t.Fatal(err)
					}
					headers["Authorization"] = basic
				}
				observed = append(observed, observation{
					Method: request.Method, URL: request.URL, Headers: headers,
					Body: string(request.Data), Verify: request.Verify, Timeout: request.Timeout,
				})
				return Response{Status: row.Statuses[index]}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v; want %v", added, err, row.Added)
			}
			sent, err := client.Notify(t.Context(), row.Title, row.Body, nil)
			if err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v; want %v", sent, err, row.Result)
			}
			if !reflect.DeepEqual(observed, row.Requests) {
				t.Fatalf("requests = %#v; want %#v", observed, row.Requests)
			}
			if max(len(times)-1, 0) != len(row.Waits) {
				t.Fatalf("request intervals = %d; want %d", max(len(times)-1, 0), len(row.Waits))
			}
			for i, want := range row.Waits {
				minimum := time.Duration(want*float64(time.Second)) - 20*time.Millisecond
				if elapsed := times[i+1].Sub(times[i]); elapsed < minimum {
					t.Fatalf("request interval = %v; want at least %v", elapsed, minimum)
				}
			}
		})
	}
}
