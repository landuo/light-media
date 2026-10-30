package artifacts

import (
	"encoding/json"
	"strings"
	"testing"
)

// Cases are pinned to the complete helper/manager/router observations in the
// independent recovery-market-remaining source oracle (8a83efe), not generated
// from the target implementation. Auth filtering is deliberately excluded.
func TestPythonDisplayPipelineDictionaryAndExactKeys(t *testing.T) {
	body := `{"Owned":{"name":"first","v2":true},"owned":{"name":"case sibling","v2":true},"Owned":{"name":null,"Name":"ignored","VERSION":"ignored","v2":true,"author_url":123},"schema_version":{"name":"schema card","v2":true},"plugins":[],"Filtered":{"v2":1}}`
	rows, err := discoverPythonMetadataForVersion([]byte(body), "owned-display", true)
	if err != nil || len(rows) != 3 {
		t.Fatal(len(rows), err)
	}
	for i, id := range []string{"Owned", "owned", "schema_version"} {
		if rows[i].PluginID != id || rows[i].AddTime != 5-i {
			t.Fatalf("dictionary order/add_time: %#v", rows)
		}
	}
	for _, field := range []string{"name", "version", "author_url"} {
		if string(rows[0].SourceDisplay.CardValues()[field]) != "null" {
			t.Fatal(field, rows[0].SourceDisplay.CardValues())
		}
	}
	scalar, err := discoverPythonMetadataForVersion([]byte(`{"Owned":{"v2":true},"Sibling":{"v2":true},"Owned":1}`), "owned-display", true)
	if err != nil || len(scalar) != 1 || scalar[0].PluginID != "Sibling" || scalar[0].AddTime != 1 {
		t.Fatal(scalar, err)
	}
}

func TestPythonDisplayPipelineNonExecutableIdentityAndCount(t *testing.T) {
	var body strings.Builder
	body.WriteByte('{')
	for i := 0; i < 1025; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		key, _ := json.Marshal(strings.Repeat("x", i+1))
		body.Write(key)
		body.WriteString(`:{"v2":true}`)
	}
	body.WriteByte('}')
	rows, err := discoverPythonMetadataForVersion([]byte(body.String()), "owned-display", true)
	if err != nil || len(rows) != 1025 {
		t.Fatal(len(rows), err)
	}
	for _, row := range rows {
		if row.SourceDisplay == nil || row.Current.Manifest.Runtime != "python" || len(row.Releases) != 0 || row.Current.HostContract != "" || row.UnavailableReason == "" {
			t.Fatal("display gained executable metadata")
		}
	}
}

func TestPythonDisplayPipelineConstantsAndIntegerBudget(t *testing.T) {
	for _, raw := range []string{`{NaN:{"v2":true}}`, `{Infinity : {}}`, `{"Owned":{"v2":true,"name":{-Infinity:1}}}`} {
		if _, err := discoverPythonMetadataForVersion([]byte(raw), "owned-display", true); err == nil {
			t.Fatal("unquoted constant property repaired", raw)
		}
	}
	rows, err := discoverPythonMetadataForVersion([]byte(`{"Owned":{"v2":true,"name":NaN,"version":NaN,"description":[Infinity,-Infinity,NaN],"key":123,"level":true,"release":[NaN],"system_version":false,"labels":[NaN,Infinity,-Infinity]},"Sibling":{"v2":true}}`), "owned-display", true)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	display := rows[0].SourceDisplay
	if display.VersionKey != "nan" {
		t.Fatal(display.VersionKey)
	}
	values := display.CardValues()
	for key, want := range map[string]string{"name": "null", "version": "null", "description": "[null,null,null]", "plugin_public_key": "123", "auth_level": "1", "release": "true", "system_version": "null", "plugin_label": `"nan inf -inf"`} {
		if string(values[key]) != want {
			t.Fatal(key, string(values[key]), want)
		}
	}
	if key, err := pythonDisplayVersionKey(json.RawMessage(`[NaN,{"x":Infinity},"NaN"]`)); err != nil || key != `[nan, {'x': inf}, 'NaN']` {
		t.Fatal(key, err)
	}
	for _, digits := range []int{4300, 4301} {
		_, err := discoverPythonMetadataForVersion([]byte(`{"Owned":{"v2":true,"name":`+strings.Repeat("1", digits)+`}}`), "owned-display", true)
		if (err != nil) != (digits == 4301) {
			t.Fatal(digits, err)
		}
	}
}

func TestPythonDisplayPipelineSurvivingLabelAndIDUnicode(t *testing.T) {
	for _, body := range []string{`{"Owned":{"v2":true,"labels":"A\ud800"}}`, `{"Owned":{"v2":true,"labels":["A\ud800"]}}`, `{"\ud800":{"v2":true}}`} {
		rows, err := discoverPythonMetadataForVersion([]byte(body), "owned-display", true)
		if err != nil || len(rows) != 1 || rows[0].SourceDisplay.WireError() == nil {
			t.Fatal(body, rows, err)
		}
	}
	for _, body := range []string{`{"Owned":{"v2":true,"labels":"A\ud800","labels":"good"}}`, `{"Owned":{"v2":true,"labels":[["A\ud800"]]}}`} {
		rows, err := discoverPythonMetadataForVersion([]byte(body), "owned-display", true)
		if err != nil || len(rows) != 1 || rows[0].SourceDisplay.WireError() != nil {
			t.Fatal(body, rows, err)
		}
	}
}
