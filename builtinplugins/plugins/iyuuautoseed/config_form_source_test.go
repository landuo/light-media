package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

type configSourceInventory struct {
	SiteIDs []any        `json:"site_ids"`
	Sites   []SiteOption `json:"sites"`
	Custom  struct {
		Sites []SiteOption `json:"sites"`
	} `json:"custom"`
	Downloaders            []string `json:"downloaders"`
	DestinationDownloaders []string `json:"destination_downloaders"`
}

func TestSourceConfigForm222(t *testing.T) {
	var fixture struct {
		Inventory configSourceInventory `json:"inventory"`
		Inits     []struct {
			Name, Error string
			Input       map[string]any
			Reads       []string
			Writes      []map[string]any
			Jobs        []any
			State       bool
			Services    []Job
		}
		Deletions []struct {
			Name, Error string
			Live, Saved map[string]any
			Deleted     any
			Writes      []map[string]any
			State       bool
		}
		Forms []struct {
			Name     string
			Form     []any
			Defaults map[string]any
		}
	}
	file, err := os.Open("reference/config-form-source-2.22.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	inventory := fixture.Inventory
	for _, row := range fixture.Inits {
		t.Run("init/"+row.Name, func(t *testing.T) {
			cfg := NewConfig()
			reads := []string{}
			writes := []map[string]any{}
			jobs := []any{}
			write, err := cfg.PrepareInit(row.Input, func() ([]any, error) {
				reads = append(reads, "managed", "custom")
				return inventory.SiteIDs, nil
			})
			assertConfigFault(t, err, row.Error)
			if write != nil {
				writes = append(writes, write)
			}
			if err == nil && cfg.ShouldSchedule() {
				if cfg.ConsumeOnlyOnce() {
					jobs = append(jobs, map[string]any{"id": "auto_seed", "kind": "date", "delay_seconds": json.Number("3")})
				}
				cfg.ClearCacheFlag()
				writes = append(writes, configSnapshot(t, cfg))
				jobs = append(jobs, map[string]any{"id": "check_recheck", "kind": "interval", "interval_seconds": json.Number("180")})
			}
			if !reflect.DeepEqual(reads, row.Reads) || !reflect.DeepEqual(jobs, row.Jobs) || cfg.State() != row.State {
				t.Fatalf("reads/jobs/state = %v/%v/%v, want %v/%v/%v", reads, jobs, cfg.State(), row.Reads, row.Jobs, row.State)
			}
			assertConfigJSON(t, writes, row.Writes)
			if err == nil {
				services, err := cfg.Services()
				if err != nil || !reflect.DeepEqual(services, row.Services) {
					t.Fatalf("public services = %v, %v, want %v", services, err, row.Services)
				}
			}
		})
	}
	for _, row := range fixture.Deletions {
		t.Run("delete/"+row.Name, func(t *testing.T) {
			cfg := NewConfig()
			if _, err := cfg.PrepareInit(row.Live, func() ([]any, error) { return inventory.SiteIDs, nil }); err != nil {
				t.Fatal(err)
			}
			cfg.ClearCacheFlag()
			changed, err := cfg.SiteDeleted(row.Saved, row.Deleted)
			assertConfigFault(t, err, row.Error)
			writes := []map[string]any{}
			if changed {
				writes = append(writes, configSnapshot(t, cfg))
			}
			if cfg.State() != row.State {
				t.Fatalf("state = %v, want %v", cfg.State(), row.State)
			}
			assertConfigJSON(t, writes, row.Writes)
		})
	}
	for _, row := range fixture.Forms {
		t.Run("form/"+row.Name, func(t *testing.T) {
			form, defaults, err := Form(FormInventory{
				Sites: inventory.Sites, CustomSites: inventory.Custom.Sites,
				Downloaders: inventory.Downloaders, DestinationDownloaders: inventory.DestinationDownloaders,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertConfigJSON(t, form, row.Form)
			assertConfigJSON(t, defaults, row.Defaults)
			if Commands() != nil || Routes() != nil || Page() != nil {
				t.Fatal("source empty hooks changed")
			}
		})
	}
}

func configSnapshot(t *testing.T, c Config) map[string]any {
	t.Helper()
	snapshot, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertConfigFault(t *testing.T, err error, class string) {
	t.Helper()
	var fault *SourceFault
	if class == "" && err != nil || class != "" && (!errors.As(err, &fault) || fault.Class != class) {
		t.Fatalf("error = %v, want %q", err, class)
	}
}

func assertConfigJSON(t *testing.T, got, want any) {
	t.Helper()
	actual, err := cloneJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := cloneJSON(want)
	if err != nil {
		t.Fatal(err)
	}
	if !configJSONEqual(actual, expected) {
		actualRaw, _ := json.Marshal(actual)
		expectedRaw, _ := json.Marshal(expected)
		t.Fatalf("got %s\nwant %s", actualRaw, expectedRaw)
	}
}

func configJSONEqual(got, want any) bool {
	switch actual := got.(type) {
	case json.Number:
		expected, ok := want.(json.Number)
		return ok && scalarEqual(actual, expected)
	case []any:
		expected, ok := want.([]any)
		if !ok || len(actual) != len(expected) {
			return false
		}
		for i, item := range actual {
			if !configJSONEqual(item, expected[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		expected, ok := want.(map[string]any)
		if !ok || len(actual) != len(expected) {
			return false
		}
		for key, item := range actual {
			other, ok := expected[key]
			if !ok || !configJSONEqual(item, other) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(actual, want)
	}
}
