package mergemessagesmtp

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBindingFromFrozenComponentConfig(t *testing.T) {
	config := map[string]any{"smtp_host": "smtp.synthetic.invalid", "smtp_port": json.Number("2525"), "smtp_encrypt_type": "NONE", "username": "sender@example.invalid", "password": "synthetic-password", "to_addrs": " one@example.invalid ,\x1ctwo@example.invalid\x1f,one@example.invalid ", "unknown": map[string]any{"large": json.Number("9007199254740993")}}
	b, e := BindingFromTrustedConfig(config)
	if e != nil {
		t.Fatal(e)
	}
	if b.Port != 2525 || b.Encrypt != "NONE" || b.FromName != "MoviePilot" || !reflect.DeepEqual(b.Recipients, []string{"one@example.invalid", "two@example.invalid"}) {
		t.Fatal("source config semantics lost")
	}
	b.Recipients[0] = "mutation"
	if config["unknown"].(map[string]any)["large"] != json.Number("9007199254740993") {
		t.Fatal("unknown config rewritten")
	}
	for _, value := range []any{false, 0, "", []any{}, map[string]any{}} {
		config["from_name"] = value
		b, e = BindingFromTrustedConfig(config)
		if e != nil || b.FromName != "" {
			t.Fatal("falsey explicit name did not omit", value, e)
		}
	}
	for _, value := range []any{true, 1, []any{"value"}, map[string]any{"x": 1}} {
		config["from_name"] = value
		if _, e = BindingFromTrustedConfig(config); !errors.Is(e, ErrUnsupported) {
			t.Fatal("truthy dynamic name silently stringified")
		}
	}
	config["from_name"] = nil
	config["smtp_encrypt_type"] = ""
	if _, e = BindingFromTrustedConfig(config); !errors.Is(e, ErrNotConfigured) {
		t.Fatal("implicit plaintext accepted")
	}
	config["smtp_encrypt_type"] = "TLS"
	config["smtp_port"] = "submission"
	if _, e = BindingFromTrustedConfig(config); !errors.Is(e, ErrUnsupported) {
		t.Fatal("unported service-name port accepted")
	}
}
func TestDeepAddressResourceBranchFailsExplicitly(t *testing.T) {
	if _, e := quoteAddr(strings.Repeat("(", 450) + "comment" + strings.Repeat(")", 450) + "a@example.invalid"); !errors.Is(e, ErrUnsupported) {
		t.Fatal("unproved CPython recursion-boundary input accepted", e)
	}
}
