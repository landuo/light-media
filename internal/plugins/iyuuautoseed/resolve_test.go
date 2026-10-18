package iyuuautoseed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

type resolverOracleHTTP struct {
	responses   []any
	decoded     []any
	calls       int
	decodeIndex int
}

func (h *resolverOracleHTTP) take() *APIResponse {
	h.calls++
	if len(h.responses) == 0 {
		return nil
	}
	raw := h.responses[0]
	h.responses = h.responses[1:]
	if raw == nil {
		return nil
	}
	m := raw.(map[string]any)
	n, _ := m["status"].(json.Number).Int64()
	body, _ := base64.StdEncoding.DecodeString(m["content"].(string))
	return &APIResponse{Status: int(n), Body: body}
}
func (h *resolverOracleHTTP) MTeamToken(context.Context, string, any) (*APIResponse, error) {
	r := h.take()
	if r != nil {
		h.decodeIndex++
	}
	return r, nil
}
func (h *resolverOracleHTTP) Page(context.Context, string, PageKind, any) (*PageResponse, error) {
	r := h.take()
	if r == nil {
		return nil, nil
	}
	text := ""
	if h.decodeIndex < len(h.decoded) {
		text = h.decoded[h.decodeIndex].(map[string]any)["text"].(string)
	}
	h.decodeIndex++
	return &PageResponse{Status: r.Status, Text: text}, nil
}
func TestInstalledResolverSourceOracleWithDecodedPages(t *testing.T) {
	raw, e := os.ReadFile("reference/resolver-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	rows := v.(map[string]any)["cases"].([]any)
	failures := 0
	for _, raw := range rows {
		c := raw.(map[string]any)
		h := &resolverOracleHTTP{responses: append([]any{}, c["responses"].([]any)...), decoded: c["decoded"].([]any)}
		r := Resolver{HTTP: h}
		got, e := r.Resolve(context.Background(), c["seed"].(map[string]any), Site{Ref: "owned-site", Fields: c["site"].(map[string]any)}, c["template"].(string))
		want := c["result"].(map[string]any)["value"]
		var expected string
		if want != nil {
			expected = want.(string)
		}
		if e != nil || got != expected || h.calls != len(c["calls"].([]any)) {
			if failures < 25 {
				t.Errorf("%s got%q/%v calls%d want%q calls%d", c["name"], got, e, h.calls, expected, len(c["calls"].([]any)))
			}
			failures++
		}
	}
	if failures > 0 {
		t.Fatalf("%d/%d resolver cases differed", failures, len(rows))
	}
}

func TestInstalledResolverSourceLogOracleWithDecodedPages(t *testing.T) {
	raw, e := os.ReadFile("reference/resolver-log-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	rows := v.(map[string]any)["cases"].([]any)
	failures := 0
	for _, raw := range rows {
		c := raw.(map[string]any)
		logs := []any{}
		ctx := WithSourceLogSink(context.Background(), func(_ context.Context, event SourceLog) {
			logs = append(logs, map[string]any{"line": event.Line, "level": event.Level, "message": event.Message})
		})
		h := &resolverOracleHTTP{responses: append([]any{}, c["responses"].([]any)...), decoded: c["decoded"].([]any)}
		r := Resolver{HTTP: h}
		got, e := r.Resolve(ctx, c["seed"].(map[string]any), Site{Ref: "owned-site", Fields: c["site"].(map[string]any)}, c["template"].(string))
		// Source exception payloads are represented by safe error class/operation
		// in Go. Do not require CPython exception wording or raw external bodies;
		// retain exact original line/level/order and all ordinary business text.
		normalize := func(rows []any) []any {
			out := make([]any, 0, len(rows))
			for _, raw := range rows {
				row, _ := cloneMap(raw.(map[string]any))
				n, _ := floatValue(row["line"])
				if n == 1462 || n == 1401 || n == 1508 {
					row["message"] = "[source exception detail redacted]"
				}
				out = append(out, row)
			}
			return out
		}
		if !sameLogJSON(normalize(logs), normalize(c["logs"].([]any))) {
			if failures < 25 {
				t.Errorf("%s source logs got%v want%v", c["name"], normalize(logs), normalize(c["logs"].([]any)))
			}
			failures++
		}
		want := c["result"].(map[string]any)["value"]
		var expected string
		if want != nil {
			expected = want.(string)
		}
		if e != nil || got != expected || h.calls != len(c["calls"].([]any)) {
			if failures < 25 {
				t.Errorf("%s got%q/%v calls%d want%q calls%d", c["name"], got, e, h.calls, expected, len(c["calls"].([]any)))
			}
			failures++
		}
	}
	if failures > 0 {
		t.Fatalf("%d/%d resolver cases differed", failures, len(rows))
	}
}

func sameLogJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
