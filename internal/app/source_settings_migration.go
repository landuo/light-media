package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"light-media/internal/subscriptionstats"
)

// This importer has no HTTP route. A one-purpose offline command passes only a
// reviewed selected source snapshot through stdin. It never starts a worker,
// writes the original system, or imports a process-wide environment.
type sourceSettingsEvidence struct {
	Mode, UIDMode, EnvPath, EnvFileSHA256 string
	TimezoneMode, TzlocalVersion          string
	SourceHashes                          map[string]string
	SelectedFields, NormalizationFields   []string
	NetworkCalls, OriginalWrites          *int
}
type sourceImportedStatistics struct {
	Version     int
	Snapshot    subscriptionstats.SettingsSnapshot
	Environment *[]string
}
type sourceImportedNetwork struct {
	Version       int
	Known         bool
	GitHubHeaders map[string]string
	Proxy         map[string]string
	Environment   []string
}
type sourceImportedRuntime struct {
	Version       int
	Known         bool
	Timezone      string
	LocalTimezone string
}
type sourceSettingsInput struct {
	Version    int
	Source     sourceSettingsEvidence
	Statistics sourceImportedStatistics
	Network    sourceImportedNetwork
	Runtime    sourceImportedRuntime
}
type sourceSettingsReceipt struct {
	Version, Records int
	Digest           string
	Source           sourceSettingsEvidence
	ImportedAt       string
}

// SourceSettingsImportResult contains no config values, credential hashes or
// derived UID. Cold derivation is explicitly distinct from live process state.
type SourceSettingsImportResult struct {
	State         string `json:"state"`
	SourceMode    string `json:"source_mode"`
	UIDMode       string `json:"uid_mode"`
	TimezoneMode  string `json:"timezone_mode"`
	Records       int    `json:"records"`
	Writes        int    `json:"writes"`
	JobsQueued    int    `json:"jobs_queued"`
	ExternalCalls int    `json:"external_calls"`
}

// ValidateReviewedSourceSettingsInput does not open a target or make a request.
// It is used before any outage to review a private stdin capture in memory.
func ValidateReviewedSourceSettingsInput(raw []byte) (SourceSettingsImportResult, error) {
	in, _, e := validateSourceSettings(raw)
	if e != nil {
		return SourceSettingsImportResult{}, e
	}
	return SourceSettingsImportResult{State: "validated", SourceMode: in.Source.Mode, UIDMode: in.Source.UIDMode, TimezoneMode: in.Source.TimezoneMode, Records: 4}, nil
}

var reviewedSourceSettingsHashes = map[string]string{
	"/app/app/core/config.py":   "f19111073a7ef25ecd0b7e9d53dab343d76fe64ff3ad821f4ff39a790c104033",
	"/app/app/utils/system.py":  "3477c8585dd955bcb42f7180e5fabaae05c528af8ddb0cbae0935dbdb0c031db",
	"/app/app/helper/server.py": "d71d14373d8984390c6b7a6669ce6f6ffc4258fcf5f163ee189c52c8d3b0d47f",
	"/app/version.py":           "02e895f5f3678698656958e333a7eb03b81280aa196ed39bf60974529ec6a1e4",
	"/opt/venv/lib/python3.12/site-packages/tzlocal/__init__.py": "a6d7c04d705c6d917d62eaedfd73b1d72bc6044dafebc358b701b84bd567d181",
	"/opt/venv/lib/python3.12/site-packages/tzlocal/unix.py":     "68e936f6076c4dac8c9e8a4a76fb6a7cec73f38294ee7dd92d493d03f15bd876",
	"/opt/venv/lib/python3.12/site-packages/tzlocal/utils.py":    "a2ff1f3f03e3caf5d1a96f56cf1fec9004ec8e207d800ccad42107276508f756",
}

var reviewedSourceSettingsFields = []string{"PROJECT_NAME", "CONFIG_DIR", "TZ", "SUBSCRIBE_STATISTIC_SHARE", "MP_SERVER_HOST", "GITHUB_TOKEN", "PROXY_HOST"}

