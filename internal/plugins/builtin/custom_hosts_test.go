package builtin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCustomHostsRejectsNonTextErrorRecords(t *testing.T) {
	for _, raw := range []string{`{"enabled":true,"hosts":"","err_hosts":{}}`, `{"err_hosts":[1]}`} {
		var config CustomHostsConfiguration
		if json.Unmarshal([]byte(raw), &config) != nil || config.Validate() == nil {
			t.Fatal("invalid error field accepted")
		}
	}
}

func TestCustomHostsPreservesPrefixAndOriginalDuplicateRules(t *testing.T) {
	original := "# baseline\n127.0.0.1 localhost\n203.0.113.7 old.example\n" + CustomHostsMarker + "\n192.0.2.1 stale.example\n"
	content, normalized, invalid, valid := BuildCustomHosts(original, []string{
		"203.0.113.7 new.example",                        // Existing address: skip, even with a new name.
		"203.0.113.8 old.example",                        // Existing name: skip.
		"127.0.0.1 Fixture.One", "127.0.0.1 fixture.two", // Loopback reuse is allowed.
		"2001:db8::1 FAß.Example", "2001:db8::1 skipped.example",
		"bad-ip broken.example", "127.0.0.1", "# own comment",
	})
	if !valid || len(invalid) != 2 || !strings.HasPrefix(content, CustomHostsBase(original)) || strings.Contains(content, "stale.example") || strings.Contains(content, "new.example") || strings.Contains(content, "skipped.example") {
		t.Fatal("prefix, validation or deduplication differs", content, invalid)
	}
	for _, want := range []string{"127.0.0.1\tfixture.one\n", "127.0.0.1\tfixture.two\n", "2001:db8::1\tfass.example\n", "# own comment\n"} {
		if !strings.Contains(content, want) {
			t.Fatal("normalized row missing", want, content)
		}
	}
	if !strings.Contains(normalized, "bad-ip broken.example\n") || strings.Contains(content, "broken.example") {
		t.Fatal("invalid row was lost from config or written", normalized, content)
	}
}

func TestCustomHostsAllInvalidRowsLeaveExistingManagedTailUntouched(t *testing.T) {
	original := "127.0.0.1 localhost\n" + CustomHostsMarker + "\n192.0.2.1 keep.example\n"
	content, _, invalid, valid := BuildCustomHosts(original, []string{"not-an-ip fixture.invalid", "  "})
	if content != original || valid || len(invalid) != 2 {
		t.Fatal("original all-invalid behavior changed", content, valid, invalid)
	}
}
