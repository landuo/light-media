package schedule

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A separate process lets the standard library initialize the real time.Local
// from an owned TZif file. No test replaces time.Local or changes a system file.
func TestTriggerSystemLocalFile(t *testing.T) {
	if os.Getenv("PLUGIN_SCHEDULE_LOCAL_CHILD") != "1" {
		raw, err := os.ReadFile("../../../testdata/plugin-local-timezone-20261105/New_York.tzif")
		if err != nil {
			t.Fatal(err)
		}
		zone := filepath.Join(t.TempDir(), "owned-zone-without-iana-name")
		if err := os.WriteFile(zone, raw, 0600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(os.Args[0], "-test.run=^TestTriggerSystemLocalFile$", "-test.v")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "TZ=") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "TZ="+zone, "PLUGIN_SCHEDULE_LOCAL_CHILD=1")
		output, err := command.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal("independent local-zone process", err)
		}
		return
	}
	_, winter := instant(t, "2026-01-01T00:00:00Z").In(time.Local).Zone()
	_, summer := instant(t, "2026-07-01T00:00:00Z").In(time.Local).Zone()
	if winter != -18000 || summer != -14400 {
		t.Fatal("real time.Local did not read owned TZif", winter, summer)
	}
	oracle := localSourceOracle(t)
	for _, sample := range oracle.Cases {
		trigger, err := New(Spec{Kind: "cron", LocalTimezone: true, Cron: sample.Cron}, instant(t, sample.Start))
		if err != nil {
			t.Fatal(err)
		}
		if trigger.cron.location != time.Local {
			t.Fatal("flag did not resolve actual time.Local")
		}
		checkLocalSourceTimes(t, trigger, sample)
		raw, err := json.Marshal(trigger.Spec())
		if err != nil || !bytes.Contains(raw, []byte(`"local_timezone":true`)) || bytes.Contains(raw, []byte(`"timezone"`)) {
			t.Fatal("local declaration wire changed", string(raw), err)
		}
		var restored Spec
		if json.Unmarshal(raw, &restored) != nil {
			t.Fatal("restore local spec")
		}
		cold, err := New(restored, instant(t, sample.Start).Add(24*time.Hour))
		if err != nil || !cold.First(instant(t, sample.Start)).Equal(trigger.First(instant(t, sample.Start))) {
			t.Fatal("cold local calendar changed", err)
		}
	}

}

func TestTriggerLocalContractAndIANACompatibility(t *testing.T) {
	at := instant(t, "2026-10-16T00:00:00Z")
	for _, spec := range []Spec{
		{Kind: "cron", LocalTimezone: true, Timezone: "UTC", Cron: "* * * * *"},
		{Kind: "cron", LocalTimezone: true, Timezone: "Local", Cron: "* * * * *"},
		{Kind: "cron", Timezone: "Local", Cron: "* * * * *"},
		{Kind: "cron", Cron: "* * * * *"},
	} {
		if _, err := New(spec, at); err == nil {
			t.Fatal("conflicting/implicit local accepted", spec)
		}
	}
	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
		spec := Spec{Kind: "cron", Timezone: zone, Cron: "30 1 * * *"}
		trigger, err := New(spec, at)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := ParseCrontab(spec.Cron, zone)
		if err != nil || !legacy.First(at).Equal(trigger.First(at)) {
			t.Fatal("IANA calendar changed", zone, err)
		}
		raw, _ := json.Marshal(trigger.Spec())
		if bytes.Contains(raw, []byte(`"local_timezone"`)) {
			t.Fatal("old declaration added local flag", string(raw))
		}
	}
	if _, err := ParseCrontab("* * * * *", "Local"); err == nil {
		t.Fatal("legacy parser accepted Local string")
	}
	if _, err := ParseCronFields(CronFields{Second: "*"}, "Local"); err == nil {
		t.Fatal("legacy fields parser accepted Local string")
	}
}

type localOracleCase struct {
	Branch, Cron, Start string
	Fires               []struct{ UTC string }
}
type localOracle struct {
	Fixture struct{ SHA256 string }
	Cases   []localOracleCase
}

