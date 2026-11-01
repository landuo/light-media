package downloadercommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

//go:embed testdata/source-tr-numeric.json
var sourceTRNumeric []byte

// Full unedited raw14 supplies values, Python types, outcomes and exact errors.
// No generated target projection replaces the actual original observations.
func TestTRNumericLeafActualSourceObservations(t *testing.T) {
	h := sha256.Sum256(sourceTRNumeric)
	if hex.EncodeToString(h[:]) != "1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d" {
		t.Fatal("not complete original numeric raw14")
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		CaseCount    int    `json:"case_count"`
		Python       struct {
			Version string `json:"version"`
		} `json:"python"`
		Cases []trFinalCase `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(sourceTRNumeric))
	d.UseNumber()
	if err := d.Decode(&oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.CaseCount != 14 || len(oracle.Cases) != 14 || !strings.HasPrefix(oracle.Python.Version, "3.12.14 ") {
		t.Fatal("numeric source scope or actual interpreter changed")
	}
	trueCount, errorCount, calls := 0, 0, 0
	for _, c := range oracle.Cases {
		t.Run(c.ID, func(t *testing.T) {
			if c.Input.Method != "change_torrent" || !c.Input.ClientPresent {
				t.Fatal("not the fixed numeric method case")
			}
			tr, p := trFinalFixture(t)
			p.steps = c.Input.Steps
			x := c.Input.Args
			value, err := tr.ChangeTorrent(context.Background(), x["hash_string"], x["upload_limit"], x["download_limit"], x["ratio_limit"], x["seeding_time_limit"])
			var outcome map[string]any
			if err != nil {
				source, ok := err.(*SourceException)
				if !ok {
					t.Fatal("ordinary spelling became platform boundary", err)
				}
				outcome = map[string]any{"kind": "uncaught_exception", "class": source.Class, "message": source.Message}
				errorCount++
				if p.calls != 0 || len(p.trace) != 0 {
					t.Fatal("try-external conversion error called or logged")
				}
			} else {
				outcome = map[string]any{"kind": "return", "value": value, "python_type": "bool", "python_truthy": value}
				if value {
					trueCount++
				}
			}
			sameJSON(t, "original numeric outcome and error message", outcome, c.Outcome)
			sameJSON(t, "complete ordered numeric call/type trace", p.trace, c.Trace)
			sameJSON(t, "original numeric caller arguments unchanged", x, c.ArgumentsAfter)
			if p.calls != c.Calls || p.calls != len(p.steps) {
				t.Fatal("numeric fixed dependency count", p.calls, c.Calls)
			}
			calls += p.calls
		})
	}
	if trueCount != 3 || errorCount != 11 || calls != 3 {
		t.Fatal("actual raw14 summary differs", trueCount, errorCount, calls)
	}
}

// These are the existing target-domain guards, not invented Python successes.
// Supporting spelling does not enlarge signed-int64 or finite-binary64 scope.
func TestTRNumericRetainsRangeAndNonfiniteBoundaries(t *testing.T) {
	for _, value := range []string{"9_223_372_036_854_775_808", "-9_223_372_036_854_775_809"} {
		_, err := trFinalInt(value)
		var boundary *BoundaryError
		if !errors.As(err, &boundary) {
			t.Fatal("string normalization enlarged int64 range", value, err)
		}
	}
	for _, value := range []any{math.NaN(), math.Inf(1), "NaN", "Inf", "1_0e999"} {
		_, err := trFinalFloat(value)
		var boundary *BoundaryError
		if !errors.As(err, &boundary) {
			t.Fatal("string normalization enlarged finite binary64 range", value, err)
		}
	}
}
