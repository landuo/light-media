package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/app"
)

func ownedInput(t *testing.T) []byte {
	t.Helper()
	body := map[string]any{
		"version": 1, "mode": "selected_installed_cold_site_message_not_live_cache", "known": true, "site_message": false,
		"normalization_fields": []string{},
		"source_hashes": map[string]string{
			"/app/app/core/config.py":  "f19111073a7ef25ecd0b7e9d53dab343d76fe64ff3ad821f4ff39a790c104033",
			"/app/app/utils/system.py": "3477c8585dd955bcb42f7180e5fabaae05c528af8ddb0cbae0935dbdb0c031db",
		},
		"env_path": "/config/app.env", "env_sha256": strings.Repeat("2", 64),
		"captures_equal": true, "original_config_unchanged": true, "original_app_imports": 0, "original_settings_constructors": 0,
		"audit": map[string]int{"denied_writes": 0, "denied_network": 0, "denied_processes": 0},
	}
	b, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

type failedInput struct{}

func (failedInput) Read([]byte) (int, error) { return 0, errors.New("owned-reader-error") }

func TestCLIValidationAndErrorsNeverEchoPrivateInput(t *testing.T) {
	raw := ownedInput(t)
	for _, item := range []struct {
		name        string
		args        []string
		body        []byte
		state, code string
		exit        int
	}{
		{name: "validate-no-target", args: []string{"--validate-input"}, body: raw, state: "validated"},
		{name: "target-required", body: raw, state: "failed", code: "explicit_existing_source_settings_target_required", exit: 1},
		{name: "invalid-json", args: []string{"--validate-input"}, body: []byte(`{"private":"owned-must-not-echo"}`), state: "failed", code: "invalid_reviewed_site_message_settings", exit: 1},
		{name: "conflicting-mode", args: []string{"--validate-input", "--apply"}, body: raw, state: "failed", code: "invalid_site_message_settings_input", exit: 1},
		{name: "target-in-validation", args: []string{"--validate-input", "--target-data-dir", "/owned-must-not-echo"}, body: raw, state: "failed", code: "invalid_site_message_settings_input", exit: 1},
		{name: "unknown-flag", args: []string{"--owned-must-not-echo"}, body: raw, state: "failed", code: "invalid_site_message_settings_input", exit: 1},
		{name: "positional", args: []string{"owned-must-not-echo"}, body: raw, state: "failed", code: "invalid_site_message_settings_input", exit: 1},
		{name: "oversize", args: []string{"--validate-input"}, body: bytes.Repeat([]byte("x"), 65537), state: "failed", code: "invalid_site_message_settings_input", exit: 1},
	} {
		t.Run(item.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := run(context.Background(), item.args, bytes.NewReader(item.body), &out); got != item.exit {
				t.Fatal(got, out.String())
			}
			var result map[string]any
			if json.Unmarshal(out.Bytes(), &result) != nil || result["state"] != item.state {
				t.Fatal("invalid summary", out.String())
			}
			if item.code != "" && result["code"] != item.code {
				t.Fatal("wrong failure code", result["code"])
			}
			if bytes.Contains(out.Bytes(), []byte("owned-must-not-echo")) || bytes.Contains(out.Bytes(), []byte("site_message\"")) || bytes.Contains(out.Bytes(), []byte(strings.Repeat("2", 64))) {
				t.Fatal("private input echoed")
			}
			if item.exit == 0 && (result["writes"] != float64(0) || result["jobs_queued"] != float64(0) || result["external_calls"] != float64(0)) {
				t.Fatal("validation performed work")
			}
		})
	}
	var out bytes.Buffer
	if run(context.Background(), []string{"--validate-input"}, failedInput{}, &out) != 1 || strings.Contains(out.String(), "owned-reader-error") {
		t.Fatal("stdin failure leaked")
	}
}

func TestCLITargetBoundaryDoesNotCreateOrStartAnInstance(t *testing.T) {
	raw := ownedInput(t)
	missing := filepath.Join(t.TempDir(), "absent")
	var out bytes.Buffer
	if run(context.Background(), []string{"--target-data-dir", missing}, bytes.NewReader(raw), &out) != 1 {
		t.Fatal("missing target admitted")
	}
	if _, e := os.Lstat(missing); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("missing target created", e)
	}
	dir := t.TempDir()
	a, e := app.New(app.Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	out.Reset()
	if run(context.Background(), []string{"--target-data-dir", dir, "--apply"}, bytes.NewReader(raw), &out) != 1 || !strings.Contains(out.String(), "source_settings_target_is_running") {
		t.Fatal("live target admitted", out.String())
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(dir, "light-media.db"))
	if e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--target-data-dir", dir}, {"--target-data-dir", dir, "--apply"}} {
		out.Reset()
		if run(context.Background(), args, bytes.NewReader(raw), &out) != 1 || !strings.Contains(out.String(), "source_site_message_parent_settings_required") {
			t.Fatal("unlinked flag admitted", out.String())
		}
	}
	after, e := os.ReadFile(filepath.Join(dir, "light-media.db"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("failed CLI changed target", e)
	}
}