func localSourceOracle(t *testing.T) localOracle {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/plugin-local-timezone-20261105/cron-local-source-oracle.json")
	var oracle localOracle
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("independent actual source oracle", err)
	}
	zone, err := os.ReadFile("../../../testdata/plugin-local-timezone-20261105/New_York.tzif")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(zone)) != oracle.Fixture.SHA256 {
		t.Fatal("source/target TZif differs", err)
	}
	return oracle
}
func checkLocalSourceTimes(t *testing.T, trigger *Trigger, sample localOracleCase) {
	t.Helper()
	at := instant(t, sample.Start)
	for _, fire := range sample.Fires {
		got := trigger.First(at)
		if !got.Equal(instant(t, fire.UTC)) {
			t.Fatalf("%s %s got %s source %s", sample.Branch, sample.Cron, got, fire.UTC)
		}
		at = got.Add(time.Microsecond)
	}
}

// The source's fixed canonical-file loop is isolated by its file-reader boundary;
// it cannot be directed to a worker-provided path and never mutates time.Local.
func TestAnonymousLocalCanonicalSourceFallback(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugin-local-timezone-20261105/New_York.tzif")
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		name                string
		first, second       []byte
		firstErr, secondErr error
		wantErr             bool
		wantPaths           int
	}{
		{name: "first", first: raw, wantPaths: 1},
		{name: "second", firstErr: os.ErrNotExist, second: raw, wantPaths: 2},
		{name: "both missing UTC", firstErr: os.ErrNotExist, secondErr: os.ErrNotExist, wantPaths: 2},
		{name: "first malformed no fallback", first: []byte("bad TZif"), wantErr: true, wantPaths: 1},
		{name: "first permission no fallback", firstErr: os.ErrPermission, wantErr: true, wantPaths: 1},
		{name: "second malformed", firstErr: os.ErrNotExist, second: []byte("bad TZif"), wantErr: true, wantPaths: 2},
		{name: "second permission", firstErr: os.ErrNotExist, secondErr: os.ErrPermission, wantErr: true, wantPaths: 2},
	} {
		t.Run(sample.name, func(t *testing.T) {
			before := time.Local
			paths := []string{}
			location, err := anonymousLocalLocation(func(path string) (os.FileInfo, error) {
				paths = append(paths, path)
				if path == "/etc/localtime" && sample.firstErr == os.ErrNotExist || path == "/usr/local/etc/localtime" && sample.secondErr == os.ErrNotExist {
					return nil, os.ErrNotExist
				}
				return nil, nil
			}, func(path string) ([]byte, error) {
				if path == "/etc/localtime" {
					return sample.first, sample.firstErr
				}
				return sample.second, sample.secondErr
			})
			if (err != nil) != sample.wantErr || len(paths) != sample.wantPaths || paths[0] != "/etc/localtime" || len(paths) == 2 && paths[1] != "/usr/local/etc/localtime" || time.Local != before {
				t.Fatal("canonical source fallback changed", paths, err)
			}
			if sample.wantErr {
				return
			}
			if sample.firstErr == os.ErrNotExist && sample.secondErr == os.ErrNotExist {
				if location != time.UTC {
					t.Fatal("missing config did not use UTC")
				}
				return
			}
			for _, source := range localSourceOracle(t).Cases {
				trigger, err := parseCrontabLocation(source.Cron, location)
				if err != nil {
					t.Fatal(err)
				}
				checkLocalSourceTimes(t, &Trigger{spec: Spec{Kind: "cron", LocalTimezone: true}, cron: trigger}, source)
			}
		})
	}
}

