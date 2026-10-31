package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"light-media/internal/plugins"
)

func downloaderUploadDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func downloaderUploadPacket(t *testing.T, handle string, offset int, data []byte) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(struct {
		Op     string `json:"op"`
		Handle string `json:"handle"`
		Offset int    `json:"offset"`
		Data   []byte `json:"data"`
	}{"upload_append", handle, offset, data})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDownloaderCommandUploadsCompleteBytesAndState(t *testing.T) {
	ctx := context.Background()
	for _, size := range []int{0, plugins.TorrentMaterialChunkBytes + 3, plugins.TorrentMaterialMaxBytes} {
		owner := newOriginalDownloaderCommandUploads()
		input := bytes.Repeat([]byte{0x6d}, size)
		capability := originalDownloaderUploadQBAdd
		if size == 0 {
			capability = originalDownloaderUploadTRAdd
		}
		handle, err := owner.begin(ctx, capability, len(input), downloaderUploadDigest(input))
		if err != nil || !originalDownloaderUploadHandle(handle) {
			t.Fatal("begin", size, handle, err)
		}
		if owner.values[handle].content != nil {
			t.Fatal("begin eagerly allocated declared bytes")
		}
		pages := 0
		for offset := 0; offset < len(input); {
			end := min(offset+plugins.TorrentMaterialChunkBytes, len(input))
			packet := downloaderUploadPacket(t, handle, offset, input[offset:end])
			chunk, err := decodeOriginalDownloaderUploadAppend(packet)
			if err != nil {
				t.Fatal("real private decoder", size, offset, err)
			}
			next, err := owner.append(ctx, capability, chunk)
			if err != nil || next != end {
				t.Fatal("append", size, offset, next, err)
			}
			chunk.Data[0] ^= 0xff // Owner must not retain the caller's mutable slice.
			if _, err := owner.append(ctx, capability, chunk); err == nil {
				t.Fatal("duplicate append accepted")
			}
			pages++
			offset = end
		}
		if pages != (size+plugins.TorrentMaterialChunkBytes-1)/plugins.TorrentMaterialChunkBytes {
			t.Fatal("wrong actual page count", size, pages)
		}
		content, err := owner.claim(ctx, capability, handle)
		if err != nil || content == nil || !bytes.Equal(content, input) {
			t.Fatal("complete bytes/empty identity", size, len(content), err)
		}
		if !owner.values[handle].consumed || owner.values[handle].content != nil {
			t.Fatal("claim did not retain only consumed tombstone")
		}
		if _, err := owner.claim(ctx, capability, handle); err == nil {
			t.Fatal("repeated submit restored consumed bytes")
		}
		if err := owner.abort(ctx, capability, handle); err == nil {
			t.Fatal("consumed token falsely acknowledged rollback")
		}
		owner.close()
		owner.close()
		if !bytes.Equal(content, input) || content == nil || owner.values != nil {
			t.Fatal("close cleared a claimed callback's bytes or retained entries")
		}
		if _, err := owner.claim(ctx, capability, handle); err == nil {
			t.Fatal("closed owner resurrected claim")
		}
	}

	owner := newOriginalDownloaderCommandUploads()
	capability := originalDownloaderUploadQBAdd
	if _, err := owner.begin(ctx, capability, 0, strings.Repeat("0", 64)); err == nil {
		t.Fatal("empty total bypassed empty digest validation")
	}
	if _, err := owner.begin(ctx, "downloader.any.add", 0, downloaderUploadDigest(nil)); err == nil {
		t.Fatal("unlisted capability admitted")
	}
	if _, err := owner.begin(ctx, capability, plugins.TorrentMaterialMaxBytes+1, downloaderUploadDigest(nil)); err == nil {
		t.Fatal("oversized total admitted")
	}
	handle, err := owner.begin(ctx, capability, 1, downloaderUploadDigest([]byte("A")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.claim(ctx, capability, handle); err == nil {
		t.Fatal("incomplete bytes claimed")
	}
	if _, err := owner.append(ctx, originalDownloaderUploadTRAdd, originalDownloaderUploadAppend{handle, 0, []byte("A")}); err == nil {
		t.Fatal("different add capability borrowed token")
	}
	foreign := newOriginalDownloaderCommandUploads()
	if _, err := foreign.claim(ctx, capability, handle); err == nil {
		t.Fatal("another invocation borrowed token")
	}
	if _, err := owner.append(ctx, capability, originalDownloaderUploadAppend{handle, 1, []byte("A")}); err == nil || len(owner.values[handle].content) != 0 {
		t.Fatal("wrong offset advanced upload")
	}
	if _, err := owner.append(ctx, capability, originalDownloaderUploadAppend{handle, 0, []byte("B")}); err == nil {
		t.Fatal("wrong final digest accepted")
	}
	if _, err := owner.append(ctx, capability, originalDownloaderUploadAppend{handle, 0, []byte("A")}); err == nil {
		t.Fatal("integrity failure was silently rewound")
	}
	if _, err := owner.claim(ctx, capability, handle); err == nil {
		t.Fatal("integrity failed bytes escaped")
	}
	if err := owner.abort(ctx, capability, handle); err != nil || len(owner.values) != 0 {
		t.Fatal("failed unused upload was not reclaimed", err)
	}
	outbound := newOriginalTorrentTransfers()
	old, err := outbound.publish(ctx, []byte("owned released source bytes"))
	if err != nil || outbound.release(ctx, old.Handle) != nil {
		t.Fatal("existing outbound setup", err)
	}
	if _, err := owner.claim(ctx, capability, old.Handle); err == nil {
		t.Fatal("released S02/S12 outbound token revived as inbound")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := owner.begin(cancelled, capability, 0, downloaderUploadDigest(nil)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled begin", err)
	}
	var nilOwner *originalDownloaderCommandUploads
	if _, err := nilOwner.begin(ctx, capability, 0, downloaderUploadDigest(nil)); err == nil {
		t.Fatal("nil owner admitted")
	}
	if _, err := (&originalDownloaderCommandUploads{}).begin(ctx, capability, 0, downloaderUploadDigest(nil)); err == nil {
		t.Fatal("uninitialized owner admitted")
	}
	owner.close()
}

func TestDownloaderCommandUploadsStrictAppendDecoder(t *testing.T) {
	handle := "u_" + strings.Repeat("1", 64)
	raw := downloaderUploadPacket(t, handle, 0, bytes.Repeat([]byte{'x'}, plugins.TorrentMaterialChunkBytes))
	if len(raw) <= 128<<10 || len(raw) > originalDownloaderUploadAppendMaxBytes {
		t.Fatal("fixture does not exercise the bounded private decoder exception", len(raw))
	}
	chunk, err := decodeOriginalDownloaderUploadAppend(raw)
	if err != nil || chunk.Offset != 0 || len(chunk.Data) != plugins.TorrentMaterialChunkBytes {
		t.Fatal("offset zero or exact 1MiB packet rejected", err)
	}
	var generic map[string]any
	if err := originalResourceDecode(raw, &generic); err == nil {
		t.Fatal("generic 128KiB resource decoder was broadened")
	}
	padded := append(bytes.Clone(raw), bytes.Repeat([]byte{' '}, originalDownloaderUploadAppendMaxBytes-len(raw))...)
	if _, err := decodeOriginalDownloaderUploadAppend(padded); err != nil {
		t.Fatal("exact raw bound rejected", err)
	}
	if _, err := decodeOriginalDownloaderUploadAppend(append(padded, ' ')); err == nil {
		t.Fatal("oversized raw packet accepted")
	}
	good := `{"op":"upload_append","handle":"` + handle + `","offset":0,"data":"QQ=="}`
	for name, invalid := range map[string]string{
		"missing-offset": strings.Replace(good, `"offset":0,`, "", 1),
		"null-offset":    strings.Replace(good, `"offset":0`, `"offset":null`, 1),
		"missing-data":   strings.Replace(good, `,"data":"QQ=="`, "", 1),
		"null-data":      strings.Replace(good, `"data":"QQ=="`, `"data":null`, 1),
		"array-data":     strings.Replace(good, `"data":"QQ=="`, `"data":[65]`, 1),
		"no-progress":    strings.Replace(good, `"QQ=="`, `""`, 1),
		"duplicate":      strings.Replace(good, `"offset":0`, `"offset":0,"offset":0`, 1),
		"unknown":        strings.Replace(good, `"offset":0`, `"offset":0,"input":{}`, 1),
		"case-alias":     strings.Replace(good, `"offset":0`, `"Offset":0`, 1),
		"wrong-op":       strings.Replace(good, "upload_append", "submit", 1),
		"noninteger":     strings.Replace(good, `"offset":0`, `"offset":0.5`, 1),
		"trailing":       good + `{}`,
		"bad-base64":     strings.Replace(good, "QQ==", "QR==", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOriginalDownloaderUploadAppend(json.RawMessage(invalid)); err == nil {
				t.Fatal("invalid fixed append shape accepted")
			}
		})
	}
	if _, err := decodeOriginalDownloaderUploadAppend(downloaderUploadPacket(t, handle, 0, bytes.Repeat([]byte{'x'}, plugins.TorrentMaterialChunkBytes+1))); err == nil {
		t.Fatal("decoded page exceeded 1MiB")
	}
}

func TestDownloaderCommandUploadsConcurrentClaimAndClose(t *testing.T) {
	ctx := context.Background()
	for _, raceClose := range []bool{false, true} {
		owner := newOriginalDownloaderCommandUploads()
		want := []byte("owned callback keeps claimed bytes until it really exits")
		handle, err := owner.begin(ctx, originalDownloaderUploadQBAdd, len(want), downloaderUploadDigest(want))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.append(ctx, originalDownloaderUploadQBAdd, originalDownloaderUploadAppend{handle, 0, want}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan []byte, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				content, err := owner.claim(ctx, originalDownloaderUploadQBAdd, handle)
				if err == nil {
					results <- content
				}
			}()
		}
		if raceClose {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; owner.close() }()
		}
		close(start)
		wg.Wait()
		close(results)
		owner.close()
		claimed := 0
		for content := range results {
			claimed++
			if !bytes.Equal(content, want) {
				t.Fatal("owner close zeroed actual handed-off bytes")
			}
		}
		if claimed > 1 || !raceClose && claimed != 1 {
			t.Fatal("claim was not atomic exactly once before close", claimed, raceClose)
		}
		if _, err := owner.claim(ctx, originalDownloaderUploadQBAdd, handle); err == nil {
			t.Fatal("closed invocation admitted another dispatch claim")
		}
	}
}
