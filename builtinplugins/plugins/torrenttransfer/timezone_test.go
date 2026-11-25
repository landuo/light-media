package torrenttransfer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func zoneBytes(t *testing.T, name string) []byte {
	t.Helper()
	for _, base := range []string{"/usr/share/zoneinfo", "/var/db/timezone/zoneinfo"} {
		if raw, e := os.ReadFile(filepath.Join(base, name)); e == nil {
			return raw
		}
	}
	t.Fatalf("owned timezone fixture needs installed zone %s", name)
	return nil
}
func TestInstalledTimezoneSourceOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/timezone-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Name      string `json:"case"`
			TZ        *string
			Files     map[string]string
			Localtime *string
			Public    struct {
				Zone, Raises string
				Offset       int `json:"offset_2026_01_01"`
			}
			Private     struct{ Zone, Raises string }
			Warnings    []string
			LogWarnings []string `json:"log_warnings"`
		}
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			root := t.TempDir()
			put := func(path string, raw []byte) {
				t.Helper()
				p := filepath.Join(root, path)
				if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(p, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			utc := zoneBytes(t, "Etc/UTC")
			put("usr/share/zoneinfo/Etc/UTC", utc)
			if e := os.Symlink("Etc/UTC", filepath.Join(root, "usr/share/zoneinfo/UTC")); e != nil {
				t.Fatal(e)
			}
			for path, value := range c.Files {
				data := []byte(value)
				if strings.HasPrefix(value, "HEX:") {
					data, e = hex.DecodeString(strings.TrimPrefix(value, "HEX:"))
					if e != nil {
						t.Fatal(e)
					}
				}
				put(path, data)
			}
			localRaw := []byte(nil)
			if c.Localtime != nil {
				if *c.Localtime == "BAD" {
					put("etc/localtime", []byte("not a valid TZif"))
				} else if *c.Localtime == "BINARY" {
					localRaw = zoneBytes(t, "Asia/Shanghai")
					put("etc/localtime", localRaw)
				} else {
					if e := os.MkdirAll(filepath.Join(root, "etc"), 0700); e != nil {
						t.Fatal(e)
					}
					if e := os.Symlink(filepath.Join(root, "usr/share/zoneinfo", *c.Localtime), filepath.Join(root, "etc/localtime")); e != nil {
						t.Fatal(e)
					}
				}
			}
			if c.TZ == nil {
				old, exists := os.LookupEnv("TZ")
				_ = os.Unsetenv("TZ")
				t.Cleanup(func() {
					if exists {
						_ = os.Setenv("TZ", old)
					} else {
						_ = os.Unsetenv("TZ")
					}
				})
			} else {
				tz := *c.TZ
				if tz == "<owned absolute file>" {
					localRaw = zoneBytes(t, "Asia/Shanghai")
					put("custom-zone", localRaw)
					tz = filepath.Join(root, "custom-zone")
				}
				t.Setenv("TZ", tz)
			}
			zone, warnings, e := resolveSourcePublicTimezone(root)
			if c.Public.Raises != "" {
				if e == nil {
					t.Fatal("source raises, Go accepted", zone)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				want := c.Public.Zone
				if want == "custom-zone" {
					want = "local"
				}
				// The actual fallback iterates zoneinfo.available_timezones(), a set.
				// UTC and Etc/UTC are the finite equivalent source outcomes here only.
				fallbackAlias := zone == "UTC" && want == "Etc/UTC" && len(c.Warnings) > 0 && strings.Contains(c.Warnings[0], "defaulting to UTC")
				if zone != want && !fallbackAlias {
					t.Fatalf("public timezone %q want %q", zone, want)
				}
				var loc *time.Location
				if zone == "local" {
					loc, e = time.LoadLocationFromTZData("local", localRaw)
				} else {
					loc, e = time.LoadLocation(zone)
				}
				if e != nil {
					t.Fatal(e)
				}
				_, offset := time.Date(2026, 1, 1, 0, 0, 0, 0, loc).Zone()
				if offset != c.Public.Offset {
					t.Fatal("source offset mismatch", offset, c.Public.Offset)
				}
			}
			expected := append(append([]string{}, c.Warnings...), c.LogWarnings...)
			if len(expected) == 0 {
				expected = nil
			}
			for i := range warnings {
				warnings[i] = strings.ReplaceAll(warnings[i], root, "<owned root>")
			}
			if !reflect.DeepEqual(warnings, expected) {
				t.Fatalf("warning observations want%v got%v", expected, warnings)
			}
			private, e := privateTimezone()
			if c.Private.Raises != "" {
				if e == nil {
					t.Fatal("private source raises, Go accepted", private)
				}
			} else if e != nil || private != c.Private.Zone {
				t.Fatal("private timezone mismatch", private, e)
			}
		})
	}
}
func TestInvalidPrivateZoneDoesNotConsumeOneShot(t *testing.T) {
	for _, tz := range []string{"", ":UTC", "No/Such_Zone"} {
		t.Run(tz, func(t *testing.T) {
			t.Setenv("TZ", tz)
			s := newSDKFixture(t, "qbittorrent", "transmission")
			p := New()
			cfg := s.config()
			cfg["onlyonce"] = true
			raw, _ := json.Marshal(cfg)
			if _, e := p.Handle(context.Background(), "init", raw, s.host); e == nil {
				t.Fatal("invalid scheduler zone accepted")
			}
			if !p.currentConfig().flag("onlyonce") || len(s.updates) != 0 || len(s.submissions) != 0 {
				t.Fatal("one-shot consumed before source constructor failure")
			}
		})
	}
}
func TestSourceLocalCronMarkerOmitsTimezoneString(t *testing.T) {
	// Test-only cache injection models the verified anonymous TZif discovery;
	// the production getter never accepts a zone name/path from a plugin request.
	p := New()
	cfg := NewConfig()
	cfg.values = map[string]any{"enabled": true, "cron": "0 0 * * *", "fromdownloader": "Source", "todownloader": "Target", "fromtorrentpath": "/backup"}
	p.setConfig(cfg)
	p.publicZone = "local"
	p.publicZoneCached = true
	raw, e := p.Handle(context.Background(), "get_service", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	var jobs []map[string]any
	_ = json.Unmarshal(raw, &jobs)
	trigger := jobs[0]["trigger"].(map[string]any)
	if trigger["local_timezone"] != true {
		t.Fatal("missing explicit local marker", trigger)
	}
	if _, exists := trigger["timezone"]; exists {
		t.Fatal("mutually exclusive timezone string leaked", trigger)
	}
}
