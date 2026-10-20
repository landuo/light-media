package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ownedSourceSettingsInput(t *testing.T) []byte {
	t.Helper()
	share, zero := true, 0
	uid := strings.Repeat("1", 64)
	env := []string{"HTTPS_PROXY=http://127.0.0.1:9999", "NO_PROXY=localhost"}
	value := sourceSettingsInput{
		Version:    1,
		Source:     sourceSettingsEvidence{Mode: "selected_installed_cold_settings", UIDMode: "derived_rootfs_not_live_helper_cache", TimezoneMode: "derived_installed_tzlocal_not_live_cache", TzlocalVersion: "5.4.4", EnvPath: "/config/app.env", EnvFileSHA256: strings.Repeat("2", 64), SourceHashes: reviewedSourceSettingsHashes, SelectedFields: reviewedSourceSettingsFields, NormalizationFields: []string{}, NetworkCalls: &zero, OriginalWrites: &zero},
		Statistics: sourceImportedStatistics{Version: 1, Environment: &env},
		Network:    sourceImportedNetwork{Version: 1, Known: true, GitHubHeaders: map[string]string{"Authorization": "Bearer owned-source-token", "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"}, Proxy: map[string]string{"http": "http://127.0.0.1:9999", "https": "http://127.0.0.1:9999"}, Environment: env},
		Runtime:    sourceImportedRuntime{Version: 1, Known: true, Timezone: "Asia/Shanghai", LocalTimezone: "Etc/UTC"},
	}
	value.Statistics.Snapshot.Share = &share
	value.Statistics.Snapshot.Known = true
	value.Statistics.Snapshot.ServerHost = "https://owned-statistics.invalid"
	value.Statistics.Snapshot.UserAgent = "MoviePilot/2.15.6 (Linux owned; x86_64)"
	value.Statistics.Snapshot.UserUID = &uid
	value.Statistics.Snapshot.Proxies = value.Network.Proxy
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func ownedSourceSettingsTarget(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewPluginStore(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('owned_existing','keep',?,'before')`, []byte(`{"preserve":"owned"}`)); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	return dir
}

type ownedSourceSettingsFile struct {
	SHA     [32]byte
	Size    int64
	ModTime int64
	Mode    os.FileMode
}

func ownedSourceSettingsFiles(t *testing.T, dir string) map[string]ownedSourceSettingsFile {
	t.Helper()
	out := map[string]ownedSourceSettingsFile{}
	e := filepath.Walk(dir, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out[filepath.Base(path)] = ownedSourceSettingsFile{sha256.Sum256(raw), info.Size(), info.ModTime().UnixNano(), info.Mode()}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func TestReviewedSourceSettingsDryApplyReplayCold(t *testing.T) {
	dir, input := ownedSourceSettingsTarget(t), ownedSourceSettingsInput(t)
	before := ownedSourceSettingsFiles(t, dir)
	r, e := ImportReviewedSourceSettings(context.Background(), dir, input, false)
	if e != nil || r.State != "ready" || r.Writes != 0 || r.Records != 4 || r.ExternalCalls != 0 || r.JobsQueued != 0 {
		t.Fatalf("dry result=%+v error=%v", r, e)
	}
	if !reflect.DeepEqual(before, ownedSourceSettingsFiles(t, dir)) {
		t.Fatalf("read-only dry run changed a file, byte, mtime, size or permission: before=%v after=%v", before, ownedSourceSettingsFiles(t, dir))
	}
	r, e = ImportReviewedSourceSettings(context.Background(), dir, input, true)
	if e != nil || r.State != "imported" || r.Writes != 4 || r.ExternalCalls != 0 || r.JobsQueued != 0 {
		t.Fatalf("apply result=%+v error=%v", r, e)
	}
	after := ownedSourceSettingsFiles(t, dir)
	if after["secret.key"] != before["secret.key"] || after["instance.lock"] != before["instance.lock"] {
		t.Fatal("target key or lock changed")
	}
	for _, apply := range []bool{false, true} {
		r, e = ImportReviewedSourceSettings(context.Background(), dir, input, apply)
		if e != nil || r.State != "already_imported" || r.Writes != 0 {
			t.Fatalf("cold replay result=%+v error=%v", r, e)
		}
		if !reflect.DeepEqual(after, ownedSourceSettingsFiles(t, dir)) {
			t.Fatal("cold repeated delivery changed persistent files")
		}
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := NewPluginStore(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	in, _, e := validateSourceSettings(input)
	if e != nil {
		t.Fatal(e)
	}
	for kind, want := range map[string]any{"source_statistics_settings": in.Statistics, "source_network_settings": in.Network, "source_runtime_settings": in.Runtime} {
		var sealed []byte
		if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind=? AND id='main'`, kind).Scan(&sealed); e != nil {
			t.Fatal(e)
		}
		for _, plain := range [][]byte{[]byte("owned-source-token"), []byte("owned-statistics.invalid"), []byte(strings.Repeat("1", 64)), []byte("Authorization")} {
			if bytes.Contains(sealed, plain) {
				t.Fatal("plaintext in encrypted record", kind)
			}
		}
		raw, e := p.unseal(kind, "system", "main", sealed)
		expected, _ := json.Marshal(want)
		if e != nil || !bytes.Equal(raw, expected) {
			t.Fatal("cold unseal differs", kind, e)
		}
		if _, e = p.unseal("different-bound-kind", "system", "main", sealed); e == nil {
			t.Fatal("AEAD permitted a transplanted settings record")
		}
	}
	for _, query := range []string{`SELECT COUNT(*) FROM jobs`, `SELECT COUNT(*) FROM plugin_runs`, `SELECT COUNT(*) FROM plugin_schedules`, `SELECT COUNT(*) FROM events`} {
		var count int
		if e = s.DB.QueryRow(query).Scan(&count); e != nil || count != 0 {
			t.Fatal("import started work", count, e)
		}
	}
	var existing []byte
	if s.DB.QueryRow(`SELECT payload FROM records WHERE kind='owned_existing' AND id='keep'`).Scan(&existing) != nil || string(existing) != `{"preserve":"owned"}` {
		t.Fatal("unrelated record changed")
	}
}

