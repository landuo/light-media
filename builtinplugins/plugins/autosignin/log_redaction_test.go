package autosignin

import (
	"strings"
	"testing"
)

// These assertions test the explicit credential-handling adaptation. They are
// not independent Python business expectations.
func TestRuntimeDiagnosticSecretBoundary(t *testing.T) {
	site := Object{"cookie": "owned-cookie-secret", "apikey": "owned-api-secret", "token": "owned-token-secret"}
	cases := []string{
		"signed_timestamp=123 signed_token=owned-generated-secret",
		"获取到x-csrf owned-generated-secret",
		`result {"access_token":"owned-generated-secret","status":"failed"}`,
		`<meta name="x-csrf-token" content="owned-generated-secret">request failed`,
		`<input name="passkey" value="owned-generated-secret">`,
		"owned-cookie-secret owned-api-secret owned-token-secret",
	}
	for _, message := range cases {
		got := redactSiteDiagnostic(message, site)
		if strings.Contains(got, "owned-") {
			t.Fatalf("secret retained: %q", got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Fatal(got)
		}
	}
	ordinary := "Owned 今日已签到，状态码：200"
	if got := redactSiteDiagnostic(ordinary, site); got != ordinary {
		t.Fatal(got)
	}
}
