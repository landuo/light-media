package sites

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSourceUserModuleDiagnosticHasRealSafeNativeFrames(t *testing.T) {
	for _, item := range []struct{ kind, body, want string }{
		{"mteam", "{", "mt-invalid-json"},
		{"mteam", `{"code":"0","data":null}`, "mt-null-data"},
		{"mteam", `{"code":"0","data":{"memberCount":{"uploaded":"owned-private-invalid-number"}}}`, "mt-invalid-integer"},
		{"hhanclub", `<a href="logout.php">out</a>`, "hh-missing-panel"},
	} {
		t.Run(item.want, func(t *testing.T) {
			c := sourceFailureClient(t, item.kind, item.body)
			_, dto, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
			kind, stack, ok := SourceUserModuleFailureDiagnostic(err)
			if !ok || dto != nil || kind != item.want || !strings.HasPrefix(stack, "Go native parser diagnostic (not Python traceback):\n") || !strings.Contains(stack, "light-media/internal/sites.") || !strings.Contains(stack, "SourceUserStats") {
				t.Fatal("missing actual Go parser stack", kind, stack, err)
			}
			for _, forbidden := range []string{"owned-private-invalid-number", "synthetic-cookie", "synthetic-key", "/workspace/", "/home/", "Traceback (most recent call last)"} {
				if strings.Contains(stack, forbidden) {
					t.Fatal("diagnostic leaked or fabricated content", forbidden)
				}
			}
			// General native errors stay stable: display projection does not silently
			// change existing API error codes, parser invariants or DTO behavior.
			if err.Error() != "site_user_source_module_failed" {
				t.Fatal("native error contract changed", err)
			}
			for _, untrusted := range []error{nil, errors.New(err.Error()), fmt.Errorf("older: %w", err)} {
				if _, _, ok := SourceUserModuleFailureDiagnostic(untrusted); ok {
					t.Fatal("untrusted/wrapped diagnostic accepted")
				}
			}
			c.SetRequestGuard(func(context.Context) error { return err })
			_, _, revoked := c.SourceUserStats(context.Background(), UserStatsOptions{})
			if _, _, ok := SourceUserModuleFailureDiagnostic(revoked); ok {
				t.Fatal("guard replayed previous native diagnostic")
			}
		})
	}
}

func TestSourceUserUnsupportedDispatchHasNoModuleDiagnostic(t *testing.T) {
	c := sourceFailureClient(t, "unsupported", `{}`)
	_, _, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
	if _, _, ok := SourceUserModuleFailureDiagnostic(err); ok {
		t.Fatal("unsupported dispatch became module exception")
	}
}