func TestReviewedSourceSettingsAtomicRollbackAndScope(t *testing.T) {
	t.Run("uncheckpointed-WAL", func(t *testing.T) {
		dir := ownedSourceSettingsTarget(t)
		if e := os.WriteFile(filepath.Join(dir, "light-media.db-wal"), []byte("owned-uncheckpointed-WAL"), 0600); e != nil {
			t.Fatal(e)
		}
		before := ownedSourceSettingsFiles(t, dir)
		if _, e := ImportReviewedSourceSettings(context.Background(), dir, ownedSourceSettingsInput(t), false); e == nil || e.Error() != "source_settings_target_requires_clean_checkpoint" {
			t.Fatal("immutable read ignored committed WAL", e)
		}
		if !reflect.DeepEqual(before, ownedSourceSettingsFiles(t, dir)) {
			t.Fatal("WAL refusal changed target")
		}
	})
	t.Run("target-live-lock", func(t *testing.T) {
		dir := ownedSourceSettingsTarget(t)
		s, e := OpenStore(dir)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		if _, e = ImportReviewedSourceSettings(context.Background(), dir, ownedSourceSettingsInput(t), true); e == nil || e.Error() != "source_settings_target_is_running" {
			t.Fatal("running target accepted import", e)
		}
	})
	for _, scenario := range []string{"SQL-failure", "existing-conflict", "partial-record", "active-work", "corrupt-record"} {
		t.Run(scenario, func(t *testing.T) {
			dir, input := ownedSourceSettingsTarget(t), ownedSourceSettingsInput(t)
			if scenario == "existing-conflict" || scenario == "corrupt-record" {
				if _, e := ImportReviewedSourceSettings(context.Background(), dir, input, true); e != nil {
					t.Fatal(e)
				}
			}
			s, e := OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			switch scenario {
			case "SQL-failure":
				_, e = s.DB.Exec(`CREATE TRIGGER owned_fail_source_settings BEFORE INSERT ON records WHEN NEW.kind='source_network_settings' BEGIN SELECT RAISE(ABORT,'owned failure'); END`)
			case "partial-record":
				p := &PluginStore{store: s}
				sealed, err := p.seal("source_statistics_settings", "system", "main", []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				_, e = s.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('source_statistics_settings','main',?,'owned')`, sealed)
			case "active-work":
				_, e = s.DB.Exec(`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES('owned','owned','owned','queued','{}',0,'owned','owned')`)
			case "corrupt-record":
				_, e = s.DB.Exec(`UPDATE records SET payload=zeroblob(length(payload)) WHERE kind='source_network_settings'`)
			case "existing-conflict":
				input = bytes.Replace(input, []byte("owned-source-token"), []byte("different-owned-source-token"), 1)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			before := ownedSourceSettingsFiles(t, dir)
			if _, e = ImportReviewedSourceSettings(context.Background(), dir, input, true); e == nil {
				t.Fatal("invalid target imported")
			}
			after := ownedSourceSettingsFiles(t, dir)
			if scenario != "SQL-failure" && !reflect.DeepEqual(before, after) {
				t.Fatal("rejected import changed target files")
			}
			if scenario == "SQL-failure" {
				s, e := OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				var count int
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('source_statistics_settings','source_network_settings','source_runtime_settings','source_settings_migration')`).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial failed transaction survived", count, e)
				}
			}
		})
	}
}

