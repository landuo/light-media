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

func TestSourceJoinAndIFTTT199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/join-ifttt-1.9.9.json")
}

func TestSourcePushjet199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/pushjet-1.9.9.json")
}

func TestSourceIFTTTDotSegments199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/ifttt-dot-1.9.9.json")
}

func TestSourceZulip199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/zulip-1.9.9.json")
}

func TestSourceFlockAndProwl199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/flock-prowl-1.9.9.json")
}

func TestSourceFlockImage199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/flock-image-1.9.9.json")
}

func TestSourcePushPlusFeishuDingTalk199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/cn-webhooks-1.9.9.json")
}

func testSourceHTTPProvider(t *testing.T, path string) {
	t.Helper()
	var fixture struct {
		FixedTimeMS int64 `json:"fixed_time_ms"`
		Cases       []struct {
			Name, URL, Title, Body string
			Attach                 any
			Statuses               []int
			Waits                  []float64
			Added, Result          bool
			Requests               []attachmentWire
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing HTTP provider source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var seen []attachmentWire
			var last time.Time
			client := New(func(_ context.Context, call Request) (Response, error) {
				i := len(seen)
				if i >= len(row.Statuses) {
					t.Fatal("unexpected provider request", call.Method)
				}
				got, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				now := time.Now()
				if i > 0 && i-1 < len(row.Waits) && now.Sub(last)+20*time.Millisecond < time.Duration(row.Waits[i-1]*float64(time.Second)) {
					t.Errorf("request %d did not honor the source interval", i)
				}
				last = now
				seen = append(seen, got)
				if row.Statuses[i] == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: row.Statuses[i], Content: []byte("not json")}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			if fixture.FixedTimeMS != 0 {
				for _, service := range client.services {
					if dingTalk, ok := service.(*dingTalkService); ok {
						dingTalk.clock = func() time.Time { return time.UnixMilli(fixture.FixedTimeMS) }
					}
				}
			}
			if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v, source %v", sent, err, row.Result)
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
