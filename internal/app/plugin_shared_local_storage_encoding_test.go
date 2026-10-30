package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"light-media/internal/plugins"
)

// Ordinary raw JSON tokens must not select a different legitimate filename.
// No permission, identity replacement, link or filesystem race is exercised.
func TestSharedLocalStorageOrdinaryRawPathEncoding(t *testing.T) {
	a, _, p := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	replacement := filepath.Join(root, "\ufffd")
	original := []byte("owned U+FFFD original fixture")
	if e := sharedLocalStorageFixtureWrite(t, replacement, original, 0600); e != nil {
		t.Fatal(e)
	}
	prefix := strconv.Quote(root + "/")
	for _, storage := range []json.RawMessage{json.RawMessage(`"local"`), json.RawMessage(`"\u006cocal"`)} {
		for _, tail := range []string{`\ud800"`, `\udcff"`} {
			rawPath := json.RawMessage(prefix[:len(prefix)-1] + tail)
			item := &plugins.ModuleFileItem{Fields: map[string]json.RawMessage{"path": rawPath, "storage": storage}}
			for _, operation := range sharedLocalStorageAll {
				for _, transport := range []string{"resource", "resource_raw"} {
					raw, e := json.Marshal(map[string]any{"method": operation, "payload": plugins.LocalStorageRequest{FileItem: item}})
					if e != nil {
						t.Fatal(e)
					}
					if !bytes.Contains(raw, rawPath) || !bytes.Contains(raw, storage) || bytes.Contains(raw, []byte("\ufffd")) {
						t.Fatal("fixture changed the original JSON path/storage tokens before dispatch")
					}
					t.Logf("owned original JSON request sha256=%x operation=%s transport=%s", sha256.Sum256(raw), operation, transport)
					if out, e := p.callOriginalMethod(context.Background(), transport, raw); e == nil {
						t.Fatalf("non-scalar path escaped the declared UTF-8 filesystem boundary: result=%s original_request_sha256=%x", out.Result, sha256.Sum256(raw))
					}
					bytes, e := os.ReadFile(replacement)
					if e != nil || string(bytes) != string(original) {
						t.Fatal("unpaired token changed a different legitimate file", e)
					}
				}
			}
		}
	}
	emoji := filepath.Join(root, "\U0001f600")
	if e := sharedLocalStorageFixtureWrite(t, emoji, []byte("paired emoji original"), 0600); e != nil {
		t.Fatal(e)
	}
	item := &plugins.ModuleFileItem{Fields: map[string]json.RawMessage{"path": json.RawMessage(prefix[:len(prefix)-1] + `\ud83d\ude00"`), "storage": json.RawMessage(`"\u006cocal"`)}}
	paired, e := json.Marshal(plugins.LocalStorageRequest{FileItem: item})
	if e != nil || !bytes.Contains(paired, []byte(`\ud83d\ude00`)) || !bytes.Contains(paired, []byte(`\u006cocal`)) {
		t.Fatal("paired fixture changed original escape tokens", e)
	}
	t.Logf("owned paired JSON payload sha256=%x", sha256.Sum256(paired))
	if out, e := sharedLocalStorageCall(t, p, plugins.LocalStorageExistsCapability, plugins.LocalStorageRequest{FileItem: item}); e != nil || string(out) != "true" {
		t.Fatal("paired JSON path was not preserved", e, string(out))
	}
	if out, e := sharedLocalStorageCall(t, p, plugins.LocalStorageDeleteCapability, plugins.LocalStorageRequest{FileItem: item}); e != nil || string(out) != "true" {
		t.Fatal("ordinary paired path delete", e, string(out))
	}
	if _, e := os.Stat(emoji); !os.IsNotExist(e) {
		t.Fatal("paired deletion did not occur", e)
	}
	if bytes, e := os.ReadFile(replacement); e != nil || string(bytes) != string(original) {
		t.Fatal("paired path affected replacement", e)
	}
}
