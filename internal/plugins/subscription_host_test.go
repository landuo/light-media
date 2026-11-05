package plugins

import (
	"encoding/json"
	"testing"
)

func TestSubscriptionHostPatchNullOmittedAndUnknownColumns(t *testing.T) {
	var p SubscriptionHostPatchV1
	if err := json.Unmarshal([]byte(`{"sites":null,"keyword":null}`), &p); err != nil || len(p.NullFields) != 2 || p.Sites != nil {
		t.Fatal("null presence not retained", err, p)
	}
	raw, err := json.Marshal(p)
	if err != nil || string(raw) != `{"keyword":null,"sites":null}` {
		t.Fatal("explicit null not retransmitted", err, string(raw))
	}
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil || len(p.NullFields) != 0 {
		t.Fatal("omitted fields became null", err, p)
	}
	for _, raw := range []string{`{"unknown":null}`, `null`, `{"sites":{}}`, `{"keyword":false}`} {
		if err := json.Unmarshal([]byte(raw), &p); err == nil {
			t.Fatal("invalid or unknown patch accepted", raw)
		}
	}
	if _, err := json.Marshal(SubscriptionHostPatchV1{NullFields: []string{"AI_MODELS"}}); err == nil {
		t.Fatal("nullable vocabulary widened")
	}
}
