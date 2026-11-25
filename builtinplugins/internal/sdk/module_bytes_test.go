package plugins

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestModuleBytesRetainsBinaryAndScalarIdentity(t *testing.T) {
	for _, value := range [][]byte{nil, {}, {0, 255, 254, '\n', 128}} {
		raw, err := ModuleResultBytes(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded, yes := DecodeModuleResultBytes(raw)
		if !yes || !bytes.Equal(decoded, value) {
			t.Fatal("binary module transport changed", string(raw), decoded)
		}
		if _, tuple := DecodeModuleResultTuple(raw); tuple {
			t.Fatal("bytes mistaken for tuple")
		}
	}
	for _, raw := range []string{`null`, `""`, `"/owned/file.torrent"`, `[]`, `{}`, `{"__plugin_module_bytes":null}`, `{"__plugin_module_bytes":"not-base64"}`, `{"__plugin_module_bytes":"","extra":true}`} {
		if _, yes := DecodeModuleResultBytes(json.RawMessage(raw)); yes {
			t.Fatal("ordinary JSON or invalid envelope treated as bytes", raw)
		}
	}
}