func TestLocalEnvironmentDoesNotSilentlyBecomeUTC(t *testing.T) {
	for _, sample := range []struct{ name, tz, contents string }{
		{"malformed file", "file", "bad TZif"}, {"missing file", "missing", ""}, {"missing IANA", "Owned/Not-A-Timezone", ""}, {"colon only", ":", ""},
	} {
		t.Run(sample.name, func(t *testing.T) {
			value := sample.tz
			if value == "file" || value == "missing" {
				value = filepath.Join(t.TempDir(), value)
				if sample.contents != "" {
					if err := os.WriteFile(value, []byte(sample.contents), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			t.Setenv("TZ", value)
			if _, err := New(Spec{Kind: "cron", LocalTimezone: true, Cron: "* * * * *"}, instant(t, "2026-01-01T00:00:00Z")); err == nil {
				t.Fatal("invalid host TZ silently accepted as UTC")
			}
		})
	}
}

// Exercise the real standard-library empty-TZ difference in a fresh process.
// Existing system metadata is read-only; the canonical fallback branch itself
// is additionally tested with owned bytes above, including failures.
func TestTriggerEmptyTZRealSystemMetadata(t *testing.T) {
	if os.Getenv("PLUGIN_SCHEDULE_EMPTY_TZ_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestTriggerEmptyTZRealSystemMetadata$", "-test.v")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "TZ=") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "TZ=", "PLUGIN_SCHEDULE_EMPTY_TZ_CHILD=1")
		output, err := command.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal("independent empty-TZ process", err)
		}
		return
	}
	before := time.Local
	at := instant(t, "2026-01-01T12:00:00Z")
	_, standardOffset := at.In(time.Local).Zone()
	if standardOffset != 0 {
		t.Fatal("Go empty TZ unexpectedly not UTC", standardOffset)
	}
	expected, err := anonymousLocalLocation(os.Stat, os.ReadFile)
	if err != nil {
		t.Fatal("system canonical metadata unavailable", err)
	}
	trigger, err := New(Spec{Kind: "cron", LocalTimezone: true, Cron: "30 1 * * *"}, at)
	if err != nil {
		t.Fatal(err)
	}
	expectedCron, err := parseCrontabLocation("30 1 * * *", expected)
	if err != nil || !trigger.First(at).Equal(expectedCron.First(at)) || time.Local != before {
		t.Fatal("empty TZ did not use canonical calendar without mutating global Local", err)
	}
	_, offset := at.In(expected).Zone()
	t.Logf("empty TZ Go Local offset=%d, flag canonical offset=%d", standardOffset, offset)
}

func TestAnonymousLocalExistsReadMatchesInstalledSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugin-local-timezone-20261105/localtime-errno-source-oracle.json")
	type operation struct {
		Op, Path string
		Mode     string `json:"mode,omitempty"`
	}
	var oracle struct {
		FixtureSHA string `json:"fixture_sha256"`
		Cases      []struct {
			Case   string
			Result struct {
				Zone, Raises string
				Offset       int `json:"offset_2026_01_01"`
			}
			Trace []operation
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("installed genericpath/tzlocal errno oracle", err)
	}
	zone, err := os.ReadFile("../../../testdata/plugin-local-timezone-20261105/New_York.tzif")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(zone)) != oracle.FixtureSHA {
		t.Fatal("errno source TZif binding", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			statErr := error(nil)
			readErr := error(nil)
			secondStatErr := error(nil)
			data := zone
			switch sample.Case {
			case "first-valid":
			case "first-stat-EACCES-second-valid":
				statErr = syscall.EACCES
			case "first-stat-EIO-second-valid":
				statErr = syscall.EIO
			case "first-stat-ENOENT-second-valid":
				statErr = syscall.ENOENT
			case "first-read-EACCES-no-fallback":
				readErr = syscall.EACCES
			case "first-read-EIO-no-fallback":
				readErr = syscall.EIO
			case "first-malformed-no-fallback":
				data = []byte("owned malformed TZif")
			case "first-stat-EACCES-second-stat-EACCES-UTC":
				statErr = syscall.EACCES
				secondStatErr = syscall.EACCES
			case "both-absent-UTC":
				statErr = syscall.ENOENT
				secondStatErr = syscall.ENOENT
			default:
				t.Fatal("unbound source input", sample.Case)
			}
			trace := []operation{}
			before := time.Local
			location, err := anonymousLocalLocation(func(path string) (os.FileInfo, error) {
				trace = append(trace, operation{Op: "stat", Path: path})
				if path == "/etc/localtime" {
					return nil, statErr
				}
				return nil, secondStatErr
			}, func(path string) ([]byte, error) {
				trace = append(trace, operation{Op: "open", Path: path, Mode: "rb"})
				return data, readErr
			})
			expected := append([]operation{}, sample.Trace...)
			for i := range expected {
				expected[i].Path = strings.TrimPrefix(expected[i].Path, "/owned")
			}
			if !reflect.DeepEqual(trace, expected) || (err != nil) != (sample.Result.Raises != "") || time.Local != before {
				t.Fatal("canonical exists/open source trace differs", trace, expected, err)
			}
			if err != nil {
				return
			}
			_, offset := instant(t, "2026-01-01T00:00:00Z").In(location).Zone()
			if offset != sample.Result.Offset || location.String() != sample.Result.Zone {
				t.Fatal("source fallback calendar differs", location, offset, sample.Result)
			}
		})
	}
}
