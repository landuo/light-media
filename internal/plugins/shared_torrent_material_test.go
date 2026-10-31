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

func ownedTorrentMaterialInput() DownloadTorrentRequest {
	return DownloadTorrentRequest{Torrent: json.RawMessage(`{"site":1,"enclosure":"magnet:owned","extra":{"preserved":null}}`), UserID: json.RawMessage(`9007199254740993`)}
}

func TestSharedDownloadTorrentWireAndBoundary(t *testing.T) {
	for _, variant := range []struct{ raw, kind string }{
		{`{"kind":"none","folder":"","files":[]}`, "none"},
		{`{"kind":"string","text":"magnet:?xt=owned&dn=雪","folder":"","files":[]}`, "string"},
		{`{"kind":"string","text":"","folder":"","files":[]}`, "empty-string"},
	} {
		t.Run(variant.kind, func(t *testing.T) {
			calls := 0
			client := SharedDownloadChain{Host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				if ctx.Err() != nil || method != DownloadTorrentCapability {
					t.Fatal("unexpected host authority", method)
				}
				var request struct {
					Operation string                 `json:"op"`
					Input     DownloadTorrentRequest `json:"input"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				if request.Operation != "prepare" || string(request.Input.UserID) != "9007199254740993" || request.Input.Channel != nil || request.Input.Source != nil || string(request.Input.Torrent) != string(ownedTorrentMaterialInput().Torrent) {
					t.Fatalf("source arguments changed: %s", raw)
				}
				return json.RawMessage(variant.raw), nil
			}}
			result, err := client.DownloadTorrent(context.Background(), ownedTorrentMaterialInput())
			if err != nil || calls != 1 || result.Files == nil || len(result.Files) != 0 || result.Folder != "" {
				t.Fatalf("unexpected tuple %#v %v calls=%d", result, err, calls)
			}
			if variant.kind == "none" {
				if result.Content != nil {
					t.Fatal("None became content")
				}
			} else if result.Content == nil || result.Content.Kind != "string" || result.Content.Bytes != nil {
				t.Fatal("string discriminant lost", result)
			}
			if variant.kind == "empty-string" && result.Content.String != "" {
				t.Fatal("empty string changed")
			}
		})
	}
	calls := 0
	client := SharedDownloadChain{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"kind":"none","folder":"","files":[]}`), nil
	}}
	for _, mutate := range []func(*DownloadTorrentRequest){
		func(in *DownloadTorrentRequest) { in.Torrent = json.RawMessage(`null`) },
		func(in *DownloadTorrentRequest) { in.Torrent = json.RawMessage(`[]`) },
		func(in *DownloadTorrentRequest) { in.Torrent = json.RawMessage(`{"bad":`) },
		func(in *DownloadTorrentRequest) { s := string([]byte{0xff}); in.Source = &s },
		func(in *DownloadTorrentRequest) { in.UserID = json.RawMessage(`1.5`) },
		func(in *DownloadTorrentRequest) { in.UserID = json.RawMessage(`true`) },
		func(in *DownloadTorrentRequest) { in.UserID = json.RawMessage(`{} null`) },
		func(in *DownloadTorrentRequest) {
			in.Torrent = json.RawMessage(`{"enclosure":"` + strings.Repeat("x", 128<<10) + `"}`)
		},
	} {
		in := ownedTorrentMaterialInput()
		mutate(&in)
		if _, err := client.DownloadTorrent(context.Background(), in); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.DownloadTorrent(ctx, ownedTorrentMaterialInput()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := client.DownloadTorrent(nil, ownedTorrentMaterialInput()); err == nil {
		t.Fatal("nil context")
	}
	if _, err := (SharedDownloadChain{}).DownloadTorrent(context.Background(), ownedTorrentMaterialInput()); err == nil {
		t.Fatal("nil host")
	}
	if calls != 0 {
		t.Fatal("invalid input reached host", calls)
	}
	for _, raw := range []string{`null`, `[]`, `{"kind":"none","folder":"","files":null}`, `{"kind":"none","folder":"","files":[null]}`, `{"kind":"none","folder":"","files":[],"text":null}`, `{"kind":"string","folder":"","files":[]}`, `{"kind":"unknown","folder":"","files":[]}`, `{"kind":"none","folder":"","files":[]} {}`} {
		client.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		if _, err := client.DownloadTorrent(context.Background(), ownedTorrentMaterialInput()); err == nil {
			t.Fatal("malformed result accepted", raw)
		}
	}
	client.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"kind":"none","folder":"","files":[""]}`), nil
	}
	if result, err := client.DownloadTorrent(context.Background(), ownedTorrentMaterialInput()); err != nil || !reflect.DeepEqual(result.Files, []string{""}) {
		t.Fatal("legal empty filename was conflated with null", result, err)
	}
}

type ownedTorrentMaterialHost struct {
	data                     []byte
	prepare, reads, releases int
	mode                     string
	cancel                   context.CancelFunc
}

func (h *ownedTorrentMaterialHost) call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method != DownloadTorrentCapability {
		return nil, errors.New("wrong capability")
	}
	var request struct {
		Operation string `json:"op"`
		Handle    string `json:"handle"`
		Offset    int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	const handle = "owned_transfer_00000001"
	switch request.Operation {
	case "prepare":
		h.prepare++
		digest := sha256.Sum256(h.data)
		total := len(h.data)
		if h.mode == "oversize" {
			total = TorrentMaterialMaxBytes + 1
		}
		sha := hex.EncodeToString(digest[:])
		if h.mode == "digest" {
			sha = strings.Repeat("0", 64)
		}
		return json.Marshal(map[string]any{"kind": "bytes", "handle": handle, "total": total, "sha256": sha, "folder": "OwnedFolder", "files": []string{"z.mkv", "a.mkv"}})
	case "read":
		h.reads++
		if request.Handle != handle || request.Offset < 0 || request.Offset >= len(h.data) {
			return nil, errors.New("foreign transfer")
		}
		if h.mode == "host-error" {
			return nil, errors.New("owned host failure")
		}
		data := h.data[request.Offset:min(request.Offset+TorrentMaterialChunkBytes, len(h.data))]
		if h.mode == "short" {
			data = data[:len(data)-1]
		}
		offset := request.Offset
		if h.mode == "offset" {
			offset++
		}
		returnedHandle := handle
		if h.mode == "handle" {
			returnedHandle = "owned_other_transfer1"
		}
		if h.cancel != nil {
			h.cancel()
			h.cancel = nil
		}
		return json.Marshal(map[string]any{"handle": returnedHandle, "offset": offset, "data": data})
	case "release":
		h.releases++
		if request.Handle != handle {
			return nil, errors.New("foreign release")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if h.mode == "release-error" {
			return nil, errors.New("owned release failure")
		}
		return json.RawMessage(`{"released":true}`), nil
	default:
		return nil, errors.New("unexpected operation")
	}
}

func TestSharedDownloadTorrentPagedBytesAndCleanup(t *testing.T) {
	for _, size := range []int{0, 1, TorrentMaterialChunkBytes + 7, TorrentMaterialMaxBytes} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*31 + 7)
		}
		h := &ownedTorrentMaterialHost{data: data}
		result, err := (SharedDownloadChain{Host: h.call}).DownloadTorrent(context.Background(), ownedTorrentMaterialInput())
		if err != nil || result.Content == nil || result.Content.Kind != "bytes" || result.Content.Bytes == nil || !bytes.Equal(result.Content.Bytes, data) {
			t.Fatalf("full material %d lost: %v", size, err)
		}
		if result.Folder != "OwnedFolder" || !reflect.DeepEqual(result.Files, []string{"z.mkv", "a.mkv"}) {
			t.Fatal("folder/order changed", result)
		}
		if h.prepare != 1 || h.reads != (size+TorrentMaterialChunkBytes-1)/TorrentMaterialChunkBytes || h.releases != 1 {
			t.Fatalf("unexpected calls: %#v", h)
		}
		if size > 0 {
			data[0] ^= 0xff
			if bytes.Equal(result.Content.Bytes, data) {
				t.Fatal("returned content aliases host memory")
			}
		}
	}
	for _, mode := range []string{"digest", "short", "offset", "handle", "host-error", "oversize", "release-error"} {
		h := &ownedTorrentMaterialHost{data: []byte("owned bytes"), mode: mode}
		result, err := (SharedDownloadChain{Host: h.call}).DownloadTorrent(context.Background(), ownedTorrentMaterialInput())
		if err == nil || result.Content != nil || h.prepare != 1 || h.releases != 1 {
			t.Fatalf("failure returned partial or leaked transfer: %s %#v %v releases=%d", mode, result, err, h.releases)
		}
		if mode == "oversize" && h.reads != 0 {
			t.Fatal("oversize read attempted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &ownedTorrentMaterialHost{data: make([]byte, TorrentMaterialChunkBytes+1), cancel: cancel}
	result, err := (SharedDownloadChain{Host: h.call}).DownloadTorrent(ctx, ownedTorrentMaterialInput())
	if !errors.Is(err, context.Canceled) || result.Content != nil || h.reads != 1 || h.releases != 1 {
		t.Fatalf("cancel failed: %#v %v reads=%d release=%d", result, err, h.reads, h.releases)
	}
	// Cleanup is a best-effort call on the same HostCaller, never a fresh grant.
	// Actual host scope closure still owns reclamation if this call is denied.
}

func TestSharedDownloadTorrentSourceExceptionEnvelope(t *testing.T) {
	for _, class := range []string{"Error", "JSONDecodeError", "UnicodeDecodeError", "UnicodeEncodeError", "AttributeError", "TypeError", "ValueError", "KeyError"} {
		t.Run(class, func(t *testing.T) {
			calls := 0
			chain := SharedDownloadChain{Host: func(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
				calls++
				if method != DownloadTorrentCapability {
					t.Fatal(method)
				}
				var request struct {
					Op string `json:"op"`
				}
				if json.Unmarshal(input, &request) != nil || request.Op != "prepare" {
					t.Fatal("unexpected request")
				}
				return json.Marshal(map[string]string{"kind": "source_error", "source_error": class})
			}}
			got, err := chain.DownloadTorrent(context.Background(), DownloadTorrentRequest{Torrent: json.RawMessage(`{}`)})
			var source *DownloadTorrentSourceError
			if !errors.As(err, &source) || source.Class != class || got.Content != nil || calls != 1 {
				t.Fatalf("source category %q: %#v %v calls%d", class, got, err, calls)
			}
		})
	}
	for _, raw := range []string{
		`{"kind":"source_error","source_error":null}`,
		`{"kind":"source_error","source_error":"private URL must not be an exception class"}`,
		`{"kind":"source_error","source_error":"Error","folder":""}`,
		`{"kind":"none","folder":"","files":[],"source_error":"Error"}`,
	} {
		chain := SharedDownloadChain{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}}
		_, err := chain.DownloadTorrent(context.Background(), DownloadTorrentRequest{Torrent: json.RawMessage(`{}`)})
		var source *DownloadTorrentSourceError
		if err == nil || errors.As(err, &source) {
			t.Fatalf("malformed source envelope accepted: %s %v", raw, err)
		}
	}
}
