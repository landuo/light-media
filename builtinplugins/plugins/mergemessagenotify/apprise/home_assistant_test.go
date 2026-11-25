package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSourceHomeAssistant199(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Statuses               []int
			Waits                  []float64
			Added                  bool
			Results                []bool
			Requests               []attachmentWire
		}
	}
	data, err := os.ReadFile("testdata/home-assistant-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing Home Assistant source observations", err)
	}
	uuid := regexp.MustCompile(`"notification_id": "([a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12})"`)
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			ids := make(map[string]bool)
			var last time.Time
			count := 0
			client := New(func(_ context.Context, request Request) (Response, error) {
				i := count
				count++
				if i >= len(row.Requests) {
					t.Fatal("unexpected provider request", request.Method)
				}
				got, err := prepareAttachmentWire(request, "")
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				want := row.Requests[i]
				if strings.Contains(want.Body.(string), `"notification_id": "{uuid4}"`) {
					body := got.Body.(string)
					match := uuid.FindStringSubmatch(body)
					if match == nil || ids[match[1]] {
						t.Fatal("missing or reused UUID4 in Home Assistant payload")
					}
					ids[match[1]] = true
					got.Body = strings.Replace(body, match[0], `"notification_id": "{uuid4}"`, 1)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("request %d = %#v, source %#v", i, got, want)
				}
				now := time.Now()
				if i > 0 && i-1 < len(row.Waits) && now.Sub(last)+20*time.Millisecond < time.Duration(row.Waits[i-1]*float64(time.Second)) {
					t.Errorf("request %d did not honor the source interval", i)
				}
				last = now
				if row.Statuses[i] == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: row.Statuses[i], Content: []byte("not json")}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			for i, want := range row.Results {
				if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || sent != want {
					t.Fatalf("Notify %d = %v, %v, source %v", i, sent, err, want)
				}
			}
			if count != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", count, len(row.Requests))
			}
		})
	}
}
