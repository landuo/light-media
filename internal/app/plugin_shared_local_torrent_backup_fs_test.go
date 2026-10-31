package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/torrenttransfer"
)

const ownedLocalBackupHash = "1111111111111111111111111111111111111111"

type ownedLocalBackupSample struct {
	torrent, resume, want []byte
	temporary             bool
}

func ownedLocalBackupSource(t *testing.T, name string) ownedLocalBackupSample {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/plugins/torrent-transfer-1.10.3/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "536e6236db1af581258943c14258d59c253ae5ec99e9f663904e79f89593199b" {
		t.Fatal("fixed historical source observation changed")
	}
	var source struct {
		Cases []struct {
			Name     string `json:"name"`
			Scenario struct {
				Torrent *string `json:"torrent_b64"`
				Resume  *string `json:"resume_b64"`
			} `json:"scenario"`
			Expected struct {
				Calls []struct {
					Op      string `json:"op"`
					Content string `json:"content"`
					Spec    struct {
						Content string `json:"content"`
					} `json:"spec"`
				} `json:"calls"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	decode := func(value string) []byte {
		out, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, sample := range source.Cases {
		if sample.Name != name {
			continue
		}
		// Exact default input from generate_source_oracle.py:27. Do not construct
		// input from the source output or from a call to the target codec.
		result := ownedLocalBackupSample{torrent: []byte("d8:announce32:https://tracker.invalid/announce4:infod6:lengthi1e4:name15:owned syntheticee")}
		if sample.Scenario.Torrent != nil {
			result.torrent = decode(*sample.Scenario.Torrent)
		}
		if sample.Scenario.Resume != nil {
			result.resume = decode(*sample.Scenario.Resume)
		}
		found := false
		for _, call := range sample.Expected.Calls {
			if call.Op == "temp" {
				result.temporary = true
			}
			if call.Op == "add" {
				result.want = decode(call.Spec.Content)
				found = true
			}
		}
		if !found {
			t.Fatal("selected source observation has no resulting material", name)
		}
		return result
	}
	t.Fatal("selected source observation missing", name)
	return ownedLocalBackupSample{}
}

func ownedLocalBackupApp(t *testing.T) (*App, string, string, originalBaseCallScope) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	allowed, library := t.TempDir(), t.TempDir()
	// This is a real App/SQLite/filesystem component fixture, not an installation
	// or settings-HTTP gate. No downloader client is constructed or contacted.
	a.cfgMu.Lock()
	a.config.AllowedRoots = []string{allowed}
	a.config.Roots = []Root{{ID: "owned-backup-library", Name: "Owned", Path: library}}
	disabled := false
	a.config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "owned-qb", Kind: "qb"}, Enabled: &disabled},
		{Config: download.Config{ID: "owned-tr", Kind: "transmission"}, Enabled: &disabled},
	}
	a.cfgMu.Unlock()
	return a, allowed, library, originalBaseCallScope{validate: func(ctx context.Context) error { return ctx.Err() }}
}

func ownedLocalBackupWrite(t *testing.T, directory string, sample ownedLocalBackupSample) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, ownedLocalBackupHash+".torrent"), sample.torrent, 0600); err != nil {
		t.Fatal(err)
	}
	if sample.resume != nil {
		if err := os.WriteFile(filepath.Join(directory, ownedLocalBackupHash+".fastresume"), sample.resume, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func ownedLocalBackupAssertBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("owned material bytes differ at %s: %v", filepath.Base(path), err)
	}
}

func TestOriginalLocalTorrentBackupOwnedFilesystem(t *testing.T) {
	a, allowed, library, scope := ownedLocalBackupApp(t)
	ctx := context.Background()
	tempRoot := filepath.Join(a.Dir, "temp", "plugin-torrent-backup")
	var checks atomic.Int32
	scope.validate = func(ctx context.Context) error { checks.Add(1); return ctx.Err() }
	for _, name := range []string{"qbittorrent-qbittorrent-autoFalse-skipFalse", "restore-empty-trackers-keeps-original", "restore-trackers"} {
		t.Run(name, func(t *testing.T) {
			sample := ownedLocalBackupSource(t, name)
			ownedLocalBackupWrite(t, allowed, sample)
			path := filepath.Join(allowed, ownedLocalBackupHash+".torrent")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.prepareOriginalLocalTorrentBackup(ctx, scope, plugins.LocalTorrentBackupRequest{DownloaderID: "owned-qb", Directory: allowed, Hash: ownedLocalBackupHash})
			if err != nil || result.Skipped || result.Temporary != sample.temporary || !bytes.Equal(result.Content, sample.want) {
				t.Fatalf("real filesystem/source material mismatch: %+v %v", result, err)
			}
			ownedLocalBackupAssertBytes(t, path, sample.torrent)
			after, err := os.Stat(path)
			if err != nil || !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("readonly backup mtime changed", err)
			}
			if sample.resume != nil {
				ownedLocalBackupAssertBytes(t, filepath.Join(allowed, ownedLocalBackupHash+".fastresume"), sample.resume)
			}
			if sample.temporary {
				ownedLocalBackupAssertBytes(t, filepath.Join(tempRoot, ownedLocalBackupHash+".torrent"), sample.want)
			} else if _, err := os.Stat(tempRoot); !os.IsNotExist(err) {
				t.Fatal("ordinary material created temporary storage", err)
			}
		})
	}
	// The existing library-root half of browserRoots is equally usable. Type is
	// selected from host config: TR reads these literal bytes without qB decoding.
	plain := []byte("owned transmission material, not bencode")
	if err := os.WriteFile(filepath.Join(library, "MixedCase.torrent"), plain, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.prepareOriginalLocalTorrentBackup(ctx, scope, plugins.LocalTorrentBackupRequest{DownloaderID: "owned-tr", Directory: library, Hash: "MixedCase"})
	if err != nil || result.Skipped || result.Temporary || !bytes.Equal(result.Content, plain) {
		t.Fatal("library root or configured TR direct-read branch changed", result, err)
	}
	if checks.Load() < 10 {
		t.Fatal("actual per-operation scope checks missing", checks.Load())
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != ownedLocalBackupHash+".torrent" {
		t.Fatal("temporary storage must contain only the rebuilt exact hash", entries, err)
	}
}

func TestOriginalLocalTorrentBackupSameHashReuseAndConcurrent(t *testing.T) {
	a, allowed, library, scope := ownedLocalBackupApp(t)
	ctx := context.Background()
	source := ownedLocalBackupSource(t, "restore-trackers")
	other := source
	// Owned filesystem contention vector: distinct same-length info.name inputs.
	// This extra marker is not claimed to be another historical source execution.
	other.torrent = bytes.ReplaceAll(source.torrent, []byte("5:owned"), []byte("5:other"))
	other.want = bytes.ReplaceAll(source.want, []byte("5:owned"), []byte("5:other"))
	ownedLocalBackupWrite(t, allowed, source)
	ownedLocalBackupWrite(t, library, other)
	tempRoot := filepath.Join(a.Dir, "temp", "plugin-torrent-backup")
	if err := os.MkdirAll(tempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tempRoot, ownedLocalBackupHash+".torrent")
	if err := os.WriteFile(path, append(append([]byte{}, source.want...), []byte("owned obsolete trailing bytes")...), 0600); err != nil {
		t.Fatal(err)
	}
	prepare := func(directory string) (torrenttransfer.LocalBackupMaterial, error) {
		return a.prepareOriginalLocalTorrentBackup(ctx, scope, plugins.LocalTorrentBackupRequest{DownloaderID: "owned-qb", Directory: directory, Hash: ownedLocalBackupHash})
	}
	for n := 0; n < 2; n++ {
		result, err := prepare(allowed)
		if err != nil || !result.Temporary || result.Skipped || !bytes.Equal(result.Content, source.want) {
			t.Fatal("same hash deterministic replacement failed", result, err)
		}
		ownedLocalBackupAssertBytes(t, path, source.want)
	}
	start := make(chan struct{})
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for _, sample := range []struct {
		directory string
		want      []byte
	}{{allowed, source.want}, {library, other.want}} {
		wg.Add(1)
		go func(directory string, want []byte) {
			defer wg.Done()
			<-start
			result, err := prepare(directory)
			if err != nil || !result.Temporary || result.Skipped || !bytes.Equal(result.Content, want) {
				failures <- fmt.Errorf("same hash write/read crossed calls: temporary=%v skipped=%v error=%v", result.Temporary, result.Skipped, err)
			}
		}(sample.directory, sample.want)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != ownedLocalBackupHash+".torrent" {
		t.Fatal("same hash calls accumulated material files", entries, err)
	}
	ownedLocalBackupAssertBytes(t, filepath.Join(allowed, ownedLocalBackupHash+".torrent"), source.torrent)
	ownedLocalBackupAssertBytes(t, filepath.Join(library, ownedLocalBackupHash+".torrent"), other.torrent)
	originalLocalBackupPathLocks.Lock()
	retained := originalLocalBackupPathLocks.paths[path] != nil
	originalLocalBackupPathLocks.Unlock()
	if retained {
		t.Fatal("completed calls leaked path-lock registry entries", retained)
	}
	// Returning/releasing material must not unlink its source-style retained file.
	if _, err := os.Stat(path); err != nil {
		t.Fatal("retained rebuilt material disappeared after call completion", err)
	}
}
