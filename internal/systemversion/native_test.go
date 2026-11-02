package systemversion

import (
	"strings"
	"testing"
)

// These expectations implement the user's native-version adaptation, not a
// newly claimed original Python oracle. The original v2 tests remain intact.
func TestNativeLightMediaReleasePolicy(t *testing.T) {
	for _, sample := range []struct{ raw, want string }{
		{`[]`, ""}, {`[{"tag_name":"v2.0.0-beta"},{"tag_name":"nightly"}]`, ""},
		{`[{"tag_name":"v0.4.9"},{"tag_name":"0.4.19"}]`, "0.4.19"},
		{`[{"tag_name":"0.4.19"},{"tag_name":"v0.4.19"}]`, "v0.4.19"},
		{`[{"tag_name":"v0.04.019"},{"tag_name":"0.4.19"}]`, "0.4.19"},
		{`[{"tag_name":"1.0"},{"tag_name":"1.0.0"}]`, "1.0.0"},
		{`[{"tag_name":"999999999999999999999999.1"},{"tag_name":"2.9"}]`, "999999999999999999999999.1"},
	} {
		value, err := SelectNativeRelease([]byte(sample.raw))
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if value != nil {
			got = *value
		}
		if got != sample.want {
			t.Fatalf("selection %s got %s", sample.raw, got)
		}
	}
	for _, raw := range []string{`null`, `{}`, `[{"tag_name":null}]`, `[{}]`, `[1]`, `[{"tag_name":1}]`, `[] []`, `{`} {
		if value, err := SelectNativeRelease([]byte(raw)); err == nil || value != nil {
			t.Fatal("invalid native metadata accepted", raw)
		}
	}
	for _, tag := range []string{"", "v", "V0.4.19", " 0.4.19", "0.4.19 ", "0..1", "v1.0-rc1", "１２.0", "+1.0"} {
		if _, ok := NativeVersionKey(tag); ok {
			t.Fatal("illegal native tag", tag)
		}
	}
	remote := "v0.4.19"
	if got := NativeMessage(&remote, "0.4.19", "0.4.18"); got != "当前光影后端版本：0.4.19，已是最新版本\n当前光影前端版本：0.4.18，远程版本：v0.4.19" {
		t.Fatal(got)
	}
	if got := NativeMessage(nil, "0.4.19", "0.4.19"); strings.Contains(got, "最新") || strings.Count(got, "未知") != 2 {
		t.Fatal(got)
	}
	// The source selector is deliberately not rebound to native grammar.
	original, err := SelectRelease([]byte(`[{"tag_name":"v0.4.19"}]`))
	if err != nil || original.Version != nil {
		t.Fatal("original v2 policy changed")
	}
}
