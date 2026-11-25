package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSourceMatrix199(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attachments            []string
			Added                  bool
			Results                []bool
			Requests               []struct {
				attachmentWire
				At float64
			}
			Waits   []float64
			Replies []struct {
				Status int
				Body   string
			}
		}
	}
	raw, err := os.ReadFile("testdata/matrix-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing Matrix source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var wire []attachmentWire
			var times []time.Time
			var transaction string
			uuid := regexp.MustCompile(`/send/m\.room\.message/([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$`)
			ts := regexp.MustCompile(`"ts": ([0-9.]+),`)
			client := New(func(_ context.Context, call Request) (Response, error) {
				now := time.Now()
				if row.Name == "matrix-discovery-invalid-delegate" && call.URL == "not-an-absolute-uri/_matrix/client/versions" {
					// Requests rejects this URL before preparing a wire request.
					return Response{}, errors.New("owned missing-schema failure")
				}
				i := len(wire)
				if i >= len(row.Replies) {
					t.Fatalf("unexpected Matrix request %s %s", call.Method, call.URL)
				}
				got, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(row.Requests[i].URL, "<uuid>") {
					match := uuid.FindStringSubmatch(got.URL)
					if match == nil {
						t.Fatalf("missing v4 transaction UUID in %q", got.URL)
					}
					if transaction == "" {
						transaction = match[1]
					}
					if transaction != match[1] {
						t.Fatal("borrowed token changed source transaction UUID")
					}
					got.URL = strings.TrimSuffix(got.URL, match[1]) + "<uuid>"
				}
				if row.Name == "matrix-slack-webhook" {
					data := got.Body.(string)
					match := ts.FindStringSubmatch(data)
					if match == nil {
						t.Fatal("missing Matrix Slack timestamp")
					}
					actual, err := strconv.ParseFloat(match[1], 64)
					if err != nil || actual < float64(now.Unix()-10) || actual > float64(now.Unix()+10) {
						t.Fatalf("invalid Matrix Slack timestamp %v", actual)
					}
					got.Body = ts.ReplaceAllString(data, `"ts": 1234.5,`)
				}
				wire = append(wire, got)
				times = append(times, now)
				reply := row.Replies[i]
				if reply.Status == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			client.ReadLocalAttachment = func(_ context.Context, name string) ([]byte, error) {
				switch filepath.Base(name) {
				case "owned.txt":
					return []byte("owned text"), nil
				case "owned.png":
					return []byte("owned picture"), nil
				default:
					return nil, errors.New("fixture file unavailable")
				}
			}
			var sources []any
			for _, name := range row.Attachments {
				sources = append(sources, "file:///owned/"+name)
			}
			var attach any
			if len(sources) != 0 {
				attach = sources
			}
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			for _, want := range row.Results {
				if sent, err := client.Notify(t.Context(), row.Title, row.Body, attach); err != nil || sent != want {
					t.Fatalf("Notify = %v, %v, source %v", sent, err, want)
				}
			}
			client.Close(t.Context())
			if len(wire) != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", len(wire), len(row.Requests))
			}
			for i := range wire {
				if !reflect.DeepEqual(wire[i], row.Requests[i].attachmentWire) {
					t.Errorf("request %d = %#v, source %#v", i, wire[i], row.Requests[i].attachmentWire)
				}
				if i > 0 {
					interval := time.Duration((row.Requests[i].At - row.Requests[i-1].At) * float64(time.Second))
					if times[i].Sub(times[i-1])+200*time.Microsecond < interval {
						t.Errorf("request %d did not honor source interval %s", i, interval)
					}
				}
			}
			if len(times) != 0 {
				var total float64
				for _, wait := range row.Waits {
					total += wait
				}
				terminal := time.Duration((total - row.Requests[len(times)-1].At) * float64(time.Second))
				if time.Since(times[len(times)-1])+200*time.Microsecond < terminal {
					t.Errorf("final request did not honor source terminal interval %s", terminal)
				}
			}
		})
	}
}
