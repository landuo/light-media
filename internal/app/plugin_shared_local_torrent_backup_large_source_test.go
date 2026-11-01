package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

// The unmodified actual source stdout is stored once. Inputs are reconstructed
// from its exact predeclared recipe; expected bytes come only from the original
// native fast-bencode run's gzip recovery payload, never from the Go codec.
//
//go:embed testdata/source-material-large-s02.json
var localBackupLargeSourceRaw []byte

type localBackupLargeRecipe struct {
	Prefix string `json:"prefix_base64"`
	Repeat string `json:"repeat_ascii"`
	Count  int    `json:"repeat_count"`
	Suffix string `json:"suffix_base64"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func localBackupLargeSource(t *testing.T) ownedLocalBackupSample {
	t.Helper()
	assertBytes := func(data []byte, size int, expected string) {
		t.Helper()
		sum := sha256.Sum256(data)
		if len(data) != size || hex.EncodeToString(sum[:]) != expected {
			t.Fatal("original large source bytes/hash drift")
		}
	}
	assertBytes(localBackupLargeSourceRaw, 34003, "d65cfcde5d0f71a9c331a46e06d065bbefd5d48696c0e63e7964b77f84487f7e")
	var raw struct {
		RootCalls int `json:"root_calls"`
		Outcome   struct {
			Kind, PythonType string
			Value            json.RawMessage
		} `json:"outcome"`
		Input    struct{ Torrent, Resume localBackupLargeRecipe } `json:"input_recipes"`
		Material struct {
			Length     int    `json:"length"`
			SHA256     string `json:"sha256"`
			Gzip       string `json:"gzip_base64"`
			GzipBytes  int    `json:"gzip_bytes"`
			GzipSHA256 string `json:"gzip_sha256"`
		} `json:"temporary_material_actual"`
		Drift   []json.RawMessage `json:"input_drift"`
		Rejects []json.RawMessage `json:"audit_rejections"`
	}
	if err := json.Unmarshal(localBackupLargeSourceRaw, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.RootCalls != 1 || raw.Outcome.Kind != "return" || !bytes.Equal(raw.Outcome.Value, []byte("null")) || len(raw.Drift) != 0 || len(raw.Rejects) != 0 {
		t.Fatal("source execution outcome differs")
	}
	decode := func(value string) []byte {
		t.Helper()
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	recipe := func(r localBackupLargeRecipe) []byte {
		t.Helper()
		if r.Repeat != "x" || r.Count != 16777217 || r.Bytes <= 16<<20 {
			t.Fatal("fixed source input recipe changed")
		}
		out := append(decode(r.Prefix), bytes.Repeat([]byte(r.Repeat), r.Count)...)
		out = append(out, decode(r.Suffix)...)
		assertBytes(out, r.Bytes, r.SHA256)
		return out
	}
	compressed := decode(raw.Material.Gzip)
	assertBytes(compressed, raw.Material.GzipBytes, raw.Material.GzipSHA256)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	want, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	assertBytes(want, raw.Material.Length, raw.Material.SHA256)
	if len(want) <= 16<<20 {
		t.Fatal("original rebuilt material is not above retired quota")
	}
	return ownedLocalBackupSample{torrent: recipe(raw.Input.Torrent), resume: recipe(raw.Input.Resume), want: want, temporary: true}
}

// This root is real App + owned filesystem + the actual Go codec. It is not an
// installed-worker, grant, daemon or original OS-filesystem proof.
func TestOriginalLocalBackupLargeSourceFilesystem(t *testing.T) {
	a, allowed, _, scope := ownedLocalBackupApp(t)
	sample := localBackupLargeSource(t)
	ownedLocalBackupWrite(t, allowed, sample)
	paths := []string{filepath.Join(allowed, ownedLocalBackupHash+".torrent"), filepath.Join(allowed, ownedLocalBackupHash+".fastresume")}
	before := make([]os.FileInfo, len(paths))
	for i, path := range paths {
		var err error
		before[i], err = os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	var checks int
	scope.validate = func(ctx context.Context) error { checks++; return ctx.Err() }
	got, err := a.prepareOriginalLocalTorrentBackup(context.Background(), scope, plugins.LocalTorrentBackupRequest{DownloaderID: "owned-qb", Directory: allowed, Hash: ownedLocalBackupHash})
	if err != nil || got.Skipped || !got.Temporary || !bytes.Equal(got.Content, sample.want) {
		t.Fatalf("actual source large rebuild mismatch: len=%d skipped=%v temporary=%v err=%v", len(got.Content), got.Skipped, got.Temporary, err)
	}
	if checks < 64 {
		t.Fatal("large material read did not retain bounded per-page current checks", checks)
	}
	for i, path := range paths {
		after, err := os.Stat(path)
		if err != nil || !after.ModTime().Equal(before[i].ModTime()) || after.Size() != before[i].Size() {
			t.Fatal("source backup changed", err)
		}
	}
	ownedLocalBackupAssertBytes(t, paths[0], sample.torrent)
	ownedLocalBackupAssertBytes(t, paths[1], sample.resume)
	rebuilt := filepath.Join(a.Dir, "temp", "plugin-torrent-backup", ownedLocalBackupHash+".torrent")
	ownedLocalBackupAssertBytes(t, rebuilt, sample.want)
	entries, err := os.ReadDir(filepath.Dir(rebuilt))
	if err != nil || len(entries) != 1 || entries[0].Name() != ownedLocalBackupHash+".torrent" {
		t.Fatal("deterministic source-visible material changed", err)
	}
	if _, err := os.Stat(filepath.Join(a.Dir, "temp", "plugin-material-spool")); !os.IsNotExist(err) {
		t.Fatal("source-only IO unexpectedly created a transport spool", err)
	}
}
