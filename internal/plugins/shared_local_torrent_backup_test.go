package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ownedLocalBackupRequest() LocalTorrentBackupRequest {
	return LocalTorrentBackupRequest{DownloaderID: " owned-qB ", Directory: " /owned/备份/ ", Hash: "AbCdEf_001.hash"}
}

func TestLocalTorrentBackupWireAndBoundary(t *testing.T) {
	calls := 0
	client := LocalTorrentBackup{Host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if ctx.Err() != nil || method != LocalTorrentBackupCapability {
			t.Fatal("wrong current host capability", method)
		}
		var request struct {
			Op    string                    `json:"op"`
			Input LocalTorrentBackupRequest `json:"input"`
		}
		if json.Unmarshal(raw, &request) != nil || request.Op != "prepare" || request.Input != ownedLocalBackupRequest() {
			t.Fatalf("selectors/case were normalized: %s", raw)
		}
		return json.RawMessage(`{"skipped":true,"temporary":false}`), nil
	}}
	result, err := client.Prepare(context.Background(), ownedLocalBackupRequest())
	if err != nil || !result.Skipped || result.Temporary || result.Content != nil || calls != 1 {
		t.Fatal("source skip result", result, err, calls)
	}
	calls = 0
	for _, change := range []func(*LocalTorrentBackupRequest){
		func(in *LocalTorrentBackupRequest) { in.Hash = "" },
		func(in *LocalTorrentBackupRequest) { in.Hash = "." },
		func(in *LocalTorrentBackupRequest) { in.Hash = ".." },
		func(in *LocalTorrentBackupRequest) { in.Hash = "../owned" },
		func(in *LocalTorrentBackupRequest) { in.Hash = `owned\other` },
		func(in *LocalTorrentBackupRequest) { in.Hash = "owned\x00hash" },
		func(in *LocalTorrentBackupRequest) { in.Hash = string([]byte{0xff}) },
		func(in *LocalTorrentBackupRequest) { in.DownloaderID = string([]byte{0xff}) },
		func(in *LocalTorrentBackupRequest) { in.Directory = string([]byte{0xff}) },
		func(in *LocalTorrentBackupRequest) { in.Directory = strings.Repeat("x", 128<<10) },
	} {
		input := ownedLocalBackupRequest()
		change(&input)
		if _, err := client.Prepare(context.Background(), input); err == nil {
			t.Fatal("invalid adapter input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Prepare(ctx, ownedLocalBackupRequest()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := client.Prepare(nil, ownedLocalBackupRequest()); err == nil {
		t.Fatal("nil context")
	}
	if _, err := (LocalTorrentBackup{}).Prepare(context.Background(), ownedLocalBackupRequest()); err == nil {
		t.Fatal("nil host")
	}
	if calls != 0 {
		t.Fatal("invalid input reached host", calls)
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"skipped":true}`, `{"skipped":null,"temporary":false}`,
		`{"skipped":true,"temporary":null}`, `{"skipped":true,"temporary":true}`,
		`{"skipped":true,"temporary":false,"handle":null}`, `{"skipped":true,"temporary":false,"kind":"none"}`,
		`{"skipped":false,"temporary":false}`, `{"skipped":true,"temporary":false} {}`,
	} {
		client.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		if result, err := client.Prepare(context.Background(), ownedLocalBackupRequest()); err == nil || !reflect.DeepEqual(result, LocalTorrentBackupResult{}) {
			t.Fatal("malformed result returned data", raw, result, err)
		}
	}
	sentinel := errors.New("owned host boundary")
	client.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel }
	if _, err := client.Prepare(context.Background(), ownedLocalBackupRequest()); !errors.Is(err, sentinel) {
		t.Fatal("host failure swallowed", err)
	}
}

type ownedLocalBackupHost struct {
	data                     []byte
	temporary                bool
	prepare, reads, releases int
	mode                     string
	cancel                   context.CancelFunc
}

func (h *ownedLocalBackupHost) call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method != LocalTorrentBackupCapability {
		return nil, errors.New("wrong backup capability")
	}
	var request struct {
		Op     string                    `json:"op"`
		Input  LocalTorrentBackupRequest `json:"input"`
		Handle string                    `json:"handle"`
		Offset int                       `json:"offset"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	const handle = "owned_local_backup_0001"
	switch request.Op {
	case "prepare":
		h.prepare++
		if request.Input != ownedLocalBackupRequest() {
			return nil, errors.New("backup selection changed")
		}
		digest := sha256.Sum256(h.data)
		response := map[string]any{"skipped": false, "temporary": h.temporary, "handle": handle, "total": len(h.data), "sha256": hex.EncodeToString(digest[:])}
		switch h.mode {
		case "oversize":
			response["total"] = TorrentMaterialMaxBytes + 1
		case "digest":
			response["sha256"] = strings.Repeat("0", 64)
		case "missing-temporary":
			delete(response, "temporary")
		case "mixed-skip":
			response["skipped"] = true
		case "null-total":
			response["total"] = nil
		}
		if h.mode == "cancel-prepare" && h.cancel != nil {
			h.cancel()
			h.cancel = nil
		}
		return json.Marshal(response)
	case "read":
		h.reads++
		if request.Handle != handle || request.Offset < 0 || request.Offset >= len(h.data) {
			return nil, errors.New("foreign backup transfer")
		}
		if h.mode == "host-error" {
			return nil, errors.New("owned read failure")
		}
		data := h.data[request.Offset:min(request.Offset+TorrentMaterialChunkBytes, len(h.data))]
		if h.mode == "short" {
			data = data[:len(data)-1]
		}
		offset, token := request.Offset, handle
		if h.mode == "offset" {
			offset++
		}
		if h.mode == "handle" {
			token = "owned_other_backup_01"
		}
		if h.cancel != nil {
			h.cancel()
			h.cancel = nil
		}
		return json.Marshal(map[string]any{"handle": token, "offset": offset, "data": data})
	case "release":
		h.releases++
		if request.Handle != handle || ctx.Err() != nil {
			return nil, errors.New("invalid current backup release")
		}
		switch h.mode {
		case "release-error":
			return nil, errors.New("owned release failure")
		case "release-false":
			return json.RawMessage(`{"released":false}`), nil
		}
		return json.RawMessage(`{"released":true}`), nil
	default:
		return nil, errors.New("unexpected backup operation")
	}
}

func TestLocalTorrentBackupPagedBytesAndCleanup(t *testing.T) {
	for _, size := range []int{0, TorrentMaterialChunkBytes + 3, TorrentMaterialMaxBytes} {
		for _, temporary := range []bool{false, true} {
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i*17 + 3)
			}
			h := &ownedLocalBackupHost{data: data, temporary: temporary}
			result, err := (LocalTorrentBackup{Host: h.call}).Prepare(context.Background(), ownedLocalBackupRequest())
			if err != nil || result.Skipped || result.Temporary != temporary || result.Content == nil || !bytes.Equal(result.Content, data) {
				t.Fatal("full byte/temporary result differs", size, temporary, err)
			}
			if h.prepare != 1 || h.reads != (size+TorrentMaterialChunkBytes-1)/TorrentMaterialChunkBytes || h.releases != 1 {
				t.Fatal("private page sequence differs", h)
			}
			if len(data) > 0 {
				data[0] ^= 0xff
				if bytes.Equal(result.Content, data) {
					t.Fatal("content aliases host")
				}
			}
		}
	}
	for _, mode := range []string{"oversize", "digest", "missing-temporary", "mixed-skip", "null-total", "short", "offset", "handle", "host-error", "release-error", "release-false"} {
		h := &ownedLocalBackupHost{data: []byte("owned backup bytes"), mode: mode}
		result, err := (LocalTorrentBackup{Host: h.call}).Prepare(context.Background(), ownedLocalBackupRequest())
		if err == nil || !reflect.DeepEqual(result, LocalTorrentBackupResult{}) || h.prepare != 1 || h.releases != 1 {
			t.Fatal("partial result or allocated handle leaked", mode, result, err, h)
		}
		if mode == "oversize" && h.reads != 0 {
			t.Fatal("oversize transfer read")
		}
	}
	for _, mode := range []string{"cancel-prepare", "cancel-read"} {
		ctx, cancel := context.WithCancel(context.Background())
		h := &ownedLocalBackupHost{data: make([]byte, TorrentMaterialChunkBytes+1), mode: mode, cancel: cancel}
		result, err := (LocalTorrentBackup{Host: h.call}).Prepare(ctx, ownedLocalBackupRequest())
		wantReads := 1
		if mode == "cancel-prepare" {
			wantReads = 0
		}
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, LocalTorrentBackupResult{}) || h.reads != wantReads || h.releases != 1 {
			t.Fatal("cancel returned partial or skipped same-scope release", mode, result, err, h)
		}
	}
}
