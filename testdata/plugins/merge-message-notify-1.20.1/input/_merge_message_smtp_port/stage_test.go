package mergemessagesmtp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func TestActualPythonBase64ChallengeDecoder(t *testing.T) {
	count := 0
	corpus(t, "testdata/python-3.12.13-base64.jsonl.gz", func(raw json.RawMessage) {
		var c struct{ Raw, Result, Error string }
		if e := json.Unmarshal(raw, &c); e != nil {
			t.Fatal(e)
		}
		input, e := base64.StdEncoding.DecodeString(c.Raw)
		if e != nil {
			t.Fatal(e)
		}
		got, e := decodeChallenge(string(input))
		if c.Error != "" {
			var smtp *Error
			if !errors.As(e, &smtp) || smtp.Class != c.Error {
				t.Errorf("case%d expected %s got %v", count, c.Error, e)
			}
		} else if e != nil || base64.StdEncoding.EncodeToString(got) != c.Result {
			t.Errorf("case%d Base64 bytes differ input=%q got=%x error=%v want=%q", count, input, got, e, c.Result)
		}
		count++
	})
	if count != 8651 {
		t.Fatal(count)
	}
}

func TestActualPythonHeaderStages(t *testing.T) {
	count := 0
	corpus(t, "testdata/python-3.12.13-headers.jsonl.gz", func(raw json.RawMessage) {
		var c struct {
			Subject                    bool
			Name, Value, Result, Error string
			Maximum                    int
		}
		if e := json.Unmarshal(raw, &c); e != nil {
			t.Fatal(e)
		}
		got, e := encodeHeaderRaw(c.Name, c.Value, c.Subject, c.Maximum)
		if c.Error != "" {
			var err *Error
			if !errors.As(e, &err) || err.Class != c.Error {
				t.Errorf("case%d expected %s got %v", count, c.Error, e)
			}
		} else if e != nil || got != c.Result {
			t.Errorf("case%d raw Header stage differs max=%d value=%q got=%q error=%v want=%q", count, c.Maximum, c.Value, got, e, c.Result)
		}
		count++
	})
	if count != 216 {
		t.Fatal(count)
	}
}
func TestActualPythonStrictAddressParser(t *testing.T) {
	count := 0
	corpus(t, "testdata/python-3.12.13-addresses.jsonl.gz", func(raw json.RawMessage) {
		var c struct {
			Value, Quoted, Error string
			Parsed               []string
		}
		if e := json.Unmarshal(raw, &c); e != nil {
			t.Fatal(e)
		}
		p, e := parseAddress(c.Value)
		quoted, qe := quoteAddr(c.Value)
		if e != nil || qe != nil {
			t.Errorf("case%d value=%q errors=%v/%v", count, c.Value, e, qe)
		} else if len(c.Parsed) != 2 || p.name != c.Parsed[0] || p.addr != c.Parsed[1] || quoted != c.Quoted {
			t.Errorf("case%d value=%q got=(%q,%q) quote=%q want=%q quote=%q", count, c.Value, p.name, p.addr, quoted, c.Parsed, c.Quoted)
		}
		count++
	})
	if count != 6237 {
		t.Fatal(count)
	}
}
