package plugins

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPluginHTTPDeclarationsPathsAndTransport(t *testing.T) {
	declaration := HTTPAPI{Key: "download", Path: "/jobs/{target_hash}/download", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true, Headers: []string{"content-type"}}
	if err := declaration.Validate(); err != nil {
		t.Fatal(err)
	}
	params, ok := declaration.Match("/jobs/hash123/download")
	if !ok || params["target_hash"] != "hash123" {
		t.Fatal("path binding lost", params, ok)
	}
	for _, path := range []string{"/jobs/../download", "/jobs//download", "/jobs/a/b/download", "/jobs/a\\b/download"} {
		if _, ok := declaration.Match(path); ok {
			t.Fatal("invalid path accepted", path)
		}
	}
	for _, change := range []func(*HTTPAPI){func(d *HTTPAPI) { d.Path = "/../status" }, func(d *HTTPAPI) { d.Path = "/jobs/{same}/{same}" }, func(d *HTTPAPI) { d.Headers = []string{"authorization"} }, func(d *HTTPAPI) { d.Headers = []string{"x-forwarded-host"} }, func(d *HTTPAPI) { d.Auth = "trust-anything" }, func(d *HTTPAPI) { d.Methods = []string{"POST", "POST"} }} {
		invalid := declaration
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("invalid API grant accepted", invalid)
		}
	}
	// Whitespace and arbitrary bytes must survive IPC, including signed JSON.
	for _, body := range [][]byte{[]byte("{ \"challenge\": \"签名回调\" }\n"), {0xff, 0, 1, 10, 255}} {
		request := HTTPRequest{Body: body}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var restored HTTPRequest
		if json.Unmarshal(raw, &restored) != nil || !bytes.Equal(body, restored.Body) {
			t.Fatal("callback body changed across IPC")
		}
	}
	response, err := JSONHTTPResponse(200, map[string]any{"success": false, "data": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var restored HTTPResponse
	json.Unmarshal(response, &restored)
	if restored.Validate() != nil || !json.Valid(restored.Body) {
		t.Fatal("JSON response changed")
	}
	text := HTTPResponse{Status: 200, Body: []byte("plain challenge"), Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}}
	if text.Validate() != nil {
		t.Fatal("plain webhook response unavailable")
	}
	for _, header := range []map[string]string{{"Set-Cookie": "secret=unsafe"}, {"Location": "https://other.invalid"}, {"X-Forwarded-Host": "other.invalid"}} {
		bad := text
		bad.Headers = header
		if bad.Validate() == nil {
			t.Fatal("privileged response header accepted", header)
		}
	}
	if LastQuery(map[string][]string{"domain": {"first", "last"}}, "domain") != "last" {
		t.Fatal("query order changed")
	}
}

func TestPluginHTTPBodylessStatuses(t *testing.T) {
	// The fixed frontend and FastAPI Response permit a successful no-content
	// callback. Other empty JSON bodies still fail before being sent to a client.
	for _, status := range []int{204, 304} {
		if err := (HTTPResponse{Status: status}).Validate(); err != nil {
			t.Fatal("bodyless response rejected", status, err)
		}
	}
	if err := (HTTPResponse{Status: 200}).Validate(); err == nil {
		t.Fatal("empty JSON success accepted")
	}
}

func TestPluginHTTPBinaryAndDownloadMetadata(t *testing.T) {
	raw := []byte{0xff, 0, 1, 0xfe, 10}
	response := HTTPResponse{Status: 200, Body: raw, Headers: map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="owned.bin"`, "ETag": `"owned"`}}
	if err := response.Validate(); err != nil {
		t.Fatal("ordinary binary download rejected", err)
	}
	encoded, err := json.Marshal(response)
	var decoded HTTPResponse
	if err != nil || json.Unmarshal(encoded, &decoded) != nil || !bytes.Equal(decoded.Body, raw) {
		t.Fatal("binary bytes changed across existing IPC", err)
	}
	response.Headers["Content-Type"] = "text/plain"
	if response.Validate() == nil {
		t.Fatal("invalid UTF8 text accepted")
	}
	response.Headers["Content-Type"] = "application/octet-stream\nX-Unsafe: header"
	if response.Validate() == nil {
		t.Fatal("invalid MIME accepted")
	}
	response.Headers["Content-Type"] = "application/octet-stream"
	response.Headers["Content-Disposition"] = "arbitrary; filename=x"
	if response.Validate() == nil {
		t.Fatal("invalid download disposition accepted")
	}
}
