package siteaccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"testing"
)

func TestSiteAccessActualSourceObservations(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-access-results.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "9303332121f5265e8881f3330aa785d32d08aa4c2e00054111f41954fadc9fe3" {
		t.Fatal("actual original-source observation changed")
	}
	var source struct {
		Phase  string `json:"phase"`
		Commit string `json:"source_commit"`
		Cases  []struct {
			Name    string          `json:"case"`
			Domain  string          `json:"domain"`
			Seed    json.RawMessage `json:"seed"`
			Before  []Record        `json:"rows_before_actions"`
			Actions []struct {
				Input struct {
					Method  string `json:"method"`
					Time    string `json:"time"`
					Seconds *int64 `json:"seconds"`
				} `json:"input"`
				Outcome struct {
					Returned json.RawMessage `json:"returned"`
					Type     string          `json:"type"`
				} `json:"outcome"`
				ClockCalls int      `json:"clock_calls"`
				After      []Record `json:"rows_after"`
			} `json:"actions"`
		} `json:"cases"`
		Final []Record `json:"rows_final"`
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if source.Phase != "warm" || source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Cases) != 11 || len(source.Final) != 11 {
		t.Fatal("wrong source layer/case count")
	}
	actualCalls := 0
	final := make(map[string]Record, len(source.Cases))
	findDomain := func(t *testing.T, rows []Record, domain string) *Record {
		t.Helper()
		// The capture stores complete database snapshots, not a one-row query.
		// Select only this exact input domain without normalizing any row value.
		var found *Record
		for _, row := range rows {
			if row.Domain == domain {
				if found != nil {
					t.Fatal("owned fixture unexpectedly has duplicate domain rows")
				}
				copy := row.Clone()
				found = &copy
			}
		}
		return found
	}
	for _, sample := range source.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			var current *Record
			observedBefore := findDomain(t, sample.Before, sample.Domain)
			if string(sample.Seed) != "null" {
				// Inputs come from the explicit collector seed, not its after row.
				// Only the real SQLite-assigned prior ID is supplied as identity.
				var seed Record
				if err := json.Unmarshal(sample.Seed, &seed); err != nil {
					t.Fatal(err)
				}
				if observedBefore == nil || observedBefore.ID == nil {
					t.Fatal("missing real seeded row")
				}
				seed.Domain = sample.Domain
				seed.ID = cloneInteger(observedBefore.ID)
				if !reflect.DeepEqual(seed, *observedBefore) {
					t.Fatal("explicit seed differs from original stored input")
				}
				current = &seed
			} else if observedBefore != nil {
				t.Fatal("missing-row scenario had an existing row")
			}
			for index, action := range sample.Actions {
				actualCalls++
				if action.ClockCalls != 1 || action.Outcome.Type != "NoneType" || string(action.Outcome.Returned) != "null" {
					t.Fatal("not a successful normal source method observation", index)
				}
				var before *Record
				if current != nil {
					copy := current.Clone()
					before = &copy
				}
				var got Record
				var err error
				switch action.Input.Method {
				case "success":
					got, err = Success(current, sample.Domain, action.Input.Seconds, action.Input.Time)
				case "fail":
					got, err = Fail(current, sample.Domain, action.Input.Time)
				default:
					t.Fatal("unexpected source method", action.Input.Method)
				}
				if err != nil {
					t.Fatal(index, err)
				}
				if !reflect.DeepEqual(current, before) {
					t.Fatal("transition changed its old row", index)
				}
				observedAfter := findDomain(t, action.After, sample.Domain)
				if observedAfter == nil {
					t.Fatal("source mutation has no matching row", index)
				}
				want := *observedAfter
				if current == nil {
					if got.ID != nil || want.ID == nil {
						t.Fatal("pure algorithm fabricated or source omitted new ID")
					}
					// This is the single declared cross-layer normalization: real
					// SQLite assigned the source ID, while pure code allocates none.
					// No other field is copied from an expected output.
					got.ID = cloneInteger(want.ID)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("source call %d %s mismatch\n got %#v\nwant %#v", index, action.Input.Method, got, want)
				}
				copy := got.Clone()
				current = &copy
			}
			if current == nil {
				t.Fatal("no normal source calls")
			}
			final[sample.Domain] = current.Clone()
		})
	}
	if actualCalls != 19 {
		t.Fatal("normal source call count", actualCalls)
	}
	for _, want := range source.Final {
		if got, ok := final[want.Domain]; !ok || !reflect.DeepEqual(got, want) {
			t.Fatal("complete final source row mismatch", want.Domain)
		}
	}
	// The raw file also retains five real SQLite errors and import/clock
	// provenance. This pure transition gate does not pretend to execute those
	// storage errors, persistence or the separate cold-process observations.
}

// Structural ownership/domain tests only. No generated target expectations are
// an original-source oracle; source differential cases will bind actual capture.
func TestSiteAccessRecordOwnershipAndIntegerBoundary(t *testing.T) {
	id, seconds := int64(41), int64(-3)
	prior := Record{ID: &id, Domain: " HTTPS://Owned.invalid/Exact ", Success: 7, Fail: 2, Seconds: &seconds, LastState: 1, LastModified: "before", Note: map[string]int64{"before": -3}}
	before := prior.Clone()
	out, err := Success(&prior, prior.Domain, nil, "after")
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == nil || *out.ID != id || out.ID == prior.ID || out.Seconds == prior.Seconds {
		t.Fatal("record identity/value pointers were not cloned")
	}
	*out.ID = 900
	*out.Seconds = 123
	out.Note["before"] = 456
	if !reflect.DeepEqual(prior, before) {
		t.Fatal("success output aliased prior record")
	}
	failed, err := Fail(&prior, prior.Domain, "after")
	if err != nil {
		t.Fatal(err)
	}
	failed.Note["before"] = 789
	if !reflect.DeepEqual(prior, before) {
		t.Fatal("fail output aliased prior record")
	}
	for _, operation := range []func(*Record, string, string) (Record, error){func(r *Record, d, at string) (Record, error) { return Success(r, d, nil, at) }, Fail} {
		result, err := operation(&prior, "https://owned.invalid/exact", "after")
		if !errors.Is(err, ErrDomainMismatch) || !reflect.DeepEqual(result, Record{}) || !reflect.DeepEqual(prior, before) {
			t.Fatal("domain mismatch mutated/normalized prior", result, err)
		}
	}
	overflow := prior.Clone()
	overflow.Success = math.MaxInt64
	frozen := overflow.Clone()
	result, err := Success(&overflow, overflow.Domain, &seconds, "after")
	if !errors.Is(err, ErrIntegerRange) || !reflect.DeepEqual(result, Record{}) || !reflect.DeepEqual(overflow, frozen) {
		t.Fatal("success overflow changed prior or wrapped", err)
	}
	overflow.Fail = math.MaxInt64
	frozen = overflow.Clone()
	result, err = Fail(&overflow, overflow.Domain, "after")
	if !errors.Is(err, ErrIntegerRange) || !reflect.DeepEqual(result, Record{}) || !reflect.DeepEqual(overflow, frozen) {
		t.Fatal("fail overflow changed prior or wrapped", err)
	}
	created, err := Fail(nil, "", "fixed timestamp")
	if err != nil || created.ID != nil || created.Seconds != nil || created.Note != nil || created.Domain != "" {
		t.Fatal("new row invented identity/normalization/defaults", created, err)
	}
}
