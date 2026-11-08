package browserrender

import (
	"bytes"
	"testing"
)

func TestSourceFSValueRawExtraction(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `0`, `0.0`, `true`, `1`, `0.5`, `[]`, `["owned"]`, `{}`, `{"x":"owned"}`, `["login.php"]`, `{"login.php":1}`, `""`, `"owned string"`, `"{\"status\":\"1\"}"`, `NaN`, `Infinity`, `-Infinity`, `1234567890123456789012345678901234567890`, `"\ud800"`, `{"ordered":1,"nan":NaN,"str":"NaN Infinity"}`} {
		t.Run(raw, func(t *testing.T) {
			body := []byte(`{"status":"ok","solution":{"response":` + raw + `}}`)
			outer, e := sourceFSJSONFields(body)
			if e != nil {
				t.Fatal(e)
			}
			inner, e := sourceFSJSONFields(outer["solution"])
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(inner["response"], []byte(raw)) {
				t.Fatalf("raw value lost got=%s expected=%s", inner["response"], raw)
			}
		})
	}
}
