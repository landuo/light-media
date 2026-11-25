package apprise

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSourceNtfy199(t *testing.T) {
	testSourceNtfy199(t, "testdata/ntfy-1.9.9.json")
}

func TestSourceNtfyBoundary199(t *testing.T) {
	testSourceNtfy199(t, "testdata/ntfy-boundary-1.9.9.json")
}

func testSourceNtfy199(t *testing.T, path string) {
	t.Helper()
	type observation struct {
		Method, URL string
		Headers     map[string]string
		BodySHA256  string `json:"body_sha256"`
		BodySize    int    `json:"body_size"`
		BodyJSON    string `json:"body_json"`
		Verify      bool
		Timeout     [2]float64
	}
	var fixture struct {
		Cases []struct {
			Name, URL, Title string
			Body             json.RawMessage
			Attachments      []string
			Statuses         []int
			Added, Result    bool
			Waits            []float64
			Requests         []observation
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing fixed-source observations", err)
	}
	memoryFile := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}\.txt`)
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var body string
			if err := json.Unmarshal(row.Body, &body); err != nil {
				var recipe struct {
					Repeat, Suffix string
					Count          int
				}
				if err := json.Unmarshal(row.Body, &recipe); err != nil {
					t.Fatal(err)
				}
				body = strings.Repeat(recipe.Repeat, recipe.Count) + recipe.Suffix
			}
			seen := []observation{}
			postTimes := []time.Time{}
			client := New(func(_ context.Context, request Request) (Response, error) {
				target, err := url.Parse(request.URL)
				if err != nil {
					t.Fatal(err)
				}
				if target.Path == "" {
					target.Path = "/"
				}
				for _, pair := range request.ParamOrder {
					if target.RawQuery != "" {
						target.RawQuery += "&"
					}
					target.RawQuery += url.QueryEscape(pair[0]) + "=" + url.QueryEscape(pair[1])
				}
				headers := map[string]string{}
				for _, pair := range request.HeaderOrder {
					headers[pair[0]] = pair[1]
				}
				if len(request.Auth) != 0 {
					basic, err := httpBasicAuth(request.Auth)
					if err != nil {
						t.Fatal(err)
					}
					headers["Authorization"] = basic
				}
				item := observation{
					Method: request.Method, URL: memoryFile.ReplaceAllString(target.String(), "{uuid4}.txt"),
					Headers: headers, BodySize: len(request.Data), BodySHA256: fmt.Sprintf("%x", sha256.Sum256(request.Data)),
					Verify: request.Verify, Timeout: request.Timeout,
				}
				if len(request.Data) < 350 && headers["Content-Type"] == "application/json" {
					item.BodyJSON = string(request.Data)
				}
				seen = append(seen, item)
				if request.Method == http.MethodGet {
					switch target.Path {
					case "/a.png":
						return Response{Status: 200, Content: []byte("\x89PNG\r\n\x1a\nowned bytes"), Headers: map[string]string{"Content-Type": "image/png"}}, nil
					case "/b.txt":
						return Response{Status: 200, Content: []byte("owned text"), Headers: map[string]string{"Content-Type": "text/plain"}}, nil
					default:
						return Response{Status: 404}, nil
					}
				}
				if len(postTimes) >= len(row.Statuses) {
					t.Fatalf("unexpected ntfy POST to %s", item.URL)
				}
				status := row.Statuses[len(postTimes)]
				postTimes = append(postTimes, time.Now())
				return Response{Status: status, Content: []byte("{}")}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v; want %v", added, err, row.Added)
			}
			var attachments any
			if len(row.Attachments) != 0 {
				attachments = row.Attachments
			}
			sent, err := client.Notify(t.Context(), row.Title, body, attachments)
			if err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v; want %v", sent, err, row.Result)
			}
			if !reflect.DeepEqual(seen, row.Requests) {
				t.Fatalf("requests = %#v; want %#v", seen, row.Requests)
			}
			if max(len(postTimes)-1, 0) != len(row.Waits) {
				t.Fatalf("POST intervals = %d; want %d", max(len(postTimes)-1, 0), len(row.Waits))
			}
			for i, wait := range row.Waits {
				minimum := time.Duration(wait*float64(time.Second)) - 20*time.Millisecond
				if elapsed := postTimes[i+1].Sub(postTimes[i]); elapsed < minimum {
					t.Fatalf("POST interval = %v; want at least %v", elapsed, minimum)
				}
			}
		})
	}
}
