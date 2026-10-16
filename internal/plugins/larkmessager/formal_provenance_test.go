package larkmessager

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This inventory pins the promoted leaf and the intact frozen candidate; it is
// not evidence of App registration, migrated data or a real Lark delivery.
func TestFormalLarkSourceInventory(t *testing.T) {
	root := filepath.Clean("../../..")
	raw, e := os.ReadFile(filepath.Join(root, "testdata/plugins/lark-messager-0.9.1/formal-manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "ed055f57fb4fc80c15ab001b35a14bc52fbb55e9352e81c64102bed4ce2e14db" {
		t.Fatal("formal promotion manifest changed without review")
	}
	var m struct {
		Version         int
		Plugin          string
		OriginalVersion string `json:"original_version"`
		Files, Inputs   map[string]string
	}
	if json.Unmarshal(raw, &m) != nil || m.Version != 1 || m.Plugin != "LarkMessager" || m.OriginalVersion != "0.9.1" || len(m.Files) != 41 || len(m.Inputs) != 126 {
		t.Fatal("incomplete formal Lark inventory")
	}
	for _, files := range []map[string]string{m.Files, m.Inputs} {
		for name, want := range files {
			if filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") {
				t.Fatal("unsafe evidence path", name)
			}
			b, e := os.ReadFile(filepath.Join(root, name))
			if e != nil {
				t.Fatal(name, e)
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("formal source/reference changed", name)
			}
		}
	}
}

func TestFormalLarkMatchesFrozenCandidate(t *testing.T) {
	raw, e := os.ReadFile(originalFixture("reference/candidate-go-checksums.json"))
	if e != nil {
		t.Fatal(e)
	}
	var goFiles map[string]string
	if json.Unmarshal(raw, &goFiles) != nil || len(goFiles) != 31 {
		t.Fatal("candidate Go inventory changed")
	}
	for name, want := range goFiles {
		original, e := os.ReadFile(originalFixture(name))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(original)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("frozen candidate changed", name)
		}
		text := string(original)
		if strings.HasSuffix(name, "_test.go") {
			for _, p := range []string{"testdata/source-oracle.jsonl.gz", "reference/python-printable15.json", "reference/pydantic-int-whitespace.json"} {
				text = strings.ReplaceAll(text, "os.Open(\""+p+"\")", "os.Open(originalFixture(\""+p+"\"))")
				text = strings.ReplaceAll(text, "os.ReadFile(\""+p+"\")", "os.ReadFile(originalFixture(\""+p+"\"))")
			}
		}
		if name == "sdk.go" {
			const signature = "func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {"
			if strings.Count(text, signature) != 1 {
				t.Fatal("SDK signature changed")
			}
			text = strings.Replace(text, signature, signature+"\n\tif method != \"stop\" && host == nil { return nil, ErrDependency }", 1)
		}
		adapted, e := format.Source([]byte(text))
		if e != nil {
			t.Fatal(e)
		}
		formal, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(adapted, formal) {
			t.Fatal("unlisted formal behavior change", name)
		}
	}
	for _, name := range []string{"form.json", "page-templates.json", "test-card.json", "mime-registry.json", "source-contract.json"} {
		original, e := os.ReadFile(originalFixture(name))
		if e != nil {
			t.Fatal(e)
		}
		formal, e := os.ReadFile(name)
		if e != nil || !bytes.Equal(original, formal) {
			t.Fatal("embed/source contract differs", name, e)
		}
	}
}

func TestFormalLarkOriginalCorpusInventory(t *testing.T) {
	f, e := os.Open(originalFixture("testdata/source-oracle.jsonl.gz"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	d := json.NewDecoder(z)
	counts := map[string]int{}
	total := 0
	for {
		var row struct{ Kind string }
		if e = d.Decode(&row); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		counts[row.Kind]++
		total++
	}
	want := map[string]int{"admin_work": 36, "attachment_model": 29, "callback": 19, "card": 251, "card_action": 3, "cardkit": 67, "client_calls": 15, "crypto": 12, "crypto_decrypt": 20, "crypto_signature": 27, "endpoint_calls": 48, "form": 1, "init": 5, "legacy_button": 3, "legacy_card": 4, "legacy_reply": 16, "message_parser": 20, "mime_guess": 480, "module_calls": 26, "module_response_ids": 6, "multi_calls": 40, "page": 6, "page_template": 8, "python_value": 29, "receive": 42, "remote_image": 49, "resolve_permission": 3, "stream_helpers": 10, "sync_config": 22, "target": 30, "test_card": 1, "webhook": 48}
	if total != 1376 || len(counts) != len(want) {
		t.Fatal("source corpus changed", total, counts)
	}
	for k, n := range want {
		if counts[k] != n {
			t.Fatal(k, counts[k], n)
		}
	}
}

func TestFormalLarkSDKMissingHostFailsWithoutPanic(t *testing.T) {
	for _, method := range []string{"invoke", "config_form", "page", "http_api"} {
		if _, e := Handle(context.Background(), method, json.RawMessage(`{}`), nil); !errors.Is(e, ErrDependency) {
			t.Fatal(method, e)
		}
	}
	r, e := Handle(context.Background(), "stop", nil, nil)
	if e != nil || string(r) != "null" {
		t.Fatal(string(r), e)
	}
}
