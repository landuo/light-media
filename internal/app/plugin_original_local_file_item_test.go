package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOriginalLocalFileItemActualFixedConstructors(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/local-file-item-oracle.json")
	var oracle struct {
		Bytes string `json:"file_bytes"`
		MTime int64  `json:"mtime_ns"`
		Cases []struct {
			Name      string
			Directory bool
			Expected  map[string]json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 7 {
		t.Fatal("fixed local FileItem oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, sample.Name)
			if sample.Directory {
				err = os.Mkdir(path, 0700)
			} else {
				err = os.WriteFile(path, []byte(oracle.Bytes), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(0, oracle.MTime)
			if err = os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			item, err := originalLocalFileItem(path)
			if err != nil {
				t.Fatal(err)
			}
			var sourcePath string
			if json.Unmarshal(sample.Expected["path"], &sourcePath) != nil {
				t.Fatal("source path")
			}
			sample.Expected["path"], _ = json.Marshal(strings.ReplaceAll(sourcePath, "__ROOT__", dir))
			want, _ := json.Marshal(sample.Expected)
			got, _ := json.Marshal(item)
			if !originalBaseJSONEqual(got, want) {
				t.Fatal("source FileItem fields/defaults differ", string(got), string(want))
			}
			clone := item.Clone()
			clone.Fields["name"] = json.RawMessage(`"changed"`)
			if string(item.Fields["name"]) == `"changed"` {
				t.Fatal("explicit FileItem clone shares fields")
			}
		})
	}
}
