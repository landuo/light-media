package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// This is the database selection/null/error boundary gate, not the native
// producer proof. Its independent source observations come from the actual
// source Oper and SQLAlchemy with isolated SQLite, never target-generated rows.
func TestSharedHistoryRealSourceQueryOracle(t *testing.T) {
	path := os.Getenv("HISTORY_SOURCE_QUERY_OBSERVATIONS")
	if path == "" {
		t.Fatal("HISTORY_SOURCE_QUERY_OBSERVATIONS must name fresh real source ORM output")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Observations []struct {
			Case, Family, Method string
			Arguments            json.RawMessage
			Returned             json.RawMessage
			Error                json.RawMessage
			Database             map[string][]map[string]json.RawMessage
		}
		NetworkAttempts []any `json:"network_attempts"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.NetworkAttempts) != 0 || len(oracle.Observations) == 0 {
		t.Fatal("invalid source oracle")
	}
	a, _, object := ownedHistorySDKFixture(t, true)
	rows := oracle.Observations[0].Database["transferhistory"]
	for index, original := range rows {
		cloned := map[string]json.RawMessage{}
		for key, value := range original {
			cloned[key] = value
		}
		id := fmt.Sprintf("source-history-%d", index+1)
		cloned["id"], _ = json.Marshal(id)
		for _, key := range []string{"src_fileitem", "dest_fileitem", "files"} {
			var text string
			if json.Unmarshal(cloned[key], &text) == nil {
				cloned[key] = json.RawMessage(text)
			}
		}
		var boolean bool
		if string(cloned["status"]) == "1" {
			boolean = true
		}
		cloned["status"], _ = json.Marshal(boolean)
		encoded, _ := json.Marshal(cloned)
		var row plugins.TransferHistoryRecord
		if err = json.Unmarshal(encoded, &row); err != nil {
			t.Fatal(err)
		}
		file := PlanFile{ID: id, Status: "imported"}
		if !boolean {
			file.Status = "error"
		}
		if row.Source != nil {
			file.Source = *row.Source
		}
		if row.Destination != nil {
			file.Target = *row.Destination
		}
		// A nullable source is distinct from a native empty path. The frozen
		// source-model field, rather than a lossy PlanFile string, drives lookup.
		file.HistorySource = "oracle-source-" + id
		plan := ImportPlan{ID: "oracle-plan-" + id, Files: []PlanFile{file}, CreatedAt: time.Date(2025, 1, index+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)}
		metadata := importMetadata{SharedHistory: map[string]sharedTransferHistorySnapshot{id: {Row: row, Final: true}}}
		tx, err := a.Store.DB.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = persistSharedHistoryPlan(context.Background(), tx, plan, metadata); err != nil {
			t.Fatal(err)
		}
		job, err := enqueueTx(context.Background(), tx, "import", map[string]any{"id": plan.ID}, "oracle-fixture")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`UPDATE jobs SET state='succeeded',created_at=? WHERE id=?`, plan.CreatedAt, job.ID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	checked := 0
	for _, observation := range oracle.Observations {
		if observation.Family != "transfer" || observation.Method == "delete" || observation.Case == "after-delete" {
			continue
		}
		t.Run(observation.Case, func(t *testing.T) {
			method := plugins.TransferHistoryBySourceCapability
			if observation.Method == "get_by_dest" {
				method = plugins.TransferHistoryByDestinationCapability
			}
			call, _ := json.Marshal(map[string]any{"method": method, "payload": observation.Arguments})
			result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", call, nil)
			if len(observation.Error) > 0 {
				if err == nil {
					t.Fatal("source invalid bound operand accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var want, got map[string]any
			if err = json.Unmarshal(observation.Returned, &want); err != nil {
				t.Fatal(err)
			}
			if want != nil {
				want["id"] = fmt.Sprintf("source-history-%.0f", want["id"])
			}
			if err = json.Unmarshal(result.Result, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("source differential mismatch\nwant %s\ngot %s", mustHistoryJSON(want), mustHistoryJSON(got))
			}
		})
		checked++
	}
	if checked != 30 {
		t.Fatalf("unexpected case count %d", checked)
	}
	t.Logf("compared %d source query cases (all columns, nullable exact paths, storage truthiness, duplicate destination, invalid-list transport rejection)", checked)
}

func mustHistoryJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

func TestSharedHistorySQLiteFailurePropagates(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	if err := a.Store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	call := json.RawMessage(`{"method":"history.transfer.get_by_src","payload":{"src":"/owned/missing"}}`)
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", call, nil)
	if err == nil || strings.TrimSpace(string(result.Result)) == "null" {
		t.Fatalf("SQLite failure converted to missing row: %s %v", result.Result, err)
	}
}