func invalidSourceSettings() error { return errors.New("invalid_reviewed_source_settings") }
func sourceSettingsDigest(raw []byte) string {
	v := sha256.Sum256(raw)
	return hex.EncodeToString(v[:])
}
func sourceSettingsJSON(raw []byte, out any) error {
	if !utf8.Valid(raw) || sourceSettingsUnpairedSurrogate(raw) {
		return invalidSourceSettings()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 12 {
			return invalidSourceSettings()
		}
		t, e := d.Token()
		if e != nil {
			return invalidSourceSettings()
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				name, ok := key.(string)
				if e != nil || !ok || seen[name] {
					return invalidSourceSettings()
				}
				seen[name] = true
				if visit(depth+1) != nil {
					return invalidSourceSettings()
				}
			}
			if t, e = d.Token(); e != nil || t != json.Delim('}') {
				return invalidSourceSettings()
			}
		case json.Delim('['):
			for d.More() {
				if visit(depth+1) != nil {
					return invalidSourceSettings()
				}
			}
			if t, e = d.Token(); e != nil || t != json.Delim(']') {
				return invalidSourceSettings()
			}
		case json.Delim('}'), json.Delim(']'):
			return invalidSourceSettings()
		}
		return nil
	}
	if visit(0) != nil {
		return invalidSourceSettings()
	}
	if _, e := d.Token(); e != io.EOF {
		return invalidSourceSettings()
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return invalidSourceSettings()
	}
	return nil
}

// encoding/json replaces isolated escaped UTF-16 surrogates. Preserve the
// boundary: unsupported source strings must not be silently changed on import.
func sourceSettingsUnpairedSurrogate(raw []byte) bool {
	unit := func(i int) (uint16, bool) {
		if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
			return 0, false
		}
		var value uint16
		for _, c := range raw[i+2 : i+6] {
			value *= 16
			switch {
			case c >= '0' && c <= '9':
				value += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				value += uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				value += uint16(c-'A') + 10
			default:
				return 0, false
			}
		}
		return value, true
	}
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		value, ok := unit(i)
		if !ok {
			i++
			continue
		}
		if value >= 0xd800 && value <= 0xdbff {
			low, ok := unit(i + 6)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return true
			}
			i += 11
		} else if value >= 0xdc00 && value <= 0xdfff {
			return true
		} else {
			i += 5
		}
	}
	return false
}

func sourceSettingsObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || len(value) != len(keys) {
		return nil, invalidSourceSettings()
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return nil, invalidSourceSettings()
		}
	}
	return value, nil
}

func validateSourceSettings(raw []byte) (sourceSettingsInput, []byte, error) {
	var in sourceSettingsInput
	if len(raw) == 0 || len(raw) > 1<<20 || sourceSettingsJSON(raw, &in) != nil || in.Version != 1 || in.Source.Mode != "selected_installed_cold_settings" || in.Source.UIDMode != "derived_rootfs_not_live_helper_cache" || in.Source.EnvPath != "/config/app.env" || !pluginStorageDigest.MatchString(in.Source.EnvFileSHA256) || !reflect.DeepEqual(in.Source.SourceHashes, reviewedSourceSettingsHashes) || !reflect.DeepEqual(in.Source.SelectedFields, reviewedSourceSettingsFields) || in.Source.NetworkCalls == nil || *in.Source.NetworkCalls != 0 || in.Source.OriginalWrites == nil || *in.Source.OriginalWrites != 0 || in.Source.NormalizationFields == nil {
		return in, nil, invalidSourceSettings()
	}
	if in.Source.TimezoneMode != "derived_installed_tzlocal_not_live_cache" || in.Source.TzlocalVersion != "5.4.4" {
		return in, nil, invalidSourceSettings()
	}
	root, e := sourceSettingsObject(raw, "Version", "Source", "Statistics", "Network", "Runtime")
	if e != nil {
		return in, nil, e
	}
	if _, e = sourceSettingsObject(root["Source"], "Mode", "UIDMode", "TimezoneMode", "TzlocalVersion", "EnvPath", "EnvFileSHA256", "SourceHashes", "SelectedFields", "NormalizationFields", "NetworkCalls", "OriginalWrites"); e != nil {
		return in, nil, e
	}
	stats, e := sourceSettingsObject(root["Statistics"], "Version", "Snapshot", "Environment")
	if e != nil {
		return in, nil, e
	}
	if _, e = sourceSettingsObject(stats["Snapshot"], "share", "known", "server_host", "user_uid", "user_agent", "proxies"); e != nil {
		return in, nil, e
	}
	if _, e = sourceSettingsObject(root["Network"], "Version", "Known", "GitHubHeaders", "Proxy", "Environment"); e != nil {
		return in, nil, e
	}
	if _, e = sourceSettingsObject(root["Runtime"], "Version", "Known", "Timezone", "LocalTimezone"); e != nil || in.Runtime.Version != 1 || !in.Runtime.Known || in.Runtime.LocalTimezone == "" {
		return in, nil, invalidSourceSettings()
	}
	seen := map[string]bool{}
	for _, f := range in.Source.NormalizationFields {
		found := false
		for _, allowed := range reviewedSourceSettingsFields {
			found = found || allowed == f
		}
		if seen[f] || !found {
			return in, nil, invalidSourceSettings()
		}
		seen[f] = true
	}
	s := in.Statistics.Snapshot
	if in.Statistics.Version != 1 || in.Network.Version != 1 || !s.Known || !in.Network.Known || s.Share == nil || s.UserUID == nil || !pluginStorageDigest.MatchString(*s.UserUID) || s.Environment != nil || in.Statistics.Environment == nil || *in.Statistics.Environment == nil || in.Network.Environment == nil || !reflect.DeepEqual(*in.Statistics.Environment, in.Network.Environment) || !reflect.DeepEqual(s.Proxies, in.Network.Proxy) || in.Network.GitHubHeaders == nil {
		return in, nil, invalidSourceSettings()
	}
	if in.Network.Proxy != nil && (len(in.Network.Proxy) != 2 || in.Network.Proxy["http"] == "" || in.Network.Proxy["https"] == "") {
		return in, nil, invalidSourceSettings()
	}
	if len(in.Network.GitHubHeaders) != 0 && (len(in.Network.GitHubHeaders) != 2 || !strings.HasPrefix(in.Network.GitHubHeaders["Authorization"], "Bearer ") || in.Network.GitHubHeaders["Authorization"] == "Bearer " || in.Network.GitHubHeaders["User-Agent"] != "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36") {
		return in, nil, invalidSourceSettings()
	}
	seen = map[string]bool{}
	for _, pair := range in.Network.Environment {
		key, _, found := strings.Cut(pair, "=")
		if !found || seen[key] {
			return in, nil, invalidSourceSettings()
		}
		switch key {
		case "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy":
		default:
			return in, nil, invalidSourceSettings()
		}
		seen[key] = true
	}
	canonical, e := json.Marshal(in)
	if e != nil {
		return in, nil, invalidSourceSettings()
	}
	return in, canonical, nil
}

