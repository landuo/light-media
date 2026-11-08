package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceAddWireValidationKeepsFiniteRequestBoundary(t *testing.T) {
	text := "false"
	flag := false
	valid := DownloaderSourceAddRequestV1{DownloaderAddRequestV1: DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned")}, SkipCheckingWire: &text}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(valid)
	if err != nil || !strings.Contains(string(raw), `"skip_checking_wire":"false"`) || strings.Contains(string(raw), `"skip_checking":`) {
		t.Fatal("source-only text field changed", string(raw), err)
	}
	cases := []DownloaderSourceAddRequestV1{valid, valid, valid, valid}
	cases[0].SkipChecking = &flag
	invalid := string([]byte{0xff})
	cases[1].SkipCheckingWire = &invalid
	oversize := strings.Repeat("x", MaxPayloadBytes+1)
	cases[2].SkipCheckingWire = &oversize
	escaped := strings.Repeat("\x00", MaxPayloadBytes/6)
	cases[3].SkipCheckingWire = &escaped
	calls := 0
	d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, nil }}
	for i, r := range cases {
		if _, err := d.SourceAdd(context.Background(), r); err == nil {
			t.Fatal("invalid wire request accepted", i)
		}
	}
	if calls != 0 {
		t.Fatal("invalid wire contacted host", calls)
	}
}
