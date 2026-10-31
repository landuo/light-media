package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Recording HostCaller only. These checks do not execute the source algorithm,
// SQLite, a worker, a network request or a real host authorization decision.
func TestSharedSiteAccessWire(t *testing.T) {
	ctx := context.Background()
	zero, negative := int64(0), int64(-3)
	minimum, maximum := int64(-9223372036854775808), int64(9223372036854775807)
	type invocation struct {
		method string
		raw    json.RawMessage
	}
	var calls []invocation
	s := SharedSiteAccess{Host: func(got context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if got != ctx {
			t.Fatal("invocation context changed")
		}
		calls = append(calls, invocation{method, bytes.Clone(raw)})
		return json.RawMessage(" \t\r\nnull\n"), nil
	}}
	for _, timing := range []*int64{nil, &zero, &negative, &minimum, &maximum} {
		if err := s.Success(ctx, "  Owned.雪.invalid/Path  ", timing); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Success(ctx, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, "  Owned.雪.invalid/Path  "); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, "owned\x00\n\"\\"); err != nil {
		t.Fatal(err)
	}
	want := []invocation{
		{"site.access.success", json.RawMessage(`{"domain":"  Owned.雪.invalid/Path  ","seconds":null}`)},
		{"site.access.success", json.RawMessage(`{"domain":"  Owned.雪.invalid/Path  ","seconds":0}`)},
		{"site.access.success", json.RawMessage(`{"domain":"  Owned.雪.invalid/Path  ","seconds":-3}`)},
		{"site.access.success", json.RawMessage(`{"domain":"  Owned.雪.invalid/Path  ","seconds":-9223372036854775808}`)},
		{"site.access.success", json.RawMessage(`{"domain":"  Owned.雪.invalid/Path  ","seconds":9223372036854775807}`)},
		{"site.access.success", json.RawMessage(`{"domain":"","seconds":null}`)},
		{"site.access.fail", json.RawMessage(`{"domain":"  Owned.雪.invalid/Path  "}`)},
		{"site.access.fail", json.RawMessage(`{"domain":""}`)},
		{"site.access.fail", json.RawMessage(`{"domain":"owned\u0000\n\"\\"}`)},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls=%d, want %d", len(calls), len(want))
	}
	for i := range want {
		if calls[i].method != want[i].method || !bytes.Equal(calls[i].raw, want[i].raw) {
			t.Fatalf("call %d: %s %s, want %s %s", i, calls[i].method, calls[i].raw, want[i].method, want[i].raw)
		}
	}
}

func TestSharedSiteAccessBoundaries(t *testing.T) {
	ctx := context.Background()
	calls := 0
	s := SharedSiteAccess{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`null`), nil
	}}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Success(canceled, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-call cancellation changed: %v", err)
	}
	if err := s.Fail(canceled, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-call fail cancellation changed: %v", err)
	}
	for i, call := range []func() error{
		func() error { return s.Success(nil, "owned", nil) },
		func() error { return s.Fail(nil, "owned") },
		func() error { return (SharedSiteAccess{}).Success(ctx, "owned", nil) },
		func() error { return (SharedSiteAccess{}).Fail(ctx, "owned") },
		func() error { return s.Success(ctx, string([]byte{0xff}), nil) },
		func() error { return s.Fail(ctx, string([]byte{0xff})) },
		func() error { return s.Success(ctx, strings.Repeat("x", 128<<10), nil) },
		func() error { return s.Fail(ctx, strings.Repeat("x", 128<<10)) },
	} {
		if err := call(); err == nil {
			t.Fatalf("invalid request %d accepted", i)
		}
	}
	if calls != 0 {
		t.Fatalf("rejected input reached host %d times", calls)
	}

	for _, raw := range []json.RawMessage{
		nil, {}, []byte(`false`), []byte(`true`), []byte(`{}`), []byte(`[]`),
		[]byte(`0`), []byte(`"null"`), []byte(`{"ok":null}`),
		[]byte(`{"value":null,"value":null}`), []byte(`null null`),
		[]byte("\u00a0null"), []byte{'n', 'u', 'l', 'l', 0xff},
		[]byte(strings.Repeat(" ", MaxPayloadBytes) + "null"),
	} {
		s.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return raw, nil
		}
		before := calls
		if err := s.Success(ctx, "owned", nil); err == nil {
			t.Fatalf("invalid success reply accepted (length %d)", len(raw))
		}
		if err := s.Fail(ctx, "owned"); err == nil {
			t.Fatalf("invalid fail reply accepted (length %d)", len(raw))
		}
		if calls != before+2 {
			t.Fatal("invalid response was retried")
		}
	}

	sentinel := errors.New("owned authority or database failure")
	for _, hostError := range []error{sentinel, context.Canceled, context.DeadlineExceeded} {
		s.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`null`), hostError
		}
		before := calls
		if err := s.Success(ctx, "owned", nil); err != hostError {
			t.Fatalf("success host error changed: %v", err)
		}
		if err := s.Fail(ctx, "owned"); err != hostError {
			t.Fatalf("fail host error changed: %v", err)
		}
		if calls != before+2 {
			t.Fatal("failed mutation was retried")
		}
	}

	for _, success := range []bool{true, false} {
		late, lateCancel := context.WithCancel(ctx)
		s.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			lateCancel() // Host may already have committed; cancellation is not rollback.
			return json.RawMessage(`null`), nil
		}
		before := calls
		var err error
		if success {
			err = s.Success(late, "owned", nil)
		} else {
			err = s.Fail(late, "owned")
		}
		if !errors.Is(err, context.Canceled) || calls != before+1 {
			t.Fatalf("post-call cancellation lost or retried: %v, calls=%d", err, calls-before)
		}
		lateCancel()
	}
}