// ImportReviewedSourceSettings locks an existing stopped target. Dry runs open
// SQLite with mode=ro and leave bytes, mtime, permissions, key and schema alone.
// Apply writes exactly four AEAD records atomically, with no schedule/job/run.
func ImportReviewedSourceSettings(ctx context.Context, dir string, raw []byte, apply bool) (SourceSettingsImportResult, error) {
	result := SourceSettingsImportResult{}
	in, canonical, e := validateSourceSettings(raw)
	if e != nil {
		return result, e
	}
	target, e := openSourceSettingsTarget(ctx, dir, apply)
	if e != nil {
		return result, e
	}
	defer target.close()
	p, tx := target.p, target.tx
	names := []string{"source_statistics_settings", "source_network_settings", "source_runtime_settings", "source_settings_migration"}
	values := []any{in.Statistics, in.Network, in.Runtime, sourceSettingsReceipt{Version: 1, Records: 3, Digest: sourceSettingsDigest(canonical), Source: in.Source, ImportedAt: now()}}
	old := make([][]byte, len(names))
	count := 0
	for i, name := range names {
		var sealed []byte
		e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, name).Scan(&sealed)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return result, errors.New("source_settings_target_read_failed")
		}
		old[i], e = p.unseal(name, "system", "main", sealed)
		if e != nil {
			return result, errors.New("source_settings_existing_record_unconfirmed")
		}
		count++
	}
	result = SourceSettingsImportResult{State: "ready", SourceMode: in.Source.Mode, UIDMode: in.Source.UIDMode, TimezoneMode: in.Source.TimezoneMode, Records: len(names)}
	if count > 0 {
		var receipt sourceSettingsReceipt
		if count != 4 || sourceSettingsJSON(old[3], &receipt) != nil || receipt.Version != 1 || receipt.Records != 3 || receipt.Digest != sourceSettingsDigest(canonical) || !reflect.DeepEqual(receipt.Source, in.Source) || receipt.ImportedAt == "" {
			return SourceSettingsImportResult{}, errors.New("source_settings_existing_import_conflicts")
		}
		for i := 0; i < 3; i++ {
			want, _ := json.Marshal(values[i])
			if !bytes.Equal(old[i], want) {
				return SourceSettingsImportResult{}, errors.New("source_settings_existing_import_conflicts")
			}
		}
		result.State = "already_imported"
		return result, nil
	}
	if !apply {
		return result, nil
	}
	for i, name := range names {
		body, _ := json.Marshal(values[i])
		sealed, e := p.seal(name, "system", "main", body)
		if e != nil {
			return SourceSettingsImportResult{}, errors.New("source_settings_encryption_failed")
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,?)`, name, sealed, now()); e != nil {
			return SourceSettingsImportResult{}, errors.New("source_settings_import_transaction_failed")
		}
	}
	if tx.Commit() != nil {
		return SourceSettingsImportResult{}, errors.New("source_settings_commit_unconfirmed")
	}
	result.State, result.Writes = "imported", len(names)
	return result, nil
}
