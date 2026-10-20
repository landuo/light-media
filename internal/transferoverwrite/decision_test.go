package transferoverwrite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestInstalledOverwriteDecisionAST(t *testing.T) {
	b, e := os.ReadFile("testdata/source-decision.json")
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Cases []struct {
			Name     string
			Input    Input
			Override *bool
			Expected Decision
		}
	}
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, e := Decide(context.Background(), c.Input, func(_ context.Context, ev Event) (*Event, error) { ev.Overwrite = c.Override; return &ev, nil })
			if e != nil || !reflect.DeepEqual(got, c.Expected) {
				t.Fatalf("got=%+v err=%v want=%+v", got, e, c.Expected)
			}
		})
	}
}
func TestOverwriteMissingAndCancellationAreDependencies(t *testing.T) {
	in := Input{TargetKnown: true, Target: &FileItem{Path: "/owned/a"}}
	if _, e := Decide(context.Background(), in, nil); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Decide(ctx, in, nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestAuditedDispatcherNeverDropsUnknownInstalledHandler(t *testing.T) {
	for id, v := range auditedVersions {
		d, e := AuditedDispatcher([]Installation{{id, v}})
		if e != nil || d == nil {
			t.Fatal(id, e)
		}
	}
	for _, i := range []Installation{{"Unknown", "1"}, {"CategoryEditor", "1.4"}, {"CategoryEditor", ""}} {
		if _, e := AuditedDispatcher([]Installation{i}); !errors.Is(e, ErrDependency) {
			t.Fatal(i, e)
		}
	}
}
func TestLatestIdentityUsesCompleteSourceLabels(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		want, old Identity
		match     bool
	}{
		{Identity{}, Identity{}, true},
		{Identity{Season: "S01", Episode: "E01"}, Identity{Season: "S01", Episode: "E01"}, true},
		{Identity{Season: "S01", Episode: "E01-E02"}, Identity{Season: "S01", Episode: "E01"}, false},
		{Identity{Season: "S00", Episode: "E00"}, Identity{}, false},
		{Identity{Part: ptr("CD1")}, Identity{Part: ptr("CD2")}, false},
		{Identity{Part: ptr("CD1")}, Identity{Part: ptr("")}, true},
		{Identity{Part: ptr("CD1")}, Identity{}, true},
	}
	for _, c := range tests {
		if got := VersionMatches(c.want, c.old); got != c.match {
			t.Fatal(c, got)
		}
	}
}
