package apprise

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSourceTelegram199(t *testing.T) {
	var fixture struct {
		Boundary string
		Cases    []struct {
			Name, URL, Title string
			Body             json.RawMessage
			Attachments      []string
			Added            bool
			Results          []bool
			Waits            []float64
			Replies          []struct {
				Status  int
				Content json.RawMessage
			}
			Requests []attachmentWire
		}
	}
	data, err := os.ReadFile("testdata/telegram-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing fixed-source Telegram observations", err)
	}
	files := map[string]struct{ mime, content string }{
		"/picture.png": {"image/png", "owned picture"},
		"/motion.gif":  {"image/gif", "owned animation"},
		"/clip.mp4":    {"video/mp4", "owned video"},
		"/note.ogg":    {"application/ogg", "owned voice"},
		"/song.mp3":    {"audio/mpeg", "owned audio"},
		"/notes.txt":   {"text/plain", "owned document"},
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var body string
			if err := json.Unmarshal(row.Body, &body); err != nil {
				var repeated struct {
					Repeat string
					Count  int
				}
				if err := json.Unmarshal(row.Body, &repeated); err != nil {
					t.Fatal(err)
				}
				body = strings.Repeat(repeated.Repeat, repeated.Count)
			}
			var seen []attachmentWire
			var messageTimes []time.Time
			posts := 0
			client := New(func(_ context.Context, request Request) (Response, error) {
				observedAt := time.Now()
				wire, err := prepareAttachmentWire(request, fixture.Boundary)
				if err != nil {
					return Response{}, err
				}
				wire.Body, err = telegramNormalizeWire(wire.Body)
				if err != nil {
					return Response{}, err
				}
				seen = append(seen, wire)
				address, err := url.Parse(request.URL)
				if err != nil {
					return Response{}, err
				}
				if request.Method == http.MethodGet {
					if file, ok := files[address.Path]; ok {
						return Response{Status: 200, Headers: map[string]string{"Content-Type": file.mime}, Content: []byte(file.content)}, nil
					}
					return Response{Status: 404}, nil
				}
				if posts >= len(row.Replies) {
					t.Errorf("unexpected Telegram POST: %s", wire.URL)
					return Response{}, errors.New("unexpected POST")
				}
				if !strings.HasSuffix(address.Path, "/getUpdates") {
					messageTimes = append(messageTimes, observedAt)
				}
				reply := row.Replies[posts]
				posts++
				if reply.Status == 0 {
					return Response{}, errors.New("owned synthetic transport error")
				}
				payload := []byte(reply.Content)
				if len(payload) > 0 && payload[0] == '"' {
					var text string
					if err := json.Unmarshal(payload, &text); err != nil {
						return Response{}, err
					}
					payload = []byte(text)
				}
				return Response{Status: reply.Status, Content: payload}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, original %v", added, err, row.Added)
			}
			for i, expected := range row.Results {
				result, err := client.Notify(t.Context(), row.Title, body, row.Attachments)
				if err != nil || result != expected {
					t.Fatalf("Notify[%d] = %v, %v, original %v", i, result, err, expected)
				}
			}
			if len(seen) != len(row.Requests) {
				t.Fatalf("requests = %d, original %d", len(seen), len(row.Requests))
			}
			for i, actual := range seen {
				if !reflect.DeepEqual(actual, row.Requests[i]) {
					t.Errorf("request %d = %#v, original %#v", i, actual, row.Requests[i])
				}
			}
			if len(row.Waits) != max(0, len(messageTimes)-1) {
				t.Fatalf("message interval count = %d, original %d", len(messageTimes)-1, len(row.Waits))
			}
			for i, wait := range row.Waits {
				minimum := time.Duration(wait*float64(time.Second)) - 150*time.Microsecond
				if actual := messageTimes[i+1].Sub(messageTimes[i]); actual < minimum {
					t.Errorf("throttle interval %v, original minimum %v", actual, minimum)
				}
			}
		})
	}
}

func telegramNormalizeWire(body any) (any, error) {
	switch value := body.(type) {
	case string:
		if len(value) > 1200 {
			return map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(value)))}, nil
		}
	case map[string]any:
		parts, _ := value["multipart"].([]any)
		for _, item := range parts {
			part := item.(map[string]any)
			encoded := part["base64"].(string)
			if len(encoded) <= 1600 {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, err
			}
			part["base64"] = map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256(data)), "size": float64(len(data))}
		}
	}
	return body, nil
}
