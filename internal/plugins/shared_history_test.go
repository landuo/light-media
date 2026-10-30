package plugins

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSharedHistoryTypedQueryRejectsInvalidUTF8BeforeHost(t *testing.T) {
	invalid := string([]byte{0xff})
	valid := "/owned/valid"
	for _, byDestination := range []bool{false, true} {
		for _, field := range []string{"path", "storage"} {
			t.Run(map[bool]string{false: "source", true: "destination"}[byDestination]+"/"+field, func(t *testing.T) {
				path, storage := valid, valid
				if field == "path" {
					path = invalid
				} else {
					storage = invalid
				}
				calls := 0
				sdk := Histories{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
					calls++
					return json.RawMessage(`null`), nil
				}}
				var err error
				if byDestination {
					_, err = sdk.TransferByDestination(context.Background(), &path, &storage)
				} else {
					_, err = sdk.TransferBySource(context.Background(), &path, &storage)
				}
				if err == nil || calls != 0 {
					t.Fatalf("invalid typed string crossed host boundary: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestSharedHistoryTypedQueryPreservesUnicodeAndNull(t *testing.T) {
	replacement, emoji, empty := "/owned/\ufffd", "/owned/\U0001f642", ""
	for _, byDestination := range []bool{false, true} {
		for _, value := range []*string{nil, &empty, &replacement, &emoji} {
			calls := 0
			sdk := Histories{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				expected := TransferHistoryBySourceCapability
				key := "src"
				if byDestination {
					expected = TransferHistoryByDestinationCapability
					key = "dest"
				}
				if method != expected {
					t.Fatal("unexpected capability", method)
				}
				var fields map[string]*string
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(fields[key], value) || !reflect.DeepEqual(fields["storage"], value) {
					t.Fatalf("query strings changed: %s", raw)
				}
				return json.RawMessage(`null`), nil
			}}
			var err error
			if byDestination {
				_, err = sdk.TransferByDestination(context.Background(), value, value)
			} else {
				_, err = sdk.TransferBySource(context.Background(), value, value)
			}
			if err != nil || calls != 1 {
				t.Fatalf("valid typed string rejected: calls=%d err=%v", calls, err)
			}
		}
	}
}
