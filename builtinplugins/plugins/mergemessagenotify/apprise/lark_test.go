package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSourceLark199(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Statuses               []int
			Added, Result          bool
			Requests               []attachmentWire
			Waits                  []float64
		}
	}
	data, err := os.ReadFile("testdata/lark-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("invalid source fixture: %v", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var started time.Time
			var seen []attachmentWire
			client := New(func(_ context.Context, call Request) (Response, error) {
				index := len(seen)
				if index >= len(row.Statuses) {
					t.Fatal("unexpected request")
				}
				if index == 0 && len(row.Waits) != 0 {
					wait := time.Duration(row.Waits[0] * float64(time.Second))
					if elapsed := time.Since(started); elapsed < wait {
						t.Errorf("first POST after %s, original waited %s", elapsed, wait)
					}
				}
				wire, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Fatal(err)
				}
				seen = append(seen, wire)
				if row.Statuses[index] == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: row.Statuses[index], Content: []byte("not json")}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			started = time.Now()
			if result, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || result != row.Result {
				t.Fatalf("Notify = %v, %v, source %v", result, err, row.Result)
			}
			if len(seen) != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", len(seen), len(row.Requests))
			}
			for i := range seen {
				if !reflect.DeepEqual(seen[i], row.Requests[i]) {
					t.Errorf("request %d = %#v, source %#v", i, seen[i], row.Requests[i])
				}
			}
		})
	}
}
