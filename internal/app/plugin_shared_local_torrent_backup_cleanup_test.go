package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type ownedBackupCleanupNode struct {
	Path        string  `json:"path"`
	Kind        string  `json:"kind"`
	AgeSeconds  float64 `json:"age_seconds,omitempty"`
	Mtime       float64 `json:"mtime,omitempty"`
	UnlinkError string  `json:"unlink_error,omitempty"`
}

type ownedBackupCleanupMemory struct {
	rootExists bool
	order      []string
	nodes      map[string]ownedBackupCleanupNode
	trace      []map[string]any
	now        time.Time
}

func (p *ownedBackupCleanupMemory) record(operation, name string, fields map[string]any) {
	fields["operation"], fields["path"] = operation, name
	p.trace = append(p.trace, fields)
}
func (p *ownedBackupCleanupMemory) snapshot() []ownedBackupCleanupNode {
	out := []ownedBackupCleanupNode{}
	for _, name := range p.order {
		if node, ok := p.nodes[name]; ok {
			out = append(out, node)
		}
	}
	return out
}
func (p *ownedBackupCleanupMemory) Exists() (bool, error) {
	p.record("exists", ".", map[string]any{"result": p.rootExists})
	return p.rootExists, nil
}
func (p *ownedBackupCleanupMemory) RGlob() ([]string, error) {
	out := []string{}
	for _, name := range p.order {
		if _, ok := p.nodes[name]; ok {
			out = append(out, name)
		}
	}
	p.record("rglob", ".", map[string]any{"pattern": "*", "result": append([]string{}, out...)})
	return out, nil
}
func (p *ownedBackupCleanupMemory) IsFile(name string) (bool, error) {
	result := p.nodes[name].Kind == "file"
	p.record("is_file", name, map[string]any{"result": result})
	return result, nil
}
func (p *ownedBackupCleanupMemory) MTime(name string) (float64, error) {
	value := p.nodes[name].Mtime
	p.record("stat", name, map[string]any{"st_mtime": value})
	return value, nil
}
func (p *ownedBackupCleanupMemory) Unlink(name string) error {
	if message := p.nodes[name].UnlinkError; message != "" {
		p.record("unlink", name, map[string]any{"result": "injected_error"})
		return errors.New(message) // Memory port only, not an OS permission probe.
	}
	p.record("unlink", name, map[string]any{"result": "memory_only"})
	delete(p.nodes, name)
	return nil
}
func (p *ownedBackupCleanupMemory) IsDir(name string) (bool, error) {
	result := p.nodes[name].Kind == "dir"
	p.record("is_dir", name, map[string]any{"result": result})
	return result, nil
}
func (p *ownedBackupCleanupMemory) IterDir(name string) ([]string, error) {
	out := []string{}
	for _, child := range p.order {
		if _, ok := p.nodes[child]; ok && path.Dir(child) == name {
			out = append(out, child)
		}
	}
	p.record("iterdir", name, map[string]any{"result": out})
	return out, nil
}
func (p *ownedBackupCleanupMemory) RemoveDir(name string) error {
	p.record("rmdir", name, map[string]any{"result": "memory_only"})
	delete(p.nodes, name)
	return nil
}
func (p *ownedBackupCleanupMemory) Now() time.Time {
	p.record("datetime.now", ".", map[string]any{"result": p.now.Format("2006-01-02T15:04:05")})
	return p.now
}
func (p *ownedBackupCleanupMemory) FromTimestamp(value float64) (time.Time, error) {
	stamp, err := originalLocalBackupTimestamp(value, time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	p.record("datetime.fromtimestamp", ".", map[string]any{"timestamp": value, "result": stamp.Format("2006-01-02T15:04:05")})
	return stamp, nil
}

func TestLocalTorrentBackupCleanupSourceOracle(t *testing.T) {
	sum := sha256.Sum256([]byte(ownedBackupCleanupSourceRaw))
	if hex.EncodeToString(sum[:]) != "7deac37768a1aae3251154c2985ed51de97b656e56cdc7c1f3aeaeb852097785" {
		t.Fatal("complete original recording bytes changed")
	}
	var source struct {
		Commit string `json:"source_commit"`
		Node   struct {
			Symbol string `json:"symbol"`
			AST    string `json:"ast_no_attributes_sha256"`
		} `json:"selected_node"`
		Cases []struct {
			ID    string `json:"id"`
			Input struct {
				Now        string `json:"now_local"`
				Days       int    `json:"days"`
				RootExists bool   `json:"root_exists"`
			} `json:"input"`
			Before, After   []ownedBackupCleanupNode
			Trace           []map[string]any
			Exception       *struct{ Class, Message string }
			RootExistsAfter bool `json:"root_exists_after"`
		}
	}
	if err := json.Unmarshal([]byte(ownedBackupCleanupSourceRaw), &source); err != nil {
		t.Fatal(err)
	}
	if source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || source.Node.Symbol != "SystemUtils.clear" || source.Node.AST != "527a9531d2f5f71bf7364dd868f017b324a848358e693d52400e82ad501a964a" || len(source.Cases) != 6 {
		t.Fatal("fixed complete source recording provenance")
	}
	for _, sample := range source.Cases {
		t.Run(sample.ID, func(t *testing.T) {
			now, err := time.Parse("2006-01-02T15:04:05", sample.Input.Now)
			if err != nil {
				t.Fatal(err)
			}
			p := &ownedBackupCleanupMemory{rootExists: sample.Input.RootExists, nodes: map[string]ownedBackupCleanupNode{}, trace: []map[string]any{}, now: now}
			for _, node := range sample.Before {
				p.order = append(p.order, node.Path)
				p.nodes[node.Path] = node
			}
			err = clearOriginalLocalBackupPort(p, p, sample.Input.Days)
			if sample.Exception == nil {
				if err != nil {
					t.Fatal(err)
				}
			} else if sample.Exception.Class != "PermissionError" || err == nil || err.Error() != sample.Exception.Message {
				t.Fatalf("recorded injected exception was swallowed/changed: %v", err)
			}
			// Normalize only Go slice representations to JSON. Expected traces and
			// before/after trees come intact from the actual original AST recording.
			actualJSON, err := json.Marshal(p.trace)
			if err != nil {
				t.Fatal(err)
			}
			var actual []map[string]any
			if err := json.Unmarshal(actualJSON, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, sample.Trace) || !reflect.DeepEqual(p.snapshot(), sample.After) || p.rootExists != sample.RootExistsAfter {
				t.Fatalf("original clear trace/state differs\nactual=%s\nsource=%v\nafter=%v want=%v", actualJSON, sample.Trace, p.snapshot(), sample.After)
			}
		})
	}
	// Static arithmetic assertions, not additional Python oracle executions.
	t.Run("negative-naive-days-and-path-components", func(t *testing.T) {
		now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
		if originalLocalBackupAgeDays(now, now.Add(time.Microsecond)) != -1 || originalLocalBackupAgeDays(now, now.Add(24*time.Hour+time.Microsecond)) != -2 || originalLocalBackupAgeDays(now, now.Add(24*time.Hour)) != -1 {
			t.Fatal("negative timedelta.days must floor")
		}
		left := time.Date(2026, 10, 31, 12, 0, 0, 0, time.FixedZone("owned-local", 3600))
		right := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		if originalLocalBackupAgeDays(left, right) != 1 {
			t.Fatal("naive datetime subtraction must use local civil fields")
		}
		if !originalLocalBackupPathLess("a/b", "a-") || originalLocalBackupPathLess("a/b", "a") {
			t.Fatal("path component order must not be slash-string lexical order")
		}
	})
}

func TestLocalTorrentBackupCleanupOwnedSandbox(t *testing.T) {
	// This prepared test operates only on newly owned regular files/directories.
	// Its eventual execution/deletion window belongs to the coordinator.
	home := filepath.Join(t.TempDir(), ".light-media-owned")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	a := &App{Dir: home}
	if err := a.clearOriginalLocalTorrentBackup(3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "temp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing sandbox cleanup created a directory", err)
	}
	root := filepath.Join(home, "temp", "plugin-torrent-backup")
	paths := map[string]string{
		filepath.Join(root, "old", "nested", "owned.torrent"):     "owned old material",
		filepath.Join(root, "fresh.torrent"):                      "owned fresh material",
		filepath.Join(home, "backup", "owned.torrent"):            "readonly backup sentinel",
		filepath.Join(home, "temp", "other", "owned.tmp"):         "other temp sentinel",
		filepath.Join(home, "cache", "torrents", "owned.torrent"): "persistent cache sentinel",
	}
	for name, content := range paths {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-5 * 24 * time.Hour)
		if name == filepath.Join(root, "fresh.torrent") {
			stamp = time.Now()
		}
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty", "child"), 0700); err != nil {
		t.Fatal(err)
	}
	// This is an ordinary readonly enumeration of the same owned fixture, not
	// a path-escape/permission probe. All immediate-directory starts precede
	// the deeper old/nested start, regardless of root entry provider order.
	listing := &originalLocalBackupCleanupFS{root: root, observed: map[string]os.FileInfo{}}
	if exists, err := listing.Exists(); err != nil || !exists {
		t.Fatal("owned sandbox listing", err)
	}
	names, listErr := listing.RGlob()
	_ = listing.held.Close()
	if listErr != nil {
		t.Fatal(listErr)
	}
	index := func(want string) int {
		for i, name := range names {
			if name == want {
				return i
			}
		}
		return -1
	}
	if sibling, deep := index("empty/child"), index("old/nested/owned.torrent"); sibling < 0 || deep < 0 || sibling >= deep {
		t.Fatalf("native rglob starting-point order differs: %v", names)
	}
	if err := a.clearOriginalLocalTorrentBackup(3); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old", "empty"} {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("old owned file/empty tree not removed", name, err)
		}
	}
	for name, content := range paths {
		if name == filepath.Join(root, "old", "nested", "owned.torrent") {
			continue
		}
		if raw, err := os.ReadFile(name); err != nil || string(raw) != content {
			t.Fatal("fresh or outside-sandbox content changed", name, err)
		}
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatal("source leaves selected root", err)
	}
	if err := a.clearOriginalLocalTorrentBackup(3); err != nil {
		t.Fatal("ordinary second cleanup", err)
	}
	t.Run("optional-days-default-zero-custom-and-cold-storage", func(t *testing.T) {
		// Real owned Settings/SQLite persistence, without App.Start, listeners,
		// worker installation or downloader construction. Not a UI/HTTP gate.
		dir := t.TempDir()
		var current *App
		open := func() {
			t.Helper()
			var err error
			current, err = New(Options{DataDir: dir, SafeMode: true})
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			if current != nil {
				_ = current.Close()
			}
		})
		open()
		disableResetFixtureStatistics(t, current)
		if current.Settings().TempFileDays != nil || current.originalLocalTorrentBackupTempDays() != 3 {
			t.Fatal("absent source option must retain default 3")
		}
		for _, days := range []int{0, 7, -1} {
			settings := current.Settings()
			configured := days
			settings.TempFileDays = &configured
			if err := current.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			if current.originalLocalTorrentBackupTempDays() != days {
				t.Fatal("explicit days was defaulted", days)
			}
			projection := current.publicSettings()
			delete(projection, "secret_state")
			delete(projection, "temp_file_days")
			raw, err := json.Marshal(projection)
			if err != nil {
				t.Fatal(err)
			}
			var omitted Settings
			if err := json.Unmarshal(raw, &omitted); err != nil {
				t.Fatal(err)
			}
			current.preserveSecrets(&omitted)
			if omitted.TempFileDays == nil || *omitted.TempFileDays != days {
				t.Fatal("ordinary omitted-field save lost hidden option", days)
			}
			if err := current.saveSettings(context.Background(), omitted); err != nil {
				t.Fatal(err)
			}
			if err := current.Close(); err != nil {
				t.Fatal(err)
			}
			current = nil
			open()
			if value := current.Settings().TempFileDays; value == nil || *value != days || current.originalLocalTorrentBackupTempDays() != days {
				t.Fatal("actual cold config lost optional days", days)
			}
		}
	})
}

