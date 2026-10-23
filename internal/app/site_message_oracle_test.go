package app

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/notice"
	"light-media/internal/sites"
)

type siteMessageOracleCase struct {
	Input map[string]json.RawMessage    `json:"input"`
	Scope struct{ Kind, Reason string } `json:"schema_scope"`
	Trace []map[string]json.RawMessage  `json:"trace"`
	Error *struct {
		Class string `json:"class"`
	} `json:"error"`
}

type siteMessageOracleOperation struct {
	Op    string
	Value any
}

// This is deliberately a host-contract comparison, not another SiteChain:
// production creates its real ordinal plans and constructs each current notice.
// Only the oracle's declared synthetic post/history host is reproduced below.
// Real App HTTP/batch/clone/recovery behavior is tested in site_message_chain_test.
func TestSiteMessagePinnedASTEveryCaseProductionSequence(t *testing.T) {
	path := os.Getenv("LIGHT_MEDIA_OWNED_SITE_MESSAGE_ORACLE")
	if path == "" {
		path = "../../testdata/plugins/site-message-original-oracle.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Source struct {
			SHA    string `json:"sha256"`
			Commit string `json:"commit"`
		} `json:"source"`
		Cases []siteMessageOracleCase `json:"cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.Source.SHA != "06358a8fd070d46ab254015a2f8c7a34f07c35dd240bd2b3fef38cbf219252fa" || fixture.Source.Commit != "1528176beacc0f9f8d8d9cbf1adf048b0da67b5c" {
		t.Fatal("oracle provenance differs")
	}
	seen := map[string]bool{}
	compared, invalid, coercion, module := 0, 0, 0, 0
	for _, item := range fixture.Cases {
		var name string
		if json.Unmarshal(item.Input["name"], &name) != nil || seen[name] {
			t.Fatal("missing/duplicate oracle case", name)
		}
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			kind, reason := item.Scope.Kind, item.Scope.Reason
			// Old 56-case files have no scope labels. Enumerate rather than silently
			// treating their unvalidated DTOs as real schema evidence.
			if kind == "" {
				kind = "schema_representable"
				switch name {
				case "ratio-'bad'":
					kind = "invalid_schema"
					reason = "Optional[float] rejects bad before SiteChain"
				case "ratio-'0'", "ratio-'0.5'":
					kind = "schema_coercion_not_applied"
					reason = "numeric-string ratio normalized by true schema, not oracle host"
				case "module-raises":
					kind = "module_exception"
					reason = "synthetic module exception, not a DTO"
				}
			}
			switch kind {
			case "invalid_schema":
				invalid++
				if name != "ratio-'bad'" || reason == "" || item.Error == nil || item.Error.Class != "ValueError" {
					t.Fatal("unexpected schema rejection coverage", name, reason)
				}
				t.Log("Not a true-schema DTO input:", reason)
				return
			case "schema_coercion_not_applied":
				coercion++
				if name != "ratio-'0'" && name != "ratio-'0.5'" {
					t.Fatal("unexpected coercion case", name)
				}
				if reason == "" {
					t.Fatal("coercion boundary lacks reason")
				}
				// Accepted schema inputs are checked after float normalization, but never
				// falsely compared with an oracle that retained a truthy string '0'.
				ratio := 0.0
				if name == "ratio-'0.5'" {
					ratio = .5
				}
				plans := siteMessageBusinesses("Synthetic a", "https://synthetic-a.invalid/", siteSnapshot{SourceData: &sites.SourceUserData{Ratio: ratio}})
				want := 0
				if ratio != 0 {
					want = 1
				}
				if len(plans) != want {
					t.Fatal("normalized ratio branch differs", len(plans), want)
				}
				if want == 1 {
					business, e := siteMessageBusinessAt("Synthetic a", "https://synthetic-a.invalid/", siteSnapshot{SourceData: &sites.SourceUserData{Ratio: ratio}}, plans[0])
					if e != nil {
						t.Fatal(e)
					}
					var payload sourceBusinessNotice
					if json.Unmarshal(business, &payload) != nil || payload.Envelope["text"] != "站点 Synthetic a 分享率 0.5，请注意！" {
						t.Fatal("normalized float text differs")
					}
				}
				t.Log("Separate normalized-schema check, not raw oracle equivalence:", reason)
				return
			case "module_exception":
				module++
				if name != "module-raises" || item.Error == nil || item.Error.Class != "SyntheticPostError" {
					t.Fatal("module fixture differs")
				}
				for _, event := range item.Trace {
					if oracleText(event["op"]) != "run_module" {
						t.Fatal("throwing synthetic module published a fact")
					}
				}
				t.Log("Synthetic module outcome boundary; not a production module-error proof")
				return
			case "schema_representable":
				compared++
			default:
				t.Fatal("unaccounted oracle scope", kind)
			}
			compareSiteMessageOracleProduction(t, item)
		})
	}
	// Every historical case is named, so deletion or selective cherry-picking is
	// a test failure even if the remaining cases happen to pass.
	names := []string{"unread-0", "unread-None", "unread-False", "unread--1", "unread-1", "unread-3", "summary-return-None", "summary-return-False", "summary-return-True", "three-items", "four-items", "more-items", "null-fields", "false-source", "null-source", "same-no-source", "same-source", "different-source", "malformed-short", "existing-source", "ratio-None", "ratio-False", "ratio-0", "ratio--0.5", "ratio-0.5", "ratio-1", "ratio-1.5", "ratio-'0'", "ratio-'0.5'", "ratio-'bad'", "level-None", "level-''", "level-'User'", "level-'VIP'", "level-'vip'", "level-'VI'", "level-'V'", "level-'贵宾'", "level-'preVIpost'", "post-error-1-after-False", "post-error-1-after-True", "post-error-2-after-False", "post-error-2-after-True", "post-error-3-after-False", "post-error-3-after-True", "lookup-error", "no-dto", "module-raises", "batch-normal", "batch-none", "batch-partial", "batch-post-error", "batch-second-post-error", "batch-stopped", "batch-stop-after-post", "batch-empty"}
	for _, name := range names {
		if !seen[name] {
			t.Error("historical oracle case missing", name)
		}
	}
	for _, name := range []string{"level-'Vİ'", "level-'Vı'", "later-malformed-short", "later-null-row", "later-number-row", "string-row-iterable", "dict-row-iterable"} {
		if !seen[name] {
			t.Error("extended source case missing", name)
		}
	}
	if len(seen) != 63 || invalid != 1 || coercion != 2 || module != 1 || compared != 59 {
		t.Fatalf("oracle accounting changed: total=%d compared=%d invalid=%d coercion=%d module=%d", len(seen), compared, invalid, coercion, module)
	}
}

func oracleText(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func oracleValue(raw json.RawMessage) any {
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}
func oracleInt(raw json.RawMessage) int   { var n int; _ = json.Unmarshal(raw, &n); return n }
func oracleBool(raw json.RawMessage) bool { var b bool; _ = json.Unmarshal(raw, &b); return b }
func oracleNotice(raw json.RawMessage) notice.Object {
	var fields notice.Object
	_ = json.Unmarshal(raw, &fields)
	if fields["mtype"] == "SiteMessage" {
		fields["mtype"] = "站点"
	}
	// The stub records only explicitly passed kwargs; JSON nil source is still
	// semantically None, and production plans may represent it explicitly.
	if fields["source"] == nil {
		delete(fields, "source")
	}
	return fields
}

func compareSiteMessageOracleProduction(t *testing.T, item siteMessageOracleCase) {
	t.Helper()
	expected := []siteMessageOracleOperation{}
	for _, row := range item.Trace {
		op := oracleText(row["op"])
		switch op {
		case "exists_by_source":
			expected = append(expected, siteMessageOracleOperation{op, oracleValue(row["source"])})
		case "post_call", "host_history_stub":
			expected = append(expected, siteMessageOracleOperation{op, oracleNotice(row["kwargs"])})
		case "post_return":
			expected = append(expected, siteMessageOracleOperation{op, oracleValue(row["value"])})
		}
	}
	// DTO snapshots are taken from actual source save trace, including source
	// defaults. Their order represents exactly what the extracted source reached;
	// this comparison does not pretend to independently implement its batch loop.
	actual := []siteMessageOracleOperation{}
	history := []any{}
	_ = json.Unmarshal(item.Input["history"], &history)
	posts, lookups := 0, 0
	errorClass := ""
	reachedSaves := 0
	for _, row := range item.Trace {
		if oracleText(row["op"]) != "save_userdata" {
			continue
		}
		reachedSaves++
		var dtoFields map[string]json.RawMessage
		if json.Unmarshal(row["payload"], &dtoFields) != nil {
			t.Fatal("source save payload invalid")
		}
		// Pydantic accepts bool for numeric fields; false and null exercise the
		// source false branch. Preserve arbitrary message rows without coercing them.
		for _, key := range []string{"message_unread", "ratio"} {
			if string(dtoFields[key]) == "false" || string(dtoFields[key]) == "null" {
				dtoFields[key] = json.RawMessage(`0`)
			}
		}
		raw, e := json.Marshal(dtoFields)
		if e != nil {
			t.Fatal(e)
		}
		var dto sites.SourceUserData
		if e = json.Unmarshal(raw, &dto); e != nil {
			t.Fatal("representable source DTO rejected before its ordinal", e)
		}
		name := oracleText(row["name"])
		domain := oracleText(row["domain"])
		snapshot := siteSnapshot{SourceData: &dto}
		plans := siteMessageBusinesses(name, "https://"+domain+"/", snapshot)
		for _, step := range plans {
			business, e := siteMessageBusinessAt(name, "https://"+domain+"/", snapshot, step)
			if e != nil {
				errorClass = sourceNoticeError(e)
				break
			}
			var payload sourceBusinessNotice
			if json.Unmarshal(business, &payload) != nil {
				t.Fatal("production business invalid")
			}
			fields := payload.Envelope
			if fields["source"] == nil {
				delete(fields, "source")
			}
			source := fields["source"]
			if sourceSubscriptionTruth(source) {
				lookups++
				actual = append(actual, siteMessageOracleOperation{"exists_by_source", source})
				if lookups == oracleInt(item.Input["lookup_error_at"]) {
					errorClass = "SyntheticLookupError"
					break
				}
				exists := false
				for _, old := range history {
					if reflect.DeepEqual(old, source) {
						exists = true
					}
				}
				if exists {
					continue
				}
			}
			posts++
			actual = append(actual, siteMessageOracleOperation{"post_call", fields})
			if posts == oracleInt(item.Input["post_error_at"]) && !oracleBool(item.Input["error_after_history"]) {
				errorClass = "SyntheticPostError"
				break
			}
			simulateHistory := true
			if value, ok := item.Input["simulate_history"]; ok {
				simulateHistory = oracleBool(value)
			}
			if simulateHistory {
				actual = append(actual, siteMessageOracleOperation{"host_history_stub", fields})
				if sourceSubscriptionTruth(source) {
					history = append(history, source)
				}
			}
			if posts == oracleInt(item.Input["post_error_at"]) {
				errorClass = "SyntheticPostError"
				break
			}
			actual = append(actual, siteMessageOracleOperation{"post_return", oracleValue(item.Input["post_return"])})
		}
		if errorClass != "" {
			break
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		a, _ := json.MarshalIndent(actual, "", "  ")
		b, _ := json.MarshalIndent(expected, "", "  ")
		t.Fatalf("production ordinal/source-host sequence differs\nactual %s\nsource %s", a, b)
	}
	wantError := ""
	if item.Error != nil {
		wantError = item.Error.Class
	}
	if errorClass != wantError {
		t.Fatal("source exception boundary differs", errorClass, wantError)
	}
	// Read source events as evidence of the oracle's scope, not as fake Go events.
	// Assert save precedes its single event and any notice in that same segment.
	saved, eventSeen := false, false
	for _, row := range item.Trace {
		switch oracleText(row["op"]) {
		case "run_module":
			saved, eventSeen = false, false
		case "save_userdata":
			saved = true
		case "event":
			var data map[string]any
			_ = json.Unmarshal(row["data"], &data)
			if data["site_id"] != "*" {
				if !saved {
					t.Fatal("oracle event before save")
				}
				eventSeen = true
			}
		case "post_call":
			if !saved || !eventSeen {
				t.Fatal("oracle post before save/event")
			}
		}
	}
	name := oracleText(item.Input["name"])
	if strings.HasPrefix(name, "batch-") {
		t.Logf("Compared every reached production ordinal for batch trace (%d saves); App batch/stop/event behavior has separate real-App tests", reachedSaves)
	}
	if len(actual) == 0 {
		t.Log(fmt.Sprintf("Compared empty production sequence; source reached %d DTO saves", reachedSaves))
	}
}
