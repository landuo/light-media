package app

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestPluginLogRotationFixedConfigurationOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-plugin-logs/file-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Pydantic string `json:"pydantic_version"`
		Integers []struct {
			Raw   string
			OK    bool
			Value *string
		} `json:"integer_inputs"`
		Rotation []struct {
			Name         string
			MiB, Backups int64
			Prefix       bool `json:"current_keeps_prefix"`
			Previous     bool `json:"previous_backup_preserved"`
			Files        []string
		} `json:"configured_rotation"`
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Pydantic != "2.13.4" || len(ref.Integers) != 15 || len(ref.Rotation) != 5 {
		t.Fatal("fixed configuration oracle missing")
	}
	for i, sample := range ref.Integers {
		for _, key := range []string{"LOG_MAX_FILE_SIZE", "LOG_BACKUP_COUNT"} {
			got, err := PluginLogRotationEnvironment(func(name string) (string, bool) { return sample.Raw, name == key })
			if (err == nil) != sample.OK {
				t.Fatal("source environment int acceptance differs", i, key)
			}
			if sample.OK {
				n := got.MaxMiB
				if key == "LOG_BACKUP_COUNT" {
					n = got.Backups
				}
				if n == nil || sample.Value == nil || n.String() != *sample.Value {
					t.Fatal("source integer value differs", i, key)
				}
			}
		}
	}
	for _, sample := range ref.Rotation {
		t.Run(sample.Name, func(t *testing.T) {
			a := newTestApp(t)
			a.pluginLogRotation = &PluginLogRotation{MaxMiB: big.NewInt(sample.MiB), Backups: big.NewInt(sample.Backups)}
			dir := filepath.Join(a.Dir, "logs", "plugins")
			path := filepath.Join(dir, "owned.log")
			if err := os.WriteFile(path, []byte("owned prefix\n"), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			err = f.Truncate((1 << 20) - 1)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path+".1", []byte("owned previous backup\n"), 0600); err != nil {
				t.Fatal(err)
			}
			entry, _ := json.Marshal(map[string]any{"plugin_instance_id": "Owned", "module": "plugins", "level": "INFO", "message": "中文界", "time": now()})
			a.logs.fileMu.Lock()
			err = a.logs.writePluginProjection(entry)
			a.logs.fileMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := os.ReadFile(path + ".1")
			if err != nil {
				t.Fatal(err)
			}
			if bytes.HasPrefix(content, []byte("owned prefix\n")) != sample.Prefix || bytes.Equal(backup, []byte("owned previous backup\n")) != sample.Previous {
				t.Fatal("zero/negative rotation changed append or backup data")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, sample.Files) {
				t.Fatal("configured source backup filenames differ", names, sample.Files)
			}
		})
	}
}

func TestPluginLogRotationReusesHostSettingsAndCopiesEnvironment(t *testing.T) {
	override, err := PluginLogRotationEnvironment(func(name string) (string, bool) {
		if name == "LOG_BACKUP_COUNT" {
			return "0", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: t.TempDir(), PluginLogRotation: override})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	override.Backups.SetInt64(99)
	a.cfgMu.Lock()
	a.config.LogMaxMiB = 3
	a.config.LogRetainFiles = 4
	a.cfgMu.Unlock()
	mib, backups := a.pluginProjectionBudget()
	if mib.Int64() != 3 || backups.Sign() != 0 {
		t.Fatal("trusted environment did not override host settings immutably", mib, backups)
	}
	a.pluginLogRotation = nil
	mib, backups = a.pluginProjectionBudget()
	if mib.Int64() != 3 || backups.Int64() != 4 {
		t.Fatal("existing host settings were ignored", mib, backups)
	}
}
