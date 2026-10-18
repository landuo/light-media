package iyuuautoseed

import (
	"testing"
)

func TestJSONSurrogateBoundaryIsExplicit(t *testing.T) {
	for _, raw := range []string{`"\ud800"`, `"\uDC00"`, `{"x":"\ud800x"}`, `["\ud800\ud800"]`} {
		if _, e := decodeJSON([]byte(raw)); e == nil {
			t.Fatal("unpaired surrogate replaced", raw)
		}
	}
	for _, raw := range []string{`"\ud83d\ude00"`, `"\\ud800"`, `"�"`, `"quoted\"word"`} {
		if _, e := decodeJSON([]byte(raw)); e != nil {
			t.Fatal("valid JSON rejected", raw, e)
		}
	}
}
