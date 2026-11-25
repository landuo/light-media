package encodingdetector

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestDeclaredCharsetSkipsDetectionModels(t *testing.T) {
	// The main test process may already have run an undeclared-charset case.
	if os.Getenv("TEST_DECLARED_CHARSET_ONLY") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDeclaredCharsetSkipsDetectionModels$")
		cmd.Env = append(os.Environ(), "TEST_DECLARED_CHARSET_ONLY=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return
	}
	got, err := Decode([]byte("光影"), "UTF-8", false)
	if err != nil || got != "光影" || len(models) != 0 {
		t.Fatalf("decoded %q, models loaded %d, error %v", got, len(models), err)
	}
}

func TestSparseWeightsMatchEveryReferenceRow(t *testing.T) {
	if err := loadModels(); err != nil {
		t.Fatal(err)
	}
	b, err := assets.ReadFile("models/models.bin")
	if err != nil {
		t.Fatal(err)
	}
	offset := 8
	for range int(binary.BigEndian.Uint32(b[4:8])) {
		offset += 12 + int(binary.BigEndian.Uint32(b[offset:]))
	}
	reader, err := zlib.NewReader(bytes.NewReader(b[offset:]))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	dense, decoded := make([]byte, 65536), make([]byte, 65536)
	for _, m := range models {
		if _, err := io.ReadFull(reader, dense); err != nil {
			t.Fatal(err)
		}
		clear(decoded)
		for j, index := range m.Indices {
			decoded[index] = m.Weights[j]
		}
		if !bytes.Equal(decoded, dense) {
			t.Fatalf("different weights for %s", m.Key)
		}
	}
}
