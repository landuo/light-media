package app

import (
	"context"
	"testing"
)

// This legacy sanitization helper is retained, but installation reports no
// longer use it or construct a payload. Acquisition continues using its input.
func TestOriginalPluginReportedRepositoryLegacySanitization(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want any
	}{
		{"plain repository unchanged", "https://owned.invalid/index", "https://owned.invalid/index"},
		{"ordinary query omitted", "https://owned.invalid/index?ref=owned", "https://owned.invalid/index"},
		{"all query fields omitted", "https://owned.invalid/index?ref=owned&token=synthetic-only&ref=second", "https://owned.invalid/index"},
		{"userinfo and fragment omitted", "https://synthetic-user:synthetic-password@owned.invalid/index?ref=owned#section", "https://owned.invalid/index"},
		{"escaped path preserved", "https://owned.invalid/owned%2Findex?ref=owned", "https://owned.invalid/owned%2Findex"},
		{"empty repository preserved", "", ""},
		{"invalid URL not reported", "https://owned.invalid/%zz?ref=owned", nil},
		{"local identity and version retained", "local://Owned?path=%2Fsynthetic%2Fprivate&version=v2&ref=owned", "local://Owned?version=v2"},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			if got := originalPluginReportedRepository(row.raw); got != row.want {
				t.Fatalf("reported repository = %#v, want %#v", got, row.want)
			}
		})
	}
}

func TestOriginalPluginInstallationReportsHaveNoSideEffects(t *testing.T) {
	// An uninitialized receiver makes accidental access to saved settings,
	// identity, installation inventory, database receipts, or HTTP fail locally.
	// No settings value or sharing flag can enable automatic uploads again.
	var a *App
	a.reportOriginalPluginInstallation(context.Background(), "Owned", "https://owned.invalid/index?ref=owned")
	a.reportOriginalPluginInstallations(context.Background())
}
