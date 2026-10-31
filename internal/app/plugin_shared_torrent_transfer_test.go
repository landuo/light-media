package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"light-media/internal/plugins"
	"sync"
	"testing"
)

func TestOriginalTorrentTransferScopeAndCleanup(t *testing.T) {
	ctx := context.Background()
	owner := newOriginalTorrentTransfers()
	input := bytes.Repeat([]byte{0x42}, plugins.TorrentMaterialChunkBytes+7)
	want := sha256.Sum256(input)
	prepared, err := owner.publish(ctx, input)
	if err != nil || len(prepared.Handle) != 64 || prepared.Total != len(input) || prepared.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("prepare: %#v %v", prepared, err)
	}
	input[0] = 0
	foreign := newOriginalTorrentTransfers()
	if _, err = foreign.read(ctx, prepared.Handle, 0); err == nil {
		t.Fatal("foreign scope accepted handle")
	}
	if err = foreign.release(ctx, prepared.Handle); err == nil {
		t.Fatal("foreign scope released handle")
	}
	if _, err = owner.read(ctx, prepared.Handle, 1); err == nil {
		t.Fatal("out-of-order read")
	}
	first, err := owner.read(ctx, prepared.Handle, 0)
	if err != nil || len(first.Data) != plugins.TorrentMaterialChunkBytes || first.Data[0] != 0x42 {
		t.Fatalf("first: %d %v", len(first.Data), err)
	}
	first.Data[0] = 0
	if _, err = owner.read(ctx, prepared.Handle, 0); err == nil {
		t.Fatal("replayed offset")
	}
	last, err := owner.read(ctx, prepared.Handle, plugins.TorrentMaterialChunkBytes)
	if err != nil || len(last.Data) != 7 || last.Offset != plugins.TorrentMaterialChunkBytes {
		t.Fatalf("last: %#v %v", last, err)
	}
	if _, err = owner.read(ctx, prepared.Handle, prepared.Total); err == nil {
		t.Fatal("read beyond final byte")
	}
	if err = owner.release(ctx, prepared.Handle); err != nil {
		t.Fatal(err)
	}
	if owner.used != 0 || len(owner.values) != 0 {
		t.Fatal("release retained allocation")
	}
	if _, err = owner.read(ctx, prepared.Handle, 0); err == nil {
		t.Fatal("released token remains usable")
	}
	empty, err := owner.publish(ctx, nil)
	if err != nil || empty.Total != 0 {
		t.Fatalf("emptybytes: %#v %v", empty, err)
	}
	if _, err = owner.read(ctx, empty.Handle, 0); err == nil {
		t.Fatal("empty material has a read chunk")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = owner.publish(cancelled, []byte("owned")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = owner.release(cancelled, empty.Handle); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	owner.close()
	owner.close()
	if owner.used != 0 || owner.values != nil || !owner.closed {
		t.Fatal("close did not clear all invocation handles")
	}
	if _, err = owner.publish(ctx, nil); err == nil {
		t.Fatal("closed scope resurrected")
	}
	if err = owner.release(ctx, empty.Handle); err == nil {
		t.Fatal("cleanup revived expired authority")
	}
}

func TestOriginalTorrentTransferPerObjectAndConcurrentClose(t *testing.T) {
	ctx := context.Background()
	owner := newOriginalTorrentTransfers()
	first, err := owner.publish(ctx, []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := owner.publish(ctx, []byte("another independent material"))
	if err != nil {
		t.Fatal("parallel material was rejected", err)
	}
	if err = owner.release(ctx, second.Handle); err != nil {
		t.Fatal(err)
	}
	if err = owner.release(ctx, first.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.publish(ctx, bytes.Repeat([]byte{'x'}, plugins.TorrentMaterialMaxBytes+1)); err == nil {
		t.Fatal("per-material resource guard not enforced")
	}
	for i := 0; i < 64; i++ {
		if _, err = owner.publish(ctx, nil); err != nil {
			t.Fatal(i, err)
		}
	}
	owner.close()
	owner = newOriginalTorrentTransfers()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := owner.publish(ctx, []byte("x"))
			if err == nil {
				_, _ = owner.read(ctx, p.Handle, 0)
				_ = owner.release(ctx, p.Handle)
			}
		}()
	}
	owner.close()
	wg.Wait()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if !owner.closed || owner.values != nil || owner.used != 0 {
		t.Fatal("concurrent close leaked transfer")
	}
}
