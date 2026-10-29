package categoryeditor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"light-media/internal/plugins"
)

const (
	categorySourceSHA       = "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27"
	categorySourceInputSHA  = "2d3a4cdf9ef8af9431181bcb419961f3aa10b1fa5c4668ee3839482b2edb32b0"
	categorySourceOracleSHA = "a92f0462e1708e293baa21203029e78071512a4afc78a3a038b3774733b02aca"
	categorySourceDriverSHA = "d1d7ad9d06a3da07f6ef172407414257599957b7cfa5574279c968daea8194d0"
)

type sourceTypedValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type sourceObjectMembers struct {
	Enabled sourceTypedValue `json:"enabled"`
	Content sourceTypedValue `json:"content"`
}

type sourceReplaySnapshot struct {
	Members       sourceObjectMembers `json:"members"`
	Notifications json.RawMessage     `json:"notifications"`
	Updates       json.RawMessage     `json:"update_arguments"`
	Store         json.RawMessage     `json:"controlled_adapter_store_not_original_db"`
}

type sourceReplayStep struct {
	Op     string            `json:"op"`
	Config json.RawMessage   `json:"config"`
	Fault  map[string]string `json:"fault"`
}

type sourceReplayCase struct {
	ID              string            `json:"id"`
	Steps           []json.RawMessage `json:"steps"`
	UserPathMode    string            `json:"user_path_mode"`
	DefaultPathMode string            `json:"default_path_mode"`
}

type sourceReplayEvent struct {
	Event   string                     `json:"event"`
	Members sourceObjectMembers        `json:"members"`
	Data    sourceTypedValue           `json:"data"`
	Args    []json.RawMessage          `json:"args"`
	Kwargs  map[string]json.RawMessage `json:"kwargs"`
	Config  json.RawMessage            `json:"config"`
}

type sourceReplayObservation struct {
	Step      int                             `json:"step"`
	Input     json.RawMessage                 `json:"input"`
	Return    *sourceTypedValue               `json:"return"`
	Exception *struct{ Type, Message string } `json:"exception"`
	Events    []sourceReplayEvent             `json:"events"`
	Before    sourceReplaySnapshot            `json:"before"`
	After     sourceReplaySnapshot            `json:"after"`
}

type sourceExpectedCallback struct {
	method, phase string
	payload       json.RawMessage
	members       sourceObjectMembers
}

type sourceDifferentialCallback struct {
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
	Enabled json.RawMessage `json:"enabled_at_call"`
	Content json.RawMessage `json:"content_at_call"`
}

type sourceDifferentialObservation struct {
	CaseID          string                       `json:"case_id"`
	Step            int                          `json:"step"`
	Op              string                       `json:"op"`
	Result          json.RawMessage              `json:"return"`
	Raised          bool                         `json:"raised"`
	ExceptionPhase  string                       `json:"exception_phase,omitempty"`
	SourceException string                       `json:"source_exception_class,omitempty"`
	Enabled         json.RawMessage              `json:"enabled"`
	Content         json.RawMessage              `json:"content"`
	Callbacks       []sourceDifferentialCallback `json:"callbacks"`
	Notifications   json.RawMessage              `json:"notifications"`
	Updates         json.RawMessage              `json:"update_arguments"`
	Store           json.RawMessage              `json:"controlled_store_not_original_db"`
}

type sourceDifferentialCoverage struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Steps  int    `json:"steps"`
	Reason string `json:"reason,omitempty"`
}

func sourceReadBound(t *testing.T, directory, name, expected string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expected {
		t.Fatalf("fixed original artifact changed: %s", name)
	}
	return raw
}

func sourceMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func sourceAssertJSON(t *testing.T, label string, actual, expected json.RawMessage) {
	t.Helper()
	if !sourceJSONEqual(actual, expected) {
		t.Fatalf("%s differs from independent Python observation\n got: %s\nwant: %s", label, actual, expected)
	}
}

