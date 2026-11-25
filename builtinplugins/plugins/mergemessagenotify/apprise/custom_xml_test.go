package apprise

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSourceCustomXML199(t *testing.T) {
	var fixture struct {
		Owned string `json:"owned_file_base64"`
		Image string `json:"fixture_http_body_base64"`
		Cases []struct {
			Name, URL, Title, Body string
			Attachment             any
			Status                 int
			Added, Result          bool
			Requests               []attachmentWire
		}
	}
	raw, err := os.ReadFile("testdata/custom-xml-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing XML source observations", err)
	}
	image, err := base64.StdEncoding.DecodeString(fixture.Image)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := base64.StdEncoding.DecodeString(fixture.Owned)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "Local & Art.PNG")
	if err := os.WriteFile(filename, owned, 0600); err != nil {
		t.Fatal(err)
	}
	local := (&url.URL{Scheme: "file", Path: filename}).String()
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			var seen []attachmentWire
			client := New(func(_ context.Context, call Request) (Response, error) {
				wire, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				seen = append(seen, wire)
				switch wire.URL {
				case "http://image.invalid/missing.jpg":
					return Response{Status: 404}, nil
				case "https://image.invalid/art.png":
					return Response{Status: 200, Content: image, Headers: map[string]string{
						"Content-Type": "image/png", "Content-Disposition": `inline; filename="art & owned.png"`,
					}}, nil
				}
				if row.Status == 0 {
					return Response{}, errors.New("owned synthetic connection failure")
				}
				return Response{Status: row.Status}, nil
			})
			client.ReadLocalAttachment = func(_ context.Context, path string) ([]byte, error) {
				if path != filename {
					return nil, os.ErrPermission
				}
				return os.ReadFile(path)
			}
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add() = %v, %v; source %v", added, err, row.Added)
			}
			attach := substituteAttachment(row.Attachment, local)
			if result, err := client.Notify(t.Context(), row.Title, row.Body, attach); err != nil || result != row.Result {
				t.Fatalf("Notify() = %v, %v; source %v", result, err, row.Result)
			}
			if len(seen) != len(row.Requests) {
				t.Fatalf("requests = %d; source %d", len(seen), len(row.Requests))
			}
			for i, expected := range row.Requests {
				if !reflect.DeepEqual(seen[i], expected) {
					t.Errorf("request %d = %#v; source %#v", i, seen[i], expected)
				}
			}
		})
	}
}
