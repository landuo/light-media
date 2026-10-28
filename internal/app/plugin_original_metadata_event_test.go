package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalMetadataEventFixedSourceTraversal(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/file-consumer-source-oracle.json")
	var oracle struct {
		Scrape struct {
			Cases []struct {
				Name           string
				Input          json.RawMessage
				StoragePresent bool `json:"storage_present"`
				Bluray         bool
				Calls          []map[string]any
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Scrape.Cases) != 8 {
		t.Fatal("fixed MediaChain.scrape_metadata_event oracle", err)
	}
	for _, sample := range oracle.Scrape.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			calls := []map[string]any{}
			makeItem := func(storage, path string) map[string]any {
				kind := "dir"
				if filepath.Ext(path) != "" {
					kind = "file"
				}
				return map[string]any{"storage": storage, "path": path, "type": kind}
			}
			storage := originalMetadataEventStorage{
				getItem: func(item map[string]any) (bool, error) {
					calls = append(calls, map[string]any{"method": "get_item", "fileitem": item})
					return sample.StoragePresent, nil
				},
				getParent: func(item map[string]any) (map[string]any, error) {
					return makeItem(sourceText(item["storage"]), filepath.Dir(sourceText(item["path"]))), nil
				},
				isBluray: func(item map[string]any) (bool, error) {
					calls = append(calls, map[string]any{"method": "is_bluray_folder", "fileitem": item})
					return sample.Bluray, nil
				},
				getFileItem: func(storage, path string) (map[string]any, error) {
					calls = append(calls, map[string]any{"method": "get_file_item", "storage": storage, "path": path})
					if strings.Contains(path, "missing") {
						return nil, nil
					}
					return makeItem(storage, path), nil
				},
				scrape: func(kwargs map[string]any) error {
					calls = append(calls, map[string]any{"method": "scrape_metadata", "kwargs": kwargs})
					return nil
				},
			}
			value, err := decodeSourceJSON(sample.Input)
			if err != nil {
				t.Fatal(err)
			}
			if err = walkOriginalMetadataEvent(sourceMap(value), storage); err != nil {
				t.Fatal(err)
			}
			actual, _ := json.Marshal(calls)
			expected, _ := json.Marshal(sample.Calls)
			if string(actual) != string(expected) {
				t.Fatalf("source calls mismatch\nactual %s\nexpected %s", actual, expected)
			}
		})
	}
}