// Complete original stdout, byte-for-byte. This is an actual source-AST result
// with recording Path/clock ports, not an OS deletion or permission oracle.
const ownedBackupCleanupSourceRaw = `{
  "layer": "complete_original_AST_with_recording_path_and_controlled_clock_ports",
  "source_commit": "8a83efeacd3e26b82be495e17d50146e31c96a60",
  "python": "3.12.14 (main, Aug 25 2026, 14:00:49) [Clang 22.1.3 ]",
  "timezone": {
    "tzname": [
      "UTC",
      "UTC"
    ],
    "timezone": 0
  },
  "real_filesystem_deletions": 0,
  "clock": "fixed local-naive input; real stdlib datetime arithmetic/fromtimestamp; no module patch",
  "limits": [
    "not actual OS deletion",
    "not whole startup cleanup",
    "not filesystem permissions/inodes/symlink probes",
    "not torrent parsing or fastresume reconstruction"
  ],
  "selected_node": {
    "symbol": "SystemUtils.clear",
    "start_line": 977,
    "end_line": 994,
    "source_bytes_sha256": "b740d618bad8b1fc51e4ce77f282f438a28e1105173767438a2007c1f964981c",
    "ast_no_attributes_sha256": "527a9531d2f5f71bf7364dd868f017b324a848358e693d52400e82ad501a964a"
  },
  "cases": [
    {
      "id": "missing_root",
      "input": {
        "id": "missing_root",
        "now_local": "2026-10-31T12:00:00",
        "days": 3,
        "root_exists": false,
        "nodes": []
      },
      "before": [],
      "return": null,
      "exception": null,
      "trace": [
        {
          "operation": "exists",
          "path": ".",
          "result": false
        }
      ],
      "after": [],
      "root_exists_after": false
    },
    {
      "id": "exactly_three_days",
      "input": {
        "id": "exactly_three_days",
        "now_local": "2026-10-31T12:00:00",
        "days": 3,
        "root_exists": true,
        "nodes": [
          {
            "path": "material.torrent",
            "kind": "file",
            "age_seconds": 259200
          }
        ]
      },
      "before": [
        {
          "path": "material.torrent",
          "kind": "file",
          "age_seconds": 259200,
          "mtime": 1793188800.0
        }
      ],
      "return": null,
      "exception": null,
      "trace": [
        {
          "operation": "exists",
          "path": ".",
          "result": true
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "material.torrent"
          ]
        },
        {
          "operation": "is_file",
          "path": "material.torrent",
          "result": true
        },
        {
          "operation": "datetime.now",
          "path": ".",
          "result": "2026-10-31T12:00:00"
        },
        {
          "operation": "stat",
          "path": "material.torrent",
          "st_mtime": 1793188800.0
        },
        {
          "operation": "datetime.fromtimestamp",
          "path": ".",
          "timestamp": 1793188800.0,
          "result": "2026-10-28T12:00:00"
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "material.torrent"
          ]
        },
        {
          "operation": "is_dir",
          "path": "material.torrent",
          "result": false
        }
      ],
      "after": [
        {
          "path": "material.torrent",
          "kind": "file",
          "age_seconds": 259200,
          "mtime": 1793188800.0
        }
      ],
      "root_exists_after": true
    },
    {
      "id": "three_days_twenty_three_hours",
      "input": {
        "id": "three_days_twenty_three_hours",
        "now_local": "2026-10-31T12:00:00",
        "days": 3,
        "root_exists": true,
        "nodes": [
          {
            "path": "material.torrent",
            "kind": "file",
            "age_seconds": 342000
          }
        ]
      },
      "before": [
        {
          "path": "material.torrent",
          "kind": "file",
          "age_seconds": 342000,
          "mtime": 1793106000.0
        }
      ],
      "return": null,
      "exception": null,
      "trace": [
        {
          "operation": "exists",
          "path": ".",
          "result": true
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "material.torrent"
          ]
        },
        {
          "operation": "is_file",
          "path": "material.torrent",
          "result": true
        },
        {
          "operation": "datetime.now",
          "path": ".",
          "result": "2026-10-31T12:00:00"
        },
        {
          "operation": "stat",
          "path": "material.torrent",
          "st_mtime": 1793106000.0
        },
        {
          "operation": "datetime.fromtimestamp",
          "path": ".",
          "timestamp": 1793106000.0,
          "result": "2026-10-27T13:00:00"
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "material.torrent"
          ]
        },
        {
          "operation": "is_dir",
          "path": "material.torrent",
          "result": false
        }
      ],
      "after": [
        {
          "path": "material.torrent",
          "kind": "file",
          "age_seconds": 342000,
          "mtime": 1793106000.0
        }
      ],
      "root_exists_after": true
    },
    {
      "id": "exactly_four_days",
      "input": {
        "id": "exactly_four_days",
        "now_local": "2026-10-31T12:00:00",
        "days": 3,
        "root_exists": true,
        "nodes": [
          {
            "path": "material.torrent",
            "kind": "file",
            "age_seconds": 345600
          }
        ]
      },
      "before": [
        {
          "path": "material.torrent",
          "kind": "file",
          "age_seconds": 345600,
          "mtime": 1793102400.0
        }
      ],
      "return": null,
      "exception": null,
      "trace": [
        {
          "operation": "exists",
          "path": ".",
          "result": true
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "material.torrent"
          ]
        },
        {
          "operation": "is_file",
          "path": "material.torrent",
          "result": true
        },
        {
          "operation": "datetime.now",
          "path": ".",
          "result": "2026-10-31T12:00:00"
        },
        {
          "operation": "stat",
          "path": "material.torrent",
          "st_mtime": 1793102400.0
        },
        {
          "operation": "datetime.fromtimestamp",
          "path": ".",
          "timestamp": 1793102400.0,
          "result": "2026-10-27T12:00:00"
        },
        {
          "operation": "unlink",
          "path": "material.torrent",
          "result": "memory_only"
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": []
        }
      ],
      "after": [],
      "root_exists_after": true
    },
    {
      "id": "nested_old_new_and_empty_directories",
      "input": {
        "id": "nested_old_new_and_empty_directories",
        "now_local": "2026-10-31T12:00:00",
        "days": 3,
        "root_exists": true,
        "nodes": [
          {
            "path": "old",
            "kind": "dir"
          },
          {
            "path": "old/inner",
            "kind": "dir"
          },
          {
            "path": "old/inner/material.torrent",
            "kind": "file",
            "age_seconds": 345600
          },
          {
            "path": "new",
            "kind": "dir"
          },
          {
            "path": "new/material.torrent",
            "kind": "file",
            "age_seconds": 3600
          },
          {
            "path": "empty",
            "kind": "dir"
          },
          {
            "path": "empty/child",
            "kind": "dir"
          }
        ]
      },
      "before": [
        {
          "path": "old",
          "kind": "dir"
        },
        {
          "path": "old/inner",
          "kind": "dir"
        },
        {
          "path": "old/inner/material.torrent",
          "kind": "file",
          "age_seconds": 345600,
          "mtime": 1793102400.0
        },
        {
          "path": "new",
          "kind": "dir"
        },
        {
          "path": "new/material.torrent",
          "kind": "file",
          "age_seconds": 3600,
          "mtime": 1793444400.0
        },
        {
          "path": "empty",
          "kind": "dir"
        },
        {
          "path": "empty/child",
          "kind": "dir"
        }
      ],
      "return": null,
      "exception": null,
      "trace": [
        {
          "operation": "exists",
          "path": ".",
          "result": true
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "old",
            "old/inner",
            "old/inner/material.torrent",
            "new",
            "new/material.torrent",
            "empty",
            "empty/child"
          ]
        },
        {
          "operation": "is_file",
          "path": "old",
          "result": false
        },
        {
          "operation": "is_file",
          "path": "old/inner",
          "result": false
        },
        {
          "operation": "is_file",
          "path": "old/inner/material.torrent",
          "result": true
        },
        {
          "operation": "datetime.now",
          "path": ".",
          "result": "2026-10-31T12:00:00"
        },
        {
          "operation": "stat",
          "path": "old/inner/material.torrent",
          "st_mtime": 1793102400.0
        },
        {
          "operation": "datetime.fromtimestamp",
          "path": ".",
          "timestamp": 1793102400.0,
          "result": "2026-10-27T12:00:00"
        },
        {
          "operation": "unlink",
          "path": "old/inner/material.torrent",
          "result": "memory_only"
        },
        {
          "operation": "is_file",
          "path": "new",
          "result": false
        },
        {
          "operation": "is_file",
          "path": "new/material.torrent",
          "result": true
        },
        {
          "operation": "datetime.now",
          "path": ".",
          "result": "2026-10-31T12:00:00"
        },
        {
          "operation": "stat",
          "path": "new/material.torrent",
          "st_mtime": 1793444400.0
        },
        {
          "operation": "datetime.fromtimestamp",
          "path": ".",
          "timestamp": 1793444400.0,
          "result": "2026-10-31T11:00:00"
        },
        {
          "operation": "is_file",
          "path": "empty",
          "result": false
        },
        {
          "operation": "is_file",
          "path": "empty/child",
          "result": false
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "old",
            "old/inner",
            "new",
            "new/material.torrent",
            "empty",
            "empty/child"
          ]
        },
        {
          "operation": "is_dir",
          "path": "old/inner",
          "result": true
        },
        {
          "operation": "iterdir",
          "path": "old/inner",
          "result": []
        },
        {
          "operation": "rmdir",
          "path": "old/inner",
          "result": "memory_only"
        },
        {
          "operation": "is_dir",
          "path": "old",
          "result": true
        },
        {
          "operation": "iterdir",
          "path": "old",
          "result": []
        },
        {
          "operation": "rmdir",
          "path": "old",
          "result": "memory_only"
        },
        {
          "operation": "is_dir",
          "path": "new/material.torrent",
          "result": false
        },
        {
          "operation": "is_dir",
          "path": "new",
          "result": true
        },
        {
          "operation": "iterdir",
          "path": "new",
          "result": [
            "new/material.torrent"
          ]
        },
        {
          "operation": "is_dir",
          "path": "empty/child",
          "result": true
        },
        {
          "operation": "iterdir",
          "path": "empty/child",
          "result": []
        },
        {
          "operation": "rmdir",
          "path": "empty/child",
          "result": "memory_only"
        },
        {
          "operation": "is_dir",
          "path": "empty",
          "result": true
        },
        {
          "operation": "iterdir",
          "path": "empty",
          "result": []
        },
        {
          "operation": "rmdir",
          "path": "empty",
          "result": "memory_only"
        }
      ],
      "after": [
        {
          "path": "new",
          "kind": "dir"
        },
        {
          "path": "new/material.torrent",
          "kind": "file",
          "age_seconds": 3600,
          "mtime": 1793444400.0
        }
      ],
      "root_exists_after": true
    },
    {
      "id": "unlink_exception_propagates",
      "input": {
        "id": "unlink_exception_propagates",
        "now_local": "2026-10-31T12:00:00",
        "days": 3,
        "root_exists": true,
        "nodes": [
          {
            "path": "first.torrent",
            "kind": "file",
            "age_seconds": 345600,
            "unlink_error": "owned recording unlink failure"
          },
          {
            "path": "later.torrent",
            "kind": "file",
            "age_seconds": 345600
          },
          {
            "path": "empty",
            "kind": "dir"
          }
        ]
      },
      "before": [
        {
          "path": "first.torrent",
          "kind": "file",
          "age_seconds": 345600,
          "unlink_error": "owned recording unlink failure",
          "mtime": 1793102400.0
        },
        {
          "path": "later.torrent",
          "kind": "file",
          "age_seconds": 345600,
          "mtime": 1793102400.0
        },
        {
          "path": "empty",
          "kind": "dir"
        }
      ],
      "return": null,
      "exception": {
        "class": "PermissionError",
        "message": "owned recording unlink failure"
      },
      "trace": [
        {
          "operation": "exists",
          "path": ".",
          "result": true
        },
        {
          "operation": "rglob",
          "path": ".",
          "pattern": "*",
          "result": [
            "first.torrent",
            "later.torrent",
            "empty"
          ]
        },
        {
          "operation": "is_file",
          "path": "first.torrent",
          "result": true
        },
        {
          "operation": "datetime.now",
          "path": ".",
          "result": "2026-10-31T12:00:00"
        },
        {
          "operation": "stat",
          "path": "first.torrent",
          "st_mtime": 1793102400.0
        },
        {
          "operation": "datetime.fromtimestamp",
          "path": ".",
          "timestamp": 1793102400.0,
          "result": "2026-10-27T12:00:00"
        },
        {
          "operation": "unlink",
          "path": "first.torrent",
          "result": "injected_error"
        }
      ],
      "after": [
        {
          "path": "first.torrent",
          "kind": "file",
          "age_seconds": 345600,
          "unlink_error": "owned recording unlink failure",
          "mtime": 1793102400.0
        },
        {
          "path": "later.torrent",
          "kind": "file",
          "age_seconds": 345600,
          "mtime": 1793102400.0
        },
        {
          "path": "empty",
          "kind": "dir"
        }
      ],
      "root_exists_after": true
    }
  ]
}
`
