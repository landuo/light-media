package apprise

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestSourceURLList199(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, Line    string
			Added, Result bool
			Requests      []attachmentWire
		}
	}
	raw, err := os.ReadFile("testdata/url-list-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing pinned Apprise source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			var wire []attachmentWire
			var mu sync.Mutex
			client := New(func(_ context.Context, call Request) (Response, error) {
				observed, err := prepareAttachmentWire(call, "")
				if err != nil {
					return Response{}, err
				}
				mu.Lock()
				wire = append(wire, observed)
				mu.Unlock()
				return Response{Status: 200}, nil
			})
			if added, err := client.Add(row.Line); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			if sent, err := client.Notify(t.Context(), "owned title", "owned body", nil); err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v, source %v", sent, err, row.Result)
			}
			// Independent services run concurrently in the original too.
			slices.SortFunc(wire, func(a, b attachmentWire) int { return cmp.Compare(a.URL, b.URL) })
			if !reflect.DeepEqual(wire, row.Requests) {
				t.Errorf("wire = %#v, source %#v", wire, row.Requests)
			}
		})
	}
}
