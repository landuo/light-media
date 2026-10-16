package imagehash

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"testing"
)

func TestJPEGAllocationBudgetBeforeRasterOrProgressiveBuffers(t *testing.T) {
	var encoded []byte
	for _, c := range fixture(t).Encoded {
		if c.Format == "JPEG" && c.Mode == "RGB" && c.Width > 1 && c.Height > 1 {
			encoded = append([]byte(nil), c.Encoded...)
			break
		}
	}
	if encoded == nil {
		t.Fatal("missing controlled JPEG")
	}
	sof := -1
	for i := 2; i+10 < len(encoded); i++ {
		if encoded[i] == 255 && encoded[i+1] == 0xc0 {
			sof = i
			break
		}
	}
	if sof < 0 {
		t.Fatal("missing SOF0")
	}
	// This dimension is within the general 16Mi-pixel boundary. It would
	// otherwise allocate large native/component buffers before an entropy EOF.
	binary.BigEndian.PutUint16(encoded[sof+5:sof+7], 4096)
	binary.BigEndian.PutUint16(encoded[sof+7:sof+9], 4096)
	for _, marker := range []byte{0xc0, 0xc2} {
		encoded[sof+1] = marker
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < 5; i++ {
			_, e := DecodeJPEG(encoded)
			if !errors.Is(e, ErrInvalid) || !bytes.Contains([]byte(e.Error()), []byte("64 MiB")) {
				t.Fatalf("missing preallocation budget: %v", e)
			}
		}
		runtime.ReadMemStats(&after)
		if after.TotalAlloc-before.TotalAlloc > 4<<20 {
			t.Fatalf("resource rejection allocated raster buffers: %d bytes", after.TotalAlloc-before.TotalAlloc)
		}
	}
	if _, e := DecodeJPEG(make([]byte, (8<<20)+1)); !errors.Is(e, ErrInvalid) {
		t.Fatal("unbounded direct JPEG input")
	}
}

func TestJPEGUnsupportedModeDoesNotFallBack(t *testing.T) {
	var encoded []byte
	for _, c := range fixture(t).Encoded {
		if c.Format == "JPEG" {
			encoded = append([]byte(nil), c.Encoded...)
			break
		}
	}
	for i := 2; i+2 < len(encoded); i++ {
		if encoded[i] == 255 && encoded[i+1] == 0xc0 {
			encoded[i+1] = 0xc9
			break
		}
	}
	if _, e := Hash(encoded); !errors.Is(e, ErrUnsupported) {
		t.Fatalf("unsupported arithmetic marker not explicit: %v", e)
	}
}
