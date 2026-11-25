package apprise

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSourceCustomForm199(t *testing.T) {
	data, err := os.ReadFile("testdata/custom-form-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Owned    []byte `json:"owned_file_base64"`
		Remote   []byte `json:"http_file_base64"`
		Boundary string `json:"multipart_boundary"`
		Cases    []struct {
			Name, URL, Title, Body string
			Attachment             any
			Statuses               []int
			Added, Result          bool
			Requests               []attachmentWire
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("invalid source form fixture: %v", err)
	}
	const filename = "Owned Note.TXT"
	dir := t.TempDir()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, fixture.Owned, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	localURL := (&url.URL{Scheme: "file", Path: path}).String()
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			seen := 0
			client := New(func(_ context.Context, request Request) (Response, error) {
				i := seen
				seen++
				if i >= len(row.Requests) || i >= len(row.Statuses) {
					t.Fatalf("unexpected %s request", request.Method)
				}
				wire, err := prepareAttachmentWire(request, fixture.Boundary)
				if err != nil {
					t.Fatal(err)
				}
				if body, multipart := wire.Body.(map[string]any); multipart {
					_, params, err := mime.ParseMediaType(request.Headers["Content-Type"])
					if err != nil || params["boundary"] == "" {
						t.Fatalf("invalid multipart boundary: %v", err)
					}
					raw := bytes.ReplaceAll(request.Data, []byte("--"+params["boundary"]), []byte("--"+fixture.Boundary))
					body["raw_base64"] = base64.StdEncoding.EncodeToString(raw)
				}
				if want := row.Requests[i]; !reflect.DeepEqual(wire, want) {
					t.Errorf("request %d = %#v; source %#v", i, wire, want)
				}
				status := row.Statuses[i]
				if status == 0 {
					return Response{}, errors.New("owned transport failure")
				}
				reply := Response{Status: status, Content: []byte("not json")}
				if request.MaxResponseBytes > 0 {
					reply.Content = fixture.Remote
					reply.Headers = map[string]string{"Content-Type": "image/png", "Content-Disposition": `inline; filename="remote image.png"`}
				}
				return reply, nil
			})
			client.ReadLocalAttachment = func(_ context.Context, name string) ([]byte, error) {
				if name != path {
					return nil, os.ErrPermission
				}
				return root.ReadFile(filename)
			}
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v; source %v", added, err, row.Added)
			}
			if sent, err := client.Notify(t.Context(), row.Title, row.Body, substituteAttachment(row.Attachment, localURL)); err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v; source %v", sent, err, row.Result)
			}
			if seen != len(row.Requests) {
				t.Fatalf("request count = %d; source %d", seen, len(row.Requests))
			}
		})
	}
}
