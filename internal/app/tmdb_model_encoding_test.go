package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"light-media/internal/catalog"
)

func TestTMDBModelEncodingPreservesProviderValues(t *testing.T) {
	// Plugin MediaInfo carries the original provider payload, including extras.
	// Large IDs and decimal lexemes must survive every native projection.
	const extra = `[null,{},[],-0.00,1.2300e+04,9999999999999999999999999]`
	raw := json.RawMessage(`{"id":123,"name":"Fallout","first_air_date":"2024-04-10","provider_extension":` + extra + `}`)
	constructor, err := sourceTMDBMediaModel(raw, "tv", "", "")
	if err != nil {
		t.Fatal(err)
	}
	localized, err := sourceTMDBMediaInfoLocale(raw, "tv", "", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := importTestApp(t)
	derived, err := a.originalRecognitionProjection(catalog.Media{MediaType: "tv", ID: 123}, localized)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []json.RawMessage{constructor, localized, derived.MediaInfo} {
		var fields struct {
			Provider struct {
				Extra json.RawMessage `json:"provider_extension"`
			} `json:"tmdb_info"`
		}
		if err := json.Unmarshal(model, &fields); err != nil || !bytes.Equal(fields.Provider.Extra, []byte(extra)) {
			t.Fatalf("provider extension changed: %s, %v", fields.Provider.Extra, err)
		}
	}
	var provider struct {
		Extra json.RawMessage `json:"provider_extension"`
	}
	if err := json.Unmarshal(derived.ProviderInfo, &provider); err != nil || !bytes.Equal(provider.Extra, []byte(extra)) {
		t.Fatalf("provider payload changed: %s, %v", provider.Extra, err)
	}
}
