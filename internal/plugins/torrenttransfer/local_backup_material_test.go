package torrenttransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"testing"
)

// Reuse the intact historical recording, not a freshly generated Go expectation.
// This file has an additional wheel-metadata field compared with testdata's copy.
//
//go:embed reference/source-oracle.json
var localBackupSourceOracle []byte

type localBackupMemory struct {
	input                       LocalBackupMaterialInput
	torrent, resume, temporary  []byte
	torrentExists, resumeExists bool
	calls                       []map[string]any
	logs                        []Log
	failAt                      string
	failure                     error
	reads                       int
	secondRead                  []byte
	secondReadSet               bool
}

func (p *localBackupMemory) record(op string, fields map[string]any) {
	fields["op"] = op
	p.calls = append(p.calls, fields)
}
func (p *localBackupMemory) fail(at string) error {
	if p.failAt == at {
		return p.failure
	}
	return nil
}
func (p *localBackupMemory) TorrentExists(context.Context) (bool, error) {
	p.record("exists", map[string]any{"path": p.input.TorrentLabel})
	return p.torrentExists, p.fail("torrent-exists")
}
func (p *localBackupMemory) ReadTorrent(context.Context) ([]byte, error) {
	p.reads++
	p.record("read", map[string]any{"path": p.input.TorrentLabel})
	if err := p.fail(fmt.Sprintf("torrent-read-%d", p.reads)); err != nil {
		return nil, err
	}
	if p.reads == 2 && p.secondReadSet {
		return append([]byte{}, p.secondRead...), nil
	}
	return append([]byte{}, p.torrent...), nil
}
func (p *localBackupMemory) ResumeExists(context.Context) (bool, error) {
	p.record("exists", map[string]any{"path": p.input.ResumeLabel})
	return p.resumeExists, p.fail("resume-exists")
}
func (p *localBackupMemory) ReadResume(context.Context) ([]byte, error) {
	p.record("read", map[string]any{"path": p.input.ResumeLabel})
	return append([]byte{}, p.resume...), p.fail("resume-read")
}
func (p *localBackupMemory) WriteTemporary(_ context.Context, raw []byte) error {
	p.record("temp", map[string]any{"name": p.input.Hash + ".torrent", "content": base64.StdEncoding.EncodeToString(raw)})
	if err := p.fail("temporary-write"); err != nil {
		return err
	}
	p.temporary = append([]byte{}, raw...)
	return nil
}
func (p *localBackupMemory) ReadTemporary(context.Context) ([]byte, error) {
	p.record("read", map[string]any{"path": "/temp/" + p.input.Hash + ".torrent"})
	return append([]byte{}, p.temporary...), p.fail("temporary-read")
}
func (p *localBackupMemory) run(ctx context.Context) (LocalBackupMaterial, error) {
	return PrepareLocalBackupMaterial(ctx, p.input, p, func(l Log) { p.logs = append(p.logs, l) })
}
func localBackupMemoryInput(qb bool) LocalBackupMaterialInput {
	return LocalBackupMaterialInput{QBittorrent: qb, Hash: sourceHash,
		TorrentLabel: "/backup/" + sourceHash + ".torrent", ResumeLabel: "/backup/" + sourceHash + ".fastresume"}
}
func localBackupMemoryFor(t *testing.T, main, resume map[string]any) *localBackupMemory {
	t.Helper()
	raw, err := bencode(main)
	if err != nil {
		t.Fatal(err)
	}
	p := &localBackupMemory{input: localBackupMemoryInput(true), torrent: raw,
		torrentExists: true, calls: []map[string]any{}, logs: []Log{}}
	if resume != nil {
		p.resume, err = bencode(resume)
		if err != nil {
			t.Fatal(err)
		}
		p.resumeExists = true
	}
	return p
}

