package artifacts

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRepositoryHistoryKeepsAuthorOrderAndStrictSchema(t *testing.T) {
	raw := []byte(`{"schema_version":1,"plugins":[{"schema_version":1,"plugin_id":"Owned","current_version":"2.0","releases":[],"history":{"2.0":"new","1.0":"old","0.1":"notes only"}}]}`)
	indexes, err := parseRepository(raw)
	if err != nil || len(indexes) != 1 {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(indexes[0])
	if err != nil || bytes.Index(encoded, []byte(`"2.0":`)) > bytes.Index(encoded, []byte(`"1.0":`)) || bytes.Index(encoded, []byte(`"1.0":`)) > bytes.Index(encoded, []byte(`"0.1":`)) {
		t.Fatal("author history was reordered during decode/encode", string(encoded), err)
	}
	indexes[0].History["2.0"] = "edited notes"
	encoded, err = json.Marshal(indexes[0])
	if err != nil || !bytes.Contains(encoded, []byte("edited notes")) {
		t.Fatal("preserved wire bytes overrode changed author data", err)
	}
	for _, invalid := range [][]byte{
		bytes.Replace(raw, []byte(`"plugin_id":"Owned"`), []byte(`"plugin_id":"Owned","unexpected":true`), 1),
		bytes.Replace(raw, []byte(`"2.0":"new"`), []byte(`"2.0":{}`), 1),
		append(raw, []byte(`{}`)...),
	} {
		if _, err := parseRepository(invalid); err == nil {
			t.Fatal("history-order adaptation weakened strict repository decoding")
		}
	}
}
