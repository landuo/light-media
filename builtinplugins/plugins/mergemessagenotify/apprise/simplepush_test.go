package apprise

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
	"time"
)

type simplePushIVReader struct {
	io.Reader
	draws []int
}

func (r *simplePushIVReader) Read(p []byte) (int, error) {
	r.draws = append(r.draws, len(p))
	return r.Reader.Read(p)
}

func TestSourceSimplePush199(t *testing.T) {
	var fixture struct {
		IVHex string `json:"iv_hex"`
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Added                  bool
			Results                []bool
			Waits                  []float64
			IVDraws                []int `json:"iv_draws"`
			Requests               []attachmentWire
			Replies                []struct {
				Status int
				Body   string
			}
		}
	}
	raw, err := os.ReadFile("testdata/simplepush-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing SimplePush source observations", err)
	}
	iv, err := hex.DecodeString(fixture.IVHex)
	if err != nil || len(iv) != 16 {
		t.Fatal("missing fixed oracle IV", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var requests []attachmentWire
			var last time.Time
			client := New(func(_ context.Context, call Request) (Response, error) {
				now := time.Now()
				i := len(requests)
				if i >= len(row.Replies) {
					t.Fatal("unexpected SimplePush request")
				}
				if i > 0 && now.Sub(last)+20*time.Millisecond < time.Duration(row.Waits[i-1]*float64(time.Second)) {
					t.Errorf("request %d did not honor the source interval", i)
				}
				last = now
				wire, err := prepareAttachmentWire(call, "")
				if err != nil {
					return Response{}, err
				}
				requests = append(requests, wire)
				reply := row.Replies[i]
				if reply.Status == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			ivReader := &simplePushIVReader{Reader: bytes.NewReader(iv), draws: []int{}}
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			if added {
				client.services[0].(*simplePushService).random = ivReader
			}
			for _, want := range row.Results {
				if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || sent != want {
					t.Fatalf("Notify = %v, %v, source %v", sent, err, want)
				}
			}
			if len(requests) != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", len(requests), len(row.Requests))
			}
			for i, request := range requests {
				if !reflect.DeepEqual(request, row.Requests[i]) {
					t.Errorf("request %d = %#v, source %#v", i, request, row.Requests[i])
				}
			}
			if !reflect.DeepEqual(ivReader.draws, row.IVDraws) {
				t.Errorf("IV reads = %v, source %v", ivReader.draws, row.IVDraws)
			}
		})
	}
}
