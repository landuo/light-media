package autosignin

import (
	"context"
	"encoding/json"
	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/autosignin/pyvalue"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestActualRequestsOCRForm13(t *testing.T) {
	raw, err := os.ReadFile("reference/ocr-form13.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct{ Cases []struct{ Raw, Body string } }
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 13 {
		t.Fatal(len(source.Cases))
	}
	for _, c := range source.Cases {
		t.Run(c.Raw, func(t *testing.T) {
			values, err := pyvalue.RequestFormValues(json.RawMessage(c.Raw))
			if err != nil {
				t.Fatal(err)
			}
			pairs := []string{}
			for _, v := range values {
				pairs = append(pairs, "imagestring="+url.QueryEscape(v))
			}
			if got := strings.Join(pairs, "&"); got != c.Body {
				t.Fatalf("source form got %s want %s", got, c.Body)
			}
		})
	}
}
func TestTjuptCacheSharedTemporaryNamespaceWithoutHistory(t *testing.T) {
	directory := t.TempDir()
	targets := []string{}
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != sdk.SettingsReadCapability {
			t.Fatalf("cache touched non-file/history resource %s", method)
		}
		var in struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		targets = append(targets, in.Key)
		return json.Marshal(sdk.SettingsHostResultV1{SchemaVersion: 1, Key: in.Key, Value: json.RawMessage(strconv.Quote(directory))})
	}
	base := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: host}}
	clone := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: host}}
	if err := base.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Read(context.Background()); !isCacheIO(err) {
		t.Fatalf("native missing file must be source-caught IO: %v", err)
	}
	bytes := []byte(`{"ordered-last": 1, "ordered-first": 2}`)
	if err := base.Write(context.Background(), bytes); err != nil {
		t.Fatal(err)
	}
	if err := clone.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := clone.Read(context.Background())
	if err != nil || string(got) != string(bytes) {
		t.Fatalf("clone global answer bytes/order changed: %s,%v", got, err)
	}
	if len(targets) != 2 || targets[0] != "TEMP_PATH" || targets[1] != "TEMP_PATH" {
		t.Fatal(targets)
	}
}
