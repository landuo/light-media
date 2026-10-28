package artifacts

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestSystemVersionFixedPluginHelperOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/standard-market/system-version-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Host       string
			Value      json.RawMessage
			Compatible bool
			Message    string
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) < 200 {
		t.Fatal("fixed source oracle missing")
	}
	for _, row := range oracle.Cases {
		ok, message := CheckSystemVersion(row.Value, row.Host)
		if ok != row.Compatible || message != row.Message {
			t.Errorf("fixed source mismatch host=%s constraint=%s got=(%v,%q) want=(%v,%q)", row.Host, row.Value, ok, message, row.Compatible, row.Message)
		}
	}
}

func TestSystemVersionAcquisitionPreflightAndHistoricalRelease(t *testing.T) {
	req, client, index, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
	index.SystemVersion = json.RawMessage(`">=3"`)
	if _, err := Acquire(context.Background(), req, client); err == nil || *hits != 0 {
		t.Fatal("incompatible current range reached executable download")
	}
	req.DeveloperLocalWatch = true
	if _, err := Acquire(context.Background(), req, client); err == nil || *hits != 0 {
		t.Fatal("local DEV watcher exception leaked into HTTP acquisition")
	}
	req.DeveloperLocalWatch = false
	if _, err := InspectCompatible(context.Background(), req, client); err == nil || *hits != 0 {
		t.Fatal("existing class compatibility check ignored latest range")
	}
	old := index.Releases[0]
	old.Version, old.Manifest.Version = "0.9.0", "0.9.0"
	index.Releases = append(index.Releases, old)
	req.ReleaseVersion = "0.9.0"
	if _, err := Acquire(context.Background(), req, client); err != nil || *hits != 1 {
		t.Fatal("fixed explicit old Release incorrectly consumed latest system_version", err)
	}
}
