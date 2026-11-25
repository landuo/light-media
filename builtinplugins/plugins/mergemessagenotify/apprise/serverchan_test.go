package apprise

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSourceServerChan199(t *testing.T) {
	type observation struct {
		Method, URL, Body string
		Headers           map[string]string
		Verify            bool
		Timeout           [2]float64
		BodySHA256        string `json:"body_sha256"`
		BodySize          int    `json:"body_size"`
	}
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body, Attach string
			Status, Repeat                 int
			Added, Result                  bool
			Requests                       []observation
		}
	}
	raw, err := os.ReadFile("testdata/serverchan-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing original ServerChan observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			seen := []observation{}
			client := New(func(_ context.Context, req Request) (Response, error) {
				if len(req.Auth) != 0 || len(req.Params) != 0 {
					t.Fatal("ServerChan must not send userinfo or query parameters")
				}
				call := observation{
					Method: req.Method, URL: req.URL, Headers: req.Headers,
					Verify: req.Verify, Timeout: req.Timeout,
				}
				if len(req.Data) > 1024 {
					digest := sha256.Sum256(req.Data)
					call.BodySHA256 = hex.EncodeToString(digest[:])
					call.BodySize = len(req.Data)
				} else {
					call.Body = string(req.Data)
				}
				seen = append(seen, call)
				if row.Status == 0 {
					return Response{}, errors.New("owned synthetic connection failure")
				}
				return Response{Status: row.Status, Content: []byte("this is not a JSON response")}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v; want %v", added, err, row.Added)
			}
			var attach any
			if row.Attach != "" {
				attach = row.Attach
			}
			body := strings.Repeat(row.Body, max(row.Repeat, 1))
			got, err := client.Notify(t.Context(), row.Title, body, attach)
			if err != nil || got != row.Result {
				t.Fatalf("Notify = %v, %v; want %v", got, err, row.Result)
			}
			if !reflect.DeepEqual(seen, row.Requests) {
				t.Fatalf("requests = %#v; want %#v", seen, row.Requests)
			}
		})
	}
}
