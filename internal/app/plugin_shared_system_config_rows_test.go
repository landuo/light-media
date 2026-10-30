package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSharedSystemConfigEnvelopeAuthentication(t *testing.T) {
	a, _, p := sharedSystemConfigFixture(t, sharedSystemConfigAll)
	// An ordinary user's JSON object must never be interpreted as our internal
	// source-row envelope merely because it has the same field names.
	raw := `{"rows":[{"id":2,"value":["ordinary"]}]}`
	if _, e := systemConfigSet(t, p, raw); e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(raw))
	snapshot, rows, e := a.readSharedSystemConfigRows(context.Background(), a.Store.DB)
	if e != nil || len(rows) != 1 || rows[0].ID != 1 {
		t.Fatal(rows, e)
	}
	assertSharedOracleJSON(t, rows[0].Value, json.RawMessage(raw))
	before := snapshot.sealed
	_ = systemConfigGet(t, p)
	after, e := readSharedSystemConfigRow(context.Background(), a.Store.DB)
	if e != nil || before != after.sealed {
		t.Fatal("read rewrote legacy fact", e)
	}
	corrupted := []byte(snapshot.sealed)
	corrupted[len(corrupted)-1] ^= 1
	snapshot.sealed = string(corrupted)
	if _, e = a.sharedSystemConfigRows(snapshot); e == nil {
		t.Fatal("authentication failure was reinterpreted as a row envelope")
	}
}
