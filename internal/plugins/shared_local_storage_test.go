package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Unit-only shape/error tests; real transport/host/physical effects live in App.
func TestSharedLocalStorageTypedRequestShapes(t *testing.T) {
	item, e := NewModuleFileItem(map[string]any{"path": "/owned/media.mkv", "storage": nil, "children": nil})
	if e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{LocalStorageExistsCapability, LocalStorageListCapability, LocalStorageDeleteCapability} {
		calls := 0
		s := SharedLocalStorage{Host: func(_ context.Context, actual string, raw json.RawMessage) (json.RawMessage, error) {
			calls++
			if actual != method {
				t.Fatal(actual, method)
			}
			var in LocalStorageRequest
			if e := json.Unmarshal(raw, &in); e != nil {
				t.Fatal(e)
			}
			if string(in.FileItem.Fields["storage"]) != "null" || string(in.FileItem.Fields["children"]) != "null" {
				t.Fatal("explicit null lost")
			}
			if method == LocalStorageListCapability && !in.Recursion {
				t.Fatal("recursion lost")
			}
			return json.RawMessage(`false`), nil
		}}
		switch method {
		case LocalStorageExistsCapability:
			v, e := s.Exists(context.Background(), item)
			if e != nil || v {
				t.Fatal(v, e)
			}
		case LocalStorageListCapability:
			raw, e := s.List(context.Background(), item, true)
			if e != nil || string(raw) != "false" {
				t.Fatal(string(raw), e)
			}
		default:
			raw, e := s.DeleteFile(context.Background(), item)
			if e != nil || string(raw) != "false" {
				t.Fatal(string(raw), e)
			}
		}
		if calls != 1 {
			t.Fatal(calls)
		}
	}
}
func TestSharedLocalStorageErrorsAndNullableModuleResults(t *testing.T) {
	ctx := context.Background()
	if _, e := (SharedLocalStorage{}).Exists(ctx, nil); e == nil {
		t.Fatal("missing scope accepted")
	}
	failure := errors.New("owned failure")
	s := SharedLocalStorage{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, failure }}
	if _, e := s.List(ctx, nil, false); !errors.Is(e, failure) {
		t.Fatal(e)
	}
	if _, e := s.DeleteFile(ctx, nil); !errors.Is(e, failure) {
		t.Fatal(e)
	}
	s.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`null`), nil
	}
	for _, f := range []func() (json.RawMessage, error){func() (json.RawMessage, error) { return s.List(ctx, nil, false) }, func() (json.RawMessage, error) { return s.DeleteFile(ctx, nil) }} {
		v, e := f()
		if e != nil || string(v) != "null" {
			t.Fatal(string(v), e)
		}
	}
}