// This comparison does not call the target decoder/truthiness implementation.
// Object key order and whitespace are immaterial. Numeric kinds and lexical
// values remain distinct. String comparison retains unpaired UTF-16 surrogates
// instead of allowing encoding/json's U+FFFD replacement to mask a port bug.
func sourceJSONEqual(left, right json.RawMessage) bool {
	left, right = bytes.TrimSpace(left), bytes.TrimSpace(right)
	if !json.Valid(left) || !json.Valid(right) || left[0] != right[0] {
		return false
	}
	switch left[0] {
	case '"':
		return reflect.DeepEqual(sourceStringUnits(left), sourceStringUnits(right))
	case '{':
		var a, b map[string]json.RawMessage
		if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil || len(a) != len(b) {
			return false
		}
		for key, value := range a {
			other, exists := b[key]
			if !exists || !sourceJSONEqual(value, other) {
				return false
			}
		}
		return true
	case '[':
		var a, b []json.RawMessage
		if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !sourceJSONEqual(a[i], b[i]) {
				return false
			}
		}
		return true
	default:
		return bytes.Equal(left, right)
	}
}

func sourceStringUnits(raw []byte) []uint16 {
	units := []uint16{}
	for i := 1; i < len(raw)-1; {
		if raw[i] != '\\' {
			r, size := utf8.DecodeRune(raw[i:])
			units = append(units, utf16.Encode([]rune{r})...)
			i += size
			continue
		}
		i++
		switch raw[i] {
		case 'u':
			value, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			units = append(units, uint16(value))
			i += 5
		case 'b':
			units = append(units, '\b')
			i++
		case 'f':
			units = append(units, '\f')
			i++
		case 'n':
			units = append(units, '\n')
			i++
		case 'r':
			units = append(units, '\r')
			i++
		case 't':
			units = append(units, '\t')
			i++
		default:
			units = append(units, uint16(raw[i]))
			i++
		}
	}
	return units
}

func sourceAssertTyped(t *testing.T, label string, actual json.RawMessage, expected sourceTypedValue) {
	t.Helper()
	value := bytes.TrimSpace(actual)
	if !json.Valid(value) {
		t.Fatalf("%s returned invalid JSON: %q", label, value)
	}
	kind := "int"
	switch value[0] {
	case 'n':
		kind = "NoneType"
	case 't', 'f':
		kind = "bool"
	case '"':
		kind = "str"
	case '{':
		kind = "dict"
	case '[':
		kind = "list"
	default:
		if bytes.ContainsAny(value, ".eE") {
			kind = "float"
		}
	}
	if kind != expected.Type {
		t.Fatalf("%s type=%s, original type=%s", label, kind, expected.Type)
	}
	sourceAssertJSON(t, label, value, expected.Value)
}

func sourceAssertMembers(t *testing.T, p *Plugin, expected sourceObjectMembers) {
	t.Helper()
	sourceAssertTyped(t, "enabled member", p.enabled, expected.Enabled)
	sourceAssertTyped(t, "content member", p.content, expected.Content)
}

func sourceSkippedCase(c sourceReplayCase) string {
	if c.UserPathMode != "" {
		return "real user-file path/permission failure belongs to App/filesystem integration, not a mock host"
	}
	if c.DefaultPathMode != "" {
		return "real bundled-default read/encoding/permission failure belongs to host integration"
	}
	for _, raw := range c.Steps {
		var step sourceReplayStep
		_ = json.Unmarshal(raw, &step)
		switch step.Op {
		case "get_form", "set_default_bytes", "change_settings_paths":
			return "bundled-default file reads/mutations and frozen source settings paths are outside this pure-worker boundary replay"
		}
	}
	return ""
}

func sourceCallbacks(t *testing.T, observation sourceReplayObservation) []sourceExpectedCallback {
	t.Helper()
	expected := []sourceExpectedCallback{}
	for _, event := range observation.Events {
		switch event.Event {
		case "path.write_text.begin":
			expected = append(expected, sourceExpectedCallback{plugins.CategoryApplyCapability, "apply", sourceMarshal(t, map[string]any{"content": event.Data.Value}), event.Members})
		case "systemmessage.put":
			if len(event.Args) != 1 || len(event.Kwargs) != 1 {
				t.Fatal("source systemmessage signature changed")
			}
			expected = append(expected, sourceExpectedCallback{plugins.SystemMessagePutCapability, "message", sourceMarshal(t, map[string]any{"message": event.Args[0], "title": event.Kwargs["title"]}), event.Members})
		case "update_config":
			expected = append(expected, sourceExpectedCallback{plugins.BaseUpdateConfig, "update", sourceMarshal(t, map[string]any{"config": event.Config}), event.Members})
		}
	}
	return expected
}

