package autosignin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

const cacheRefinementRoot = "testdata/source-oracles/autosignin-source-edge-cache-refinement-20261106/"

func TestCacheRefinementActualOriginalCanonicalWriter(t *testing.T) {
	raw, err := os.ReadFile(cacheRefinementRoot + "source-canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rows []struct {
			Input string
			Bytes string `json:"bytes_b64"`
			Error any
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, row := range fixture.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if row.Error != nil {
				t.Fatal("source fixture error", row.Error)
			}
			value, err := parseSourceHTTPJSON(row.Input)
			if err != nil {
				t.Fatal(err)
			}
			cache := value.(sourceHTTPMap)
			storage := &independentCacheCapture{}
			posts := 0
			out, err := tjuptSubmit(context.Background(), Object{"name": "Owned", "cookie": "synthetic"}, SiteVisitHooks{HTTP: independentCacheHTTP(func(context.Context, PageRequest) (*PageResponse, error) {
				posts++
				return &PageResponse{Status: 200, Text: "这是您的首次签到，本次签到获得1个魔力值。"}, nil
			}), Tjupt: &TjuptDependencies{Cache: storage}}, "owned-answer", cache.values, append([]string{}, cache.keys...), "new-hash")
			want, decodeErr := base64.StdEncoding.DecodeString(row.Bytes)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if err != nil || !Truth(out.State) || posts != 1 || storage.writes != 1 || !bytes.Equal(storage.bytes, want) {
				t.Fatalf("source write bytes differ; error=%v state=%v POST=%d write=%d\ninput=%s\ngot=%s\nwant=%s", err, out.State, posts, storage.writes, row.Input, storage.bytes, want)
			}
		})
	}
}

func TestCacheRefinementNativeOwnedFileBytes(t *testing.T) {
	// Actual existing os.Root-backed cache writer/read. The shared TEMP setting response is
	// synthetic; this is native filesystem, not App/Main authorization proof.
	for _, text := range []string{`{"old":"\ud800","\udfff":1,"�":2}`, `{"old":[NaN,{"x":Infinity,"y":-Infinity}]}`, `{"old":1e2,"negative":-0.0}`, `{"old":"中文😀\u007f\b/"}`} {
		t.Run(text, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			grants := 0
			cache := &sdkTjuptCache{backend: sdkBackend{ctx: ctx, host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method != sdk.SettingsReadCapability {
					t.Fatalf("unexpected capability %s", method)
				}
				grants++
				return json.Marshal(sdk.SettingsHostResultV1{SchemaVersion:1,Key:"TEMP_PATH",Value:mustTempJSON(root)})
			}}}
			if err := cache.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			parsed, err := parseSourceHTTPJSON(text)
			if err != nil {
				t.Fatal(err)
			}
			value := parsed.(sourceHTTPMap)
			encoded, err := encodeOrderedCache(value.values, value.keys)
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.Write(ctx, encoded); err != nil {
				t.Fatal(err)
			}
			stored, err := cache.Read(ctx)
			if err != nil || !bytes.Equal(stored, encoded) || grants != 1 {
				t.Fatalf("native file bytes/grant mismatch: %v/%d/%s", err, grants, stored)
			}
			if _, err := parseSourceHTTPJSON(string(stored)); err != nil {
				t.Fatal("source cache cannot read its saved file", err)
			}
		})
	}
}
