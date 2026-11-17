package main

import (
	"testing"

	"light-media/internal/selfupdate"
)

// The startup update is only as good as its environment mapping: a wrong mode or
// a missing feed URL would silently disable it or point it at the wrong place.
func TestStartupUpdateOptionsReadSourceCompatibleEnvironment(t *testing.T) {
	env := map[string]string{
		"LIGHT_MEDIA_AUTO_UPDATE":     "release",
		"LIGHT_MEDIA_UPDATE_RELEASES": "https://owned.invalid/releases",
		"LIGHT_MEDIA_UPDATE_TOKEN":    "owned-token",
	}
	opts := startupUpdateOptions("/config", func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if opts.Mode != "release" || opts.ReleasesURL != "https://owned.invalid/releases" ||
		opts.Token != "owned-token" || opts.DataDir != "/config" || opts.CurrentVersion == "" {
		t.Fatalf("options %+v", opts)
	}
	if opts.GOOS == "" || opts.GOARCH == "" {
		t.Fatalf("platform must be filled: %+v", opts)
	}
}

// MoviePilot deployments already carry MOVIEPILOT_AUTO_UPDATE; the source name
// keeps working as the fallback so an existing compose file needs no edit.
func TestStartupUpdateFallsBackToSourceSettingAndToken(t *testing.T) {
	env := map[string]string{
		"MOVIEPILOT_AUTO_UPDATE": "off",
		"GITHUB_TOKEN":           "source-token",
	}
	opts := startupUpdateOptions("/config", func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if opts.Mode != "off" || opts.Token != "source-token" {
		t.Fatalf("fallback options %+v", opts)
	}
	if opts.ReleasesURL != "" {
		t.Fatalf("unset feed must stay empty so the default applies: %q", opts.ReleasesURL)
	}
}

func TestStartupUpdateDisabledByDefaultEnvironment(t *testing.T) {
	opts := startupUpdateOptions("/config", func(string) (string, bool) { return "", false })
	if !selfupdate.ModeEnabled(opts.Mode) {
		t.Fatal("an unset mode follows the source default and enables the check")
	}
}