func sourceFailurePhase(step sourceReplayStep, observation sourceReplayObservation) string {
	if observation.Exception == nil {
		return ""
	}
	if step.Fault["update"] == "raise" {
		return "update"
	}
	if step.Fault["message"] == "raise" {
		return "message"
	}
	if step.Fault["helper_constructor"] == "raise" || step.Fault["helper_init"] == "raise" {
		return "apply"
	}
	for _, event := range observation.Events {
		if event.Event == "path.write_text.error" {
			return "apply"
		}
	}
	return "worker"
}

// TestCategoryEditorSourceOracleReplay consumes the independent full-module
// Python recording. It drives the real Plugin.Handle, but the callback adapter
// is explicitly controlled: no filesystem/helper/database success is asserted.
// The fixed source artifacts are committed with the repository. An explicit
// CATEGORYEDITOR_SOURCE_EVIDENCE may select another copy with identical hashes;
// missing artifacts fail rather than silently skipping the differential.
func TestCategoryEditorSourceOracleReplay(t *testing.T) {
	directory := os.Getenv("CATEGORYEDITOR_SOURCE_EVIDENCE")
	if directory == "" {
		directory = filepath.Join("..", "..", "..", "migration", "evidence", "categoryeditor", "source")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	inputRaw := sourceReadBound(t, directory, "categoryeditor-source-inputs.json", categorySourceInputSHA)
	oracleRaw := sourceReadBound(t, directory, "categoryeditor-source-oracle.json", categorySourceOracleSHA)
	sourceReadBound(t, directory, "categoryeditor-source-replay.py", categorySourceDriverSHA)
	var inputs struct{ Cases []sourceReplayCase }
	var oracle struct {
		Source      struct{ SHA256 string }
		InputSHA    string          `json:"inputs_sha256"`
		DriverSHA   string          `json:"driver_sha256"`
		Environment json.RawMessage `json:"environment"`
		Cases       []struct {
			ID           string                    `json:"id"`
			Observations []sourceReplayObservation `json:"observations"`
		}
	}
	if err := json.Unmarshal(inputRaw, &inputs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(oracleRaw, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Source.SHA256 != categorySourceSHA || oracle.InputSHA != categorySourceInputSHA || oracle.DriverSHA != categorySourceDriverSHA || len(inputs.Cases) != 78 || len(oracle.Cases) != len(inputs.Cases) {
		t.Fatal("independent source provenance/count does not match the pinned replay")
	}
	coverage := []sourceDifferentialCoverage{}
	observed := []sourceDifferentialObservation{}
	passed, selectedSteps, skipped, skippedSteps := 0, 0, 0, 0
	executedCases, filteredCases, filteredSteps := 0, 0, 0
	for i, c := range inputs.Cases {
		reference := oracle.Cases[i]
		if reference.ID != c.ID || len(reference.Observations) != len(c.Steps) {
			t.Fatal("source case/input alignment changed")
		}
		if reason := sourceSkippedCase(c); reason != "" {
			coverage = append(coverage, sourceDifferentialCoverage{c.ID, "not_run", len(c.Steps), reason})
			skipped++
			skippedSteps += len(c.Steps)
			t.Run(c.ID, func(t *testing.T) { t.Skip(reason) })
			continue
		}
		selectedSteps += len(c.Steps)
		executed := false
		ok := t.Run(c.ID, func(t *testing.T) {
			executed = true
			executedCases++
			p := New()
			notifications, updates := []json.RawMessage{}, []json.RawMessage{}
			store := json.RawMessage(`null`)
			for number, rawStep := range c.Steps {
				var step sourceReplayStep
				if err := json.Unmarshal(rawStep, &step); err != nil {
					t.Fatal(err)
				}
				ref := reference.Observations[number]
				if ref.Step != number {
					t.Fatal("source step index changed")
				}
				sourceAssertJSON(t, "source input alignment", rawStep, ref.Input)
				sourceAssertMembers(t, p, ref.Before.Members)
				expected := sourceCallbacks(t, ref)
				calls := []sourceDifferentialCallback{}
				failurePhase := sourceFailurePhase(step, ref)
				hostFailure := errors.New("controlled original callback failure at " + failurePhase)
				host := func(_ context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
					index := len(calls)
					calls = append(calls, sourceDifferentialCallback{method, append(json.RawMessage(nil), payload...), append(json.RawMessage(nil), p.enabled...), append(json.RawMessage(nil), p.content...)})
					if index >= len(expected) {
						t.Fatalf("unexpected callback at step %d: %s", number, method)
					}
					want := expected[index]
					if method != want.method {
						t.Fatalf("callback order at step %d: got %s want %s", number, method, want.method)
					}
					sourceAssertJSON(t, "callback argument", payload, want.payload)
					sourceAssertMembers(t, p, want.members)
					var arguments map[string]json.RawMessage
					if err := json.Unmarshal(payload, &arguments); err != nil {
						t.Fatal(err)
					}
					switch method {
					case plugins.CategoryApplyCapability:
						// Whole write_text + helper boundary is deliberately a
						// replayed response, not a replacement file/helper test.
						if failurePhase == "apply" {
							return nil, hostFailure
						}
						return json.RawMessage(`null`), nil
					case plugins.SystemMessagePutCapability:
						if failurePhase == "message" {
							return nil, hostFailure
						}
						notifications = append(notifications, sourceMarshal(t, map[string]any{"args": []json.RawMessage{arguments["message"]}, "kwargs": map[string]json.RawMessage{"title": arguments["title"]}}))
						if step.Fault["message"] == "false" {
							return json.RawMessage(`false`), nil
						}
						return json.RawMessage(`null`), nil
					case plugins.BaseUpdateConfig:
						updates = append(updates, append(json.RawMessage(nil), arguments["config"]...))
						if failurePhase == "update" {
							return nil, hostFailure
						}
						switch step.Fault["update"] {
						case "false":
							return json.RawMessage(`false`), nil
						case "none":
							return json.RawMessage(`null`), nil
						default:
							store = append(json.RawMessage(nil), arguments["config"]...)
							return json.RawMessage(`true`), nil
						}
					}
					t.Fatal("unsupported callback in source projection")
					return nil, nil
				}
				var result json.RawMessage
				var handleErr error
				if step.Op == "new_object" {
					p, result = New(), json.RawMessage(`null`)
				} else {
					result, handleErr = p.Handle(context.Background(), step.Op, step.Config, host)
				}
				if len(calls) != len(expected) {
					t.Fatalf("callback count: got %d, original projection %d", len(calls), len(expected))
				}
				if (handleErr != nil) != (ref.Exception != nil) {
					t.Fatalf("exception timing differs at step %d: target=%v source=%+v", number, handleErr, ref.Exception)
				}
				if failurePhase != "" && failurePhase != "worker" && !errors.Is(handleErr, hostFailure) {
					t.Fatalf("original callback error was not propagated: %v", handleErr)
				}
				if handleErr == nil {
					if ref.Return == nil {
						t.Fatal("source successful return missing")
					}
					sourceAssertTyped(t, "return", result, *ref.Return)
				}
				sourceAssertMembers(t, p, ref.After.Members)
				noticeRaw, updateRaw := sourceMarshal(t, notifications), sourceMarshal(t, updates)
				sourceAssertJSON(t, "cumulative notification arguments", noticeRaw, ref.After.Notifications)
				sourceAssertJSON(t, "cumulative complete update_config arguments", updateRaw, ref.After.Updates)
				sourceAssertJSON(t, "controlled adapter store", store, ref.After.Store)
				row := sourceDifferentialObservation{CaseID: c.ID, Step: number, Op: step.Op, Result: result, Raised: handleErr != nil, ExceptionPhase: failurePhase, Enabled: append(json.RawMessage(nil), p.enabled...), Content: append(json.RawMessage(nil), p.content...), Callbacks: calls, Notifications: noticeRaw, Updates: updateRaw, Store: append(json.RawMessage(nil), store...)}
				if ref.Exception != nil {
					row.SourceException = ref.Exception.Type
				}
				observed = append(observed, row)
			}
		})
		if !executed {
			filteredCases++
			filteredSteps += len(c.Steps)
			coverage = append(coverage, sourceDifferentialCoverage{c.ID, "not_run", len(c.Steps), "excluded by go test subtest filter"})
			continue
		}
		status := "failed"
		if ok {
			status = "passed"
			passed++
		}
		coverage = append(coverage, sourceDifferentialCoverage{c.ID, status, len(c.Steps), ""})
	}
	if selectedSteps != 196 || skipped != 10 || skippedSteps != 24 {
		t.Errorf("explicit source subset changed: selected_steps=%d skipped=%d skipped_steps=%d", selectedSteps, skipped, skippedSteps)
	}
	if prefix := os.Getenv("CATEGORYEDITOR_WORKER_DIFFERENTIAL_OUTPUT"); prefix != "" {
		prefix, err = filepath.Abs(prefix)
		externalEvidence, pathErr := filepath.Abs(filepath.Join("..", "..", "..", "..", "evidence"))
		if err != nil || pathErr != nil || (filepath.Dir(prefix) != directory && filepath.Dir(prefix) != externalEvidence) || !strings.HasPrefix(filepath.Base(prefix), "categoryeditor-worker-differential-") {
			t.Fatal("differential outputs must stay in a source/task evidence directory under categoryeditor-worker-differential-*")
		}
		targets := map[string]string{}
		for _, path := range []string{"categoryeditor.go", "form.json", "source_oracle_test.go", "../base.go", "../category_policy.go", "../../../go.mod", "../../../go.sum"} {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(raw)
			targets[filepath.ToSlash(filepath.Clean(filepath.Join("internal/plugins/categoryeditor", path)))] = hex.EncodeToString(digest[:])
		}
		report := map[string]any{
			"kind":                 "independent_source_recording_vs_real_worker_with_controlled_host_boundary",
			"source_plugin_sha256": categorySourceSHA, "source_inputs_sha256": categorySourceInputSHA,
			"source_oracle_sha256": categorySourceOracleSHA, "source_driver_sha256": categorySourceDriverSHA,
			"source_environment": oracle.Environment, "target_files_sha256": targets,
			"target_environment": map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH},
			"passed_cases":       passed, "selected_cases": len(inputs.Cases) - skipped, "selected_steps": selectedSteps,
			"executed_cases": executedCases, "completed_steps": len(observed),
			"not_run_cases": skipped + filteredCases, "not_run_steps": skippedSteps + filteredSteps, "cases": coverage,
			"compared":      []string{"source inputs", "return JSON kind and value", "enabled/content before and after each step", "callback order and exact arguments", "members at callback boundaries", "exception occurrence and boundary phase", "cumulative notification arguments", "full update_config replacement arguments", "controlled adapter store after true/false/None/exception"},
			"normalization": []string{"JSON object key order and whitespace ignored", "strings compared as UTF-16 code units, preserving unpaired surrogates", "numbers retain integer/float kind and lexical value", "Python exception class/message not required to equal Go errors; occurrence/phase/host-error propagation are checked"},
			"not_verified":  []string{"filesystem bytes/mode/truncation/permissions and Unicode write failure", "real CategoryHelper/YAML execution and source helper internals within apply", "real system-message queue deduplication or SSE", "real SQLite config persistence and clone authority", "stdio transport/process lifecycle", "default file reads/settings path freezing", "Python class-vs-instance member ownership flags", "browser rendering and user acceptance"},
		}
		for suffix, value := range map[string]any{"coverage.json": report, "observations.json": observed} {
			raw, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(prefix+suffix, append(raw, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("independent original-source worker differential: %d/68 selected cases passed, %d cases executed, %d steps completed; %d cases / %d steps explicitly not run; controlled host only", passed, executedCases, len(observed), skipped+filteredCases, skippedSteps+filteredSteps)
}

func TestCategoryEditorSourceComparisonRetainsSurrogatesAndNumericKinds(t *testing.T) {
	for _, sample := range []struct {
		a, b  string
		equal bool
	}{
		{`"\ud800"`, `"\ufffd"`, false},
		{`"\ud800"`, `"\uD800"`, true},
		{`"\ud83d\ude00"`, `"😀"`, true},
		{`0`, `0.0`, false},
		{`false`, `0`, false},
		{`{"content":"a\ud800b","enabled":false}`, `{"enabled":false,"content":"a\ud800b"}`, true},
		{`[1,2]`, `[2,1]`, false},
	} {
		if got := sourceJSONEqual(json.RawMessage(sample.a), json.RawMessage(sample.b)); got != sample.equal {
			t.Fatal(fmt.Sprintf("independent comparator changed: %s / %s => %t", sample.a, sample.b, got))
		}
	}
}
