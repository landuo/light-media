package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestSharedQBittorrentCompletedInherited28Oracle(t *testing.T) {
	// Inherit the already executed installed-source oracle. This is target
	// regression against its exact source binding, NOT a new source run.
	raw, err := os.ReadFile("../plugins/iyuuautoseed/reference/native-projection-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Sources map[string]string `json:"sources"`
		Cases   []struct {
			Input  json.RawMessage `json:"input"`
			Result struct {
				Value     json.RawMessage `json:"value"`
				Exception string          `json:"exception"`
			} `json:"result"`
		} `json:"qb_completed"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 28 || oracle.Sources["qb.py"] != "114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0" {
		t.Fatal("old source oracle binding changed")
	}
	source, err := os.ReadFile("../../testdata/plugins/clean-invalid-seed-mutation-phase/installed-qb.py")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	if hex.EncodeToString(digest[:]) != oracle.Sources["qb.py"] {
		t.Fatal("installed source bytes differ")
	}
	for i, row := range oracle.Cases {
		got, caught, err := sharedQBittorrentCompletedRows(context.Background(), append(append([]byte{'['}, row.Input...), ']'), nil, func(context.Context) error { return nil })
		if row.Result.Exception != "" {
			var sourceError *APIError
			if caught || !errors.As(err, &sourceError) || sourceError.Code != "QB_COMPLETED_SOURCE_EXCEPTION" || sourceError.Message != row.Result.Exception {
				t.Fatalf("case %d: outside-try source exception class changed", i)
			}
			continue
		}
		if err != nil || caught {
			t.Fatalf("case %d: source ordinary failure %v", i, err)
		}
		var want []map[string]json.RawMessage
		if json.Unmarshal(row.Result.Value, &want) != nil {
			t.Fatal("invalid old oracle")
		}
		if len(got) != len(want) {
			t.Fatalf("case %d: source count changed", i)
		}
		for j := range want {
			if !reflect.DeepEqual(got[j].Fields, want[j]) {
				t.Fatalf("case %d: raw fields changed", i)
			}
		}
	}
}

func TestSharedQBittorrentCompletedSourcePhaseOrder(t *testing.T) {
	for _, row := range []struct {
		name, rows, tags  string
		count             int
		caught, exception bool
	}{
		{"null_row_no_tags", `[null]`, `null`, 0, false, true},
		{"null_row_tags", `[null]`, `"a"`, 0, true, false},
		{"later_tags_error_beats_nonmapping_completion", `[null,{"tags":null}]`, `"a"`, 0, true, false},
		{"unowned_complete", `[{"progress":1,"amount_left":0,"state":"queuedUP","extra":9007199254740993}]`, `null`, 1, false, false},
		{"unfinished_bad_tags", `[{"progress":0,"tags":null}]`, `"tag"`, 0, true, false},
		{"later_bad_tags_beats_earlier_bad_progress", `[{"progress":"1","tags":"tag"},{"progress":0,"tags":null}]`, `"tag"`, 0, true, false},
		{"no_tags_no_read", `[{"progress":1,"tags":null}]`, `[]`, 1, false, false},
		{"requested_not_trimmed", `[{"progress":1,"tags":" a , b "}]`, `"a, b"`, 0, false, false},
		{"row_tags_python_strip", `[{"progress":1,"tags":"\u001ca\u001c,b"}]`, `["a","b"]`, 1, false, false},
		{"empty_string_not_empty_list", `[{"progress":1,"tags":""}]`, `[""]`, 1, false, false},
		{"hashable_nonstring_not_stringified", `[{"progress":1,"tags":"1,None,True"}]`, `[1,null,true]`, 0, false, false},
		{"unhashable_tag", `[{"progress":1,"tags":"a"}]`, `[["a"]]`, 0, true, false},
		{"unhashable_tag_empty_rows", `[]`, `[["a"]]`, 0, false, false},
		{"predicate_exception", `[{"progress":"1"}]`, `null`, 0, false, true},
		{"short_circuit_amount", `[{"progress":0,"amount_left":"bad"}]`, `null`, 0, false, false},
		{"python_binary64", `[{"progress":0.99999999999999999,"amount_left":0}]`, `null`, 1, false, false},
		{"truthy_nonstring_state", `[{"progress":1,"state":{"b":2,"a":1}}]`, `null`, 1, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			request, err := decodeSharedQBittorrentCompleted(json.RawMessage(`{"downloader_id":"owned","tags":` + row.tags + `}`))
			if err != nil {
				t.Fatal(err)
			}
			got, caught, err := sharedQBittorrentCompletedRows(context.Background(), json.RawMessage(row.rows), request.tags, func(context.Context) error { return nil })
			if (err != nil) != row.exception || caught != row.caught || len(got) != row.count {
				t.Fatal("source phase/value semantics changed", len(got), caught, err)
			}
			if row.exception {
				want := "TypeError"
				if row.name == "null_row_no_tags" {
					want = "AttributeError"
				}
				var sourceError *APIError
				if !errors.As(err, &sourceError) || sourceError.Code != "QB_COMPLETED_SOURCE_EXCEPTION" || sourceError.Message != want {
					t.Fatal("source exception replaced by unrelated platform error")
				}
			}
		})
	}
}

func TestSharedQBittorrentCompletedSelectorUnion(t *testing.T) {
	for _, row := range []struct {
		ids  string
		want *string
	}{
		{`null`, nil}, {`[]`, qbCompletedString("")}, {`""`, qbCompletedString("")}, {`"B|a|B"`, qbCompletedString("B|a|B")},
		{`["B","a","B"]`, qbCompletedString("B|a|B")}, {`[null,true,1,1.0,{"b":2,"a":1},["x"]]`, qbCompletedString("None|True|1|1.0|{'b': 2, 'a': 1}|['x']")},
	} {
		got, err := decodeSharedQBittorrentCompleted(json.RawMessage(`{"downloader_id":"owned","ids":` + row.ids + `}`))
		if err != nil || !reflect.DeepEqual(got.hashes, row.want) {
			t.Fatal("ids source union changed", row.ids, err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"downloader_id":"owned","ids":true}`, `{"downloader_id":"owned","ids":{}}`, `{"downloader_id":"owned","tags":1}`, `{"downloader_id":"owned","downloader_id":"other"}`, `{"downloader_id":"owned","\u0069ds":[],"ids":[]}`, `{"downloader_id":"owned","tags":"\ud800"}`, `{"downloader_id":"owned","rpc":"torrent-delete"}`} {
		if _, err := decodeSharedQBittorrentCompleted(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid finite wire accepted")
		}
	}
}
func qbCompletedString(s string) *string { return &s }
