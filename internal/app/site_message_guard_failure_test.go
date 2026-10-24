package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"testing"
)

func TestSiteMessageDefiniteGuardFailureWhitelist(t *testing.T) {
	revoked := bad("PLUGIN_API_AUTH_REVOKED", "owned synchronous guard denial")
	marked := siteMessageDefiniteGuardError(revoked)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"observed_guard", marked, "PLUGIN_API_AUTH_REVOKED"},
		{"wrapped_observed_guard", fmt.Errorf("observed hook: %w", marked), "PLUGIN_API_AUTH_REVOKED"},
		{"unmarked_same_code", revoked, ""},
		{"wrapped_unmarked_same_code", fmt.Errorf("transport: %w", revoked), ""},
		{"unmarked_site_change", bad("SITE_CHANGED", "owned site edit"), ""},
		{"observed_site_change", siteMessageDefiniteGuardError(bad("SITE_CHANGED", "owned site edit")), "SITE_CHANGED"},
		{"guard_other_api_error", siteMessageDefiniteGuardError(bad("OTHER_FAILURE", "owned")), ""},
		{"guard_unknown", siteMessageDefiniteGuardError(seedhost.ErrUnknown), ""},
		{"guard_cancel", siteMessageDefiniteGuardError(context.Canceled), ""},
		{"guard_sql", siteMessageDefiniteGuardError(sql.ErrTxDone), ""},
		{"matching_error_text", siteMessageDefiniteGuardError(errors.New("PLUGIN_API_AUTH_REVOKED")), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := siteMessageAdmissionFailureCode(tc.err); got != tc.want {
				t.Fatal("unknown/non-observed error misclassified as definite admission failure", got, tc.want)
			}
		})
	}
	if siteMessageDefiniteGuardError(nil) != nil {
		t.Fatal("nil guard failure gained an error")
	}
	for _, original := range []error{revoked, seedhost.ErrUnknown, context.Canceled, sql.ErrTxDone} {
		if !errors.Is(siteMessageDefiniteGuardError(original), original) {
			t.Fatal("guard marker lost original error identity", original)
		}
	}
}
