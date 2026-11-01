package plugins

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
)

// Reuse the one complete source raw file; do not maintain a second projection.
//go:embed downloadercommands/testdata/source-tr-numeric.json
var sourceSharedTRNumeric []byte

// SDK transport remains transparent even for numeric spellings which the
// original host-side preparation rejects. This is a recording HostCaller gate,
// not a claim of real downloader, native RPC or host lifecycle execution.
func TestSharedTRNumericOriginalStringsAndSourceClassification(t *testing.T) {
	h := sha256.Sum256(sourceSharedTRNumeric)
	if hex.EncodeToString(h[:]) != "1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d" {
		t.Fatal("not complete original numeric raw14")
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		CaseCount    int    `json:"case_count"`
		Cases        []struct {
			ID    string `json:"id"`
			Input struct {
				Method string         `json:"method"`
				Args   map[string]any `json:"args"`
			} `json:"input"`
			Outcome struct {
				Kind  string `json:"kind"`
				Value bool   `json:"value"`
				Class string `json:"class"`
			} `json:"outcome"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(sourceSharedTRNumeric, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.CaseCount != 14 || len(oracle.Cases) != 14 {
		t.Fatal("numeric source scope")
	}
	for _, c := range oracle.Cases {
		t.Run(c.ID, func(t *testing.T) {
			if c.Input.Method != "change_torrent" {
				t.Fatal("unselected method")
			}
			ctx := context.WithValue(context.Background(), struct{}{}, "owned-numeric-context")
			x := c.Input.Args
			want := map[string]any{
				"op": "invoke", "downloader_id": " Owned-ID ", "hash_string": x["hash_string"],
				"upload_limit": x["upload_limit"], "download_limit": x["download_limit"],
				"ratio_limit": x["ratio_limit"], "seeding_time_limit": x["seeding_time_limit"],
			}
			calls := 0
			host := func(actual context.Context, capability string, request json.RawMessage) (json.RawMessage, error) {
				calls++
				if actual != ctx || capability != TransmissionChangeCapability {
					t.Fatal("context/capability was changed")
				}
				var got map[string]any
				if err := json.Unmarshal(request, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("SDK normalized/dropped original numeric spelling", got, want)
				}
				switch c.Outcome.Kind {
				case "return":
					return json.Marshal(map[string]any{"value": c.Outcome.Value})
				case "uncaught_exception":
					if c.Outcome.Class != "ValueError" {
						t.Fatal("unexpected original error class")
					}
					return json.Marshal(map[string]any{"kind": "source_error", "source_error": c.Outcome.Class})
				default:
					t.Fatal("unsupported original outcome")
					return nil, nil
				}
			}
			tr := SharedTransmissionCommands{Host: host, DownloaderID: " Owned-ID "}
			value, err := tr.ChangeTorrent(ctx, x["hash_string"], x["upload_limit"], x["download_limit"], x["ratio_limit"], x["seeding_time_limit"])
			if calls != 1 {
				t.Fatal("SDK suppressed or replayed host dispatch", calls)
			}
			if c.Outcome.Kind == "uncaught_exception" {
				source, ok := err.(*TRChangeSourceError)
				if !ok || source.Class != c.Outcome.Class || value {
					t.Fatal("lost original host-side error classification", value, err)
				}
			} else if err != nil || value != c.Outcome.Value {
				t.Fatal("lost original host-side result", value, err)
			}
		})
	}
}
