package app

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestManualOrganizeTargetLookupsUsePathIndex(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	const target = "/library/selected.mkv"
	for _, tc := range []struct{ id, path, state string }{
		{"removed", target, "removed"},
		{"superseded", target, "superseded"},
		{"elsewhere", "/library/elsewhere.mkv", "present"},
	} {
		if err := a.Store.Put(ctx, "asset", tc.id, Asset{ID: tc.id, Path: tc.path, State: tc.state}); err != nil {
			t.Fatal(err)
		}
	}
	var got Asset
	if err := a.assetAtPath(ctx, target, &got); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("inactive or unselected asset returned: %+v, %v", got, err)
	}
	if err := a.Store.Put(ctx, "asset", "restored", Asset{ID: "restored", Path: target, State: "external"}); err != nil {
		t.Fatal(err)
	}
	if err := a.assetAtPath(ctx, target, &got); err != nil || got.ID != "restored" {
		t.Fatalf("active exact-path lookup: %+v, %v", got, err)
	}
	for _, tc := range []struct{ query, lookup string }{
		{importAssetAtPathSQL, "asset_path (path=?)"},
		{`SELECT payload` + importReceiptsAtTarget + ` AND state='published'`, "import_receipt_target (target=?)"},
		{`SELECT payload` + importReceiptsAtTarget + ` AND state IN ('staged','published') LIMIT 20`, "import_receipt_target (target=?)"},
	} {
		var id, parent, unused int
		var plan string
		if err := a.Store.reader().QueryRowContext(ctx, "EXPLAIN QUERY PLAN "+tc.query, target).Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "SEARCH ") || !strings.Contains(plan, tc.lookup) {
			t.Fatalf("expected exact-path lookup via %s, got %s", tc.lookup, plan)
		}
	}
}