func TestLocalBackupMaterialSelectedSourceOracle(t *testing.T) {
	sum := sha256.Sum256(localBackupSourceOracle)
	if hex.EncodeToString(sum[:]) != "b77b1927fc5050a7de0fc7122c72534c70e190e1f947a1a7ede58455d16df2e0" {
		t.Fatal("intact historical source oracle changed")
	}
	var doc struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Name             string
			Config, Scenario map[string]any
			Expected         struct {
				Calls []map[string]any
				Logs  []Log
			}
		}
	}
	if err := json.Unmarshal(localBackupSourceOracle, &doc); err != nil || doc.Source != "e5f513a57cf42ee8778744ba30cf669f93a5a3e6c7589ca0b2e3b9c483651bd5" {
		t.Fatal("fixed installed consumer provenance", err)
	}
	selected := map[string]bool{
		"qbittorrent-qbittorrent-autoFalse-skipFalse":  true,
		"transmission-qbittorrent-autoFalse-skipFalse": true,
		"missing-torrent-file":                         true, "empty-torrent": true, "invalid-torrent": true,
		"missing-announce-no-resume": true, "restore-trackers": true,
		"restore-empty-trackers-keeps-original": true, "invalid-resume": true,
	}
	seen := 0
	for _, sample := range doc.Cases {
		if !selected[sample.Name] {
			continue
		}
		seen++
		t.Run(sample.Name, func(t *testing.T) {
			// Reuse only the inherited historical fixture's input-byte constructor.
			// Never run Core/Host services/add/recheck/delete/history from this root.
			h := newOracleHost(sample.Config, sample.Scenario)
			p := &localBackupMemory{input: localBackupMemoryInput(h.fromType == "qbittorrent"), calls: []map[string]any{}, logs: []Log{}}
			p.torrent, p.torrentExists = h.files[p.input.TorrentLabel]
			p.resume, p.resumeExists = h.files[p.input.ResumeLabel]
			beforeTorrent, beforeResume := append([]byte{}, p.torrent...), append([]byte{}, p.resume...)
			result, err := p.run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := []map[string]any{}
			wantLogs := []Log{}
			var wantContent []byte
			wantReady, wantTemporary := false, false
			for _, call := range sample.Expected.Calls {
				switch call["op"] {
				case "exists":
					if call["path"] == p.input.TorrentLabel || call["path"] == p.input.ResumeLabel {
						wantCalls = append(wantCalls, call)
					}
				case "read":
					wantCalls = append(wantCalls, call)
				case "temp":
					wantTemporary = true
					wantCalls = append(wantCalls, call)
				case "add":
					wantReady = true
					wantContent, err = base64.StdEncoding.DecodeString(call["spec"].(map[string]any)["content"].(string))
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, entry := range sample.Expected.Logs {
				if entry.Line == 787 || entry.Line >= 815 && entry.Line <= 864 {
					wantLogs = append(wantLogs, entry)
				}
			}
			if !reflect.DeepEqual(p.calls, wantCalls) || !reflect.DeepEqual(p.logs, wantLogs) {
				t.Fatalf("material-only source projection differs\nactual calls %#v\nsource calls %#v\nactual logs %#v\nsource logs %#v", p.calls, wantCalls, p.logs, wantLogs)
			}
			if result.Skipped == wantReady || result.Temporary != wantTemporary || !bytes.Equal(result.Content, wantContent) {
				t.Fatalf("source byte/skip/rebuilt result differs: %+v", result)
			}
			if !bytes.Equal(beforeTorrent, p.torrent) || !bytes.Equal(beforeResume, p.resume) {
				t.Fatal("backup input bytes mutated")
			}
		})
	}
	if seen != len(selected) {
		t.Fatalf("selected original cases missing: %d", seen)
	}
}

func TestLocalBackupMaterialReadAndRebuildBoundaries(t *testing.T) {
	ctx := context.Background()
	t.Run("single-tracker-keeps-old-announce-list", func(t *testing.T) {
		old := []any{[]any{"https://old.invalid/a"}}
		p := localBackupMemoryFor(t, map[string]any{"announce": "", "announce-list": old}, map[string]any{"trackers": []any{[]any{"https://new.invalid/a"}}})
		result, err := p.run(ctx)
		want, _ := bencode(map[string]any{"announce": "https://new.invalid/a", "announce-list": old})
		if err != nil || result.Skipped || !result.Temporary || !bytes.Equal(result.Content, want) {
			t.Fatal("single tracker must preserve old list", result, err)
		}
	})
	t.Run("integer-zero-announce-and-tracker-use-source-truth", func(t *testing.T) {
		p := localBackupMemoryFor(t, map[string]any{"announce": big.NewInt(0)}, map[string]any{"trackers": []any{big.NewInt(0)}})
		result, err := p.run(ctx)
		if err != nil || result.Skipped || result.Temporary || !bytes.Equal(result.Content, p.torrent) || len(p.logs) != 1 || p.logs[0].Line != 834 || p.reads != 2 {
			t.Fatal("codec integer zero truth differs", result, err, p.logs)
		}
	})
	t.Run("original-byte-final-reread-not-reencoding", func(t *testing.T) {
		p := localBackupMemoryFor(t, map[string]any{"announce": "https://owned.invalid"}, nil)
		p.torrent = []byte("d4:info1:x8:announce21:https://owned.invalide") // valid unsorted dictionary
		p.secondRead, p.secondReadSet = []byte("owned changed final read"), true
		result, err := p.run(ctx)
		if err != nil || result.Skipped || result.Temporary || !bytes.Equal(result.Content, p.secondRead) || p.reads != 2 || len(p.logs) != 0 {
			t.Fatal("outside-try final read not preserved", result, err, p.logs)
		}
		result.Content[0] = '!'
		if p.secondRead[0] != 'o' {
			t.Fatal("returned bytes alias IO buffer")
		}
	})
	t.Run("non-qb-empty-raw-is-not-qb-skip", func(t *testing.T) {
		p := localBackupMemoryFor(t, map[string]any{}, nil)
		p.input.QBittorrent, p.torrent = false, []byte{}
		result, err := p.run(ctx)
		if err != nil || result.Skipped || result.Temporary || len(result.Content) != 0 || p.reads != 1 || len(p.logs) != 0 {
			t.Fatal("non-qb must not decode or require resume", result, err)
		}
	})
	for _, failure := range []struct {
		at     string
		caught bool
	}{
		{"torrent-exists", false}, {"torrent-read-1", false}, {"resume-exists", false},
		{"resume-read", true}, {"temporary-write", true}, {"temporary-read", false},
	} {
		t.Run(failure.at, func(t *testing.T) {
			p := localBackupMemoryFor(t, map[string]any{}, map[string]any{"trackers": []any{[]any{"https://owned.invalid/a"}}})
			p.failAt, p.failure = failure.at, errors.New("owned IO failure")
			result, err := p.run(ctx)
			if failure.caught {
				if err != nil || !result.Skipped || result.Temporary || len(result.Content) != 0 || len(p.logs) != 2 || p.logs[1].Line != 861 || p.logs[1].Level != "ERROR" {
					t.Fatal("resume try catch/skip differs", result, err, p.logs)
				}
			} else if !errors.Is(err, p.failure) || result.Skipped {
				t.Fatal("outside-try IO failure was swallowed", result, err)
			}
		})
	}
	t.Run("final-original-read-error-propagates", func(t *testing.T) {
		p := localBackupMemoryFor(t, map[string]any{"announce": "owned"}, nil)
		p.failAt, p.failure = "torrent-read-2", errors.New("owned final read failure")
		result, err := p.run(ctx)
		if !errors.Is(err, p.failure) || result.Skipped || len(p.logs) != 0 {
			t.Fatal(result, err, p.logs)
		}
	})
	t.Run("pre-cancel-no-IO", func(t *testing.T) {
		p := localBackupMemoryFor(t, map[string]any{}, nil)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := p.run(cancelled)
		if !errors.Is(err, context.Canceled) || len(p.calls) != 0 {
			t.Fatal("pre-cancel performed IO", err, p.calls)
		}
	})
}