func TestReviewedSourceSettingsStrictInputBoundary(t *testing.T) {
	valid := ownedSourceSettingsInput(t)
	if r, e := ValidateReviewedSourceSettingsInput(valid); e != nil || r.State != "validated" || r.Records != 4 || r.Writes != 0 || r.ExternalCalls != 0 || r.JobsQueued != 0 {
		t.Fatal("input-only review failed", r, e)
	}
	var seed map[string]any
	if json.Unmarshal(valid, &seed) != nil {
		t.Fatal("invalid owned seed")
	}
	type change func(map[string]any)
	for name, mutate := range map[string]change{
		"absent-local-timezone":       func(v map[string]any) { delete(v["Runtime"].(map[string]any), "LocalTimezone") },
		"empty-local-timezone":        func(v map[string]any) { v["Runtime"].(map[string]any)["LocalTimezone"] = "" },
		"unconfirmed-localzone-cache": func(v map[string]any) { v["Source"].(map[string]any)["TimezoneMode"] = "verified_live_cache" },
		"tzlocal-version-drift":       func(v map[string]any) { v["Source"].(map[string]any)["TzlocalVersion"] = "0" },
		"absent-proxy":                func(v map[string]any) { delete(v["Network"].(map[string]any), "Proxy") },
		"absent-snapshot-proxy": func(v map[string]any) {
			delete(v["Statistics"].(map[string]any)["Snapshot"].(map[string]any), "proxies")
		},
		"absent-environment":     func(v map[string]any) { delete(v["Network"].(map[string]any), "Environment") },
		"null-environment":       func(v map[string]any) { v["Network"].(map[string]any)["Environment"] = nil },
		"unknown-source-runtime": func(v map[string]any) { v["Source"].(map[string]any)["Mode"] = "live_settings" },
		"unconfirmed-UID-cache":  func(v map[string]any) { v["Source"].(map[string]any)["UIDMode"] = "verified_live_cache" },
		"omitted-audit-evidence": func(v map[string]any) { delete(v["Source"].(map[string]any), "NetworkCalls") },
		"nonzero-source-writes":  func(v map[string]any) { v["Source"].(map[string]any)["OriginalWrites"] = 1 },
		"source-code-drift": func(v map[string]any) {
			v["Source"].(map[string]any)["SourceHashes"].(map[string]any)["/app/version.py"] = strings.Repeat("a", 64)
		},
		"other-environment": func(v map[string]any) { v["Network"].(map[string]any)["Environment"] = []string{"API_TOKEN=owned"} },
		"extra-header": func(v map[string]any) {
			v["Network"].(map[string]any)["GitHubHeaders"].(map[string]any)["Cookie"] = "owned"
		},
		"snapshot-env-override": func(v map[string]any) {
			v["Statistics"].(map[string]any)["Snapshot"].(map[string]any)["environment"] = nil
		},
		"false-known":            func(v map[string]any) { v["Network"].(map[string]any)["Known"] = false },
		"missing-share":          func(v map[string]any) { delete(v["Statistics"].(map[string]any)["Snapshot"].(map[string]any), "share") },
		"null-share":             func(v map[string]any) { v["Statistics"].(map[string]any)["Snapshot"].(map[string]any)["share"] = nil },
		"different-global-proxy": func(v map[string]any) { v["Network"].(map[string]any)["Proxy"] = nil },
		"changed-field-case":     func(v map[string]any) { v["version"] = v["Version"]; delete(v, "Version") },
		"unknown-top-field":      func(v map[string]any) { v["Other"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			json.Unmarshal(valid, &value)
			mutate(value)
			raw, _ := json.Marshal(value)
			if _, _, e := validateSourceSettings(raw); e == nil {
				t.Fatal("unsupported input silently accepted")
			}
		})
	}
	for name, raw := range map[string][]byte{
		"duplicate":      bytes.Replace(valid, []byte(`"Version":1`), []byte(`"Version":99,"Version":1`), 1),
		"lone-surrogate": bytes.Replace(valid, []byte("owned-source-token"), []byte(`\ud800`), 1),
		"invalid-UTF8":   append(append([]byte{}, valid...), 255),
		"second-object":  append(append([]byte{}, valid...), []byte(` {}`)...),
		"null":           []byte(`null`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, e := validateSourceSettings(raw); e == nil {
				t.Fatal("ambiguous or lossy JSON accepted")
			}
		})
	}
	for _, raw := range []string{`"\ud83d\ude00"`, `"\\ud800"`, `"literal�"`, `"a\"b"`} {
		var text string
		if sourceSettingsJSON([]byte(raw), &text) != nil {
			t.Fatal("valid Unicode boundary rejected", raw)
		}
	}
	// Known empty and actual None are not missing state. No source default is
	// substituted for false share, empty host, headers or an explicit empty env.
	var value sourceSettingsInput
	json.Unmarshal(valid, &value)
	no := false
	empty := []string{}
	value.Statistics.Snapshot.Share = &no
	value.Statistics.Snapshot.ServerHost = ""
	value.Statistics.Snapshot.Proxies = nil
	value.Statistics.Environment = &empty
	value.Network.Proxy = nil
	value.Network.GitHubHeaders = map[string]string{}
	value.Network.Environment = empty
	value.Runtime.Timezone = ""
	raw, _ := json.Marshal(value)
	if _, _, e := validateSourceSettings(raw); e != nil {
		t.Fatal("legitimate empty source state rejected", e)
	}
}
