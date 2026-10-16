package releasemeta

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFrozenParserAndSourceCopyOwnership(t *testing.T) {
	words := []string{"Raw => Cached"}
	platforms := map[string]string{"CUSTOM": "Original"}
	parser, e := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: "legacy", CustomWords: words}, StreamingPlatforms: platforms})
	if e != nil {
		t.Fatal(e)
	}
	first, e := parser.ParseTitle("Raw.S04E01-E04.1080p.WEB-DL.mkv", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	words[0] = "Raw => Changed"
	platforms["CUSTOM"] = "Changed"
	again, e := parser.ParseTitle("Raw.S04E01-E04.1080p.WEB-DL.mkv", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := first.EncodeState()
	b, _ := again.EncodeState()
	if !bytes.Equal(a, b) {
		t.Fatal("caller mutation changed frozen source configuration")
	}
	shallow, deep := first.ShallowCopy(), first.DeepCopy()
	shallow.Set("new instance key", true)
	if first.Has("new instance key") {
		t.Fatal("instance dictionaries alias")
	}
	applied, ok := first.Get("apply_words").([]string)
	if !ok || len(applied) == 0 {
		t.Fatal("source words were not applied")
	}
	applied[0] = "mutated list element"
	if shallow.Get("apply_words").([]string)[0] != "mutated list element" {
		t.Fatal("shallow copy lost Python list sharing")
	}
	if deep.Get("apply_words").([]string)[0] == "mutated list element" {
		t.Fatal("deep copy retained mutable list alias")
	}
}
func TestStatePrecisionAndPresence(t *testing.T) {
	object := EmptyMetaObject()
	object.Kind = "video"
	object.Set("type", "电视剧")
	object.Set("begin_season", json.Number("100000000000000000000001"))
	object.Set("resource_pix", nil)
	object.Set("resource_type", "")
	object.Set("fps", 0)
	encoded, e := object.EncodeState()
	if e != nil {
		t.Fatal(e)
	}
	copy, e := DecodeMetaObjectState(encoded)
	if e != nil {
		t.Fatal(e)
	}
	if !copy.Has("resource_pix") || copy.Has("resource_team") || copy.Get("resource_type") != "" || copy.Number("fps").Sign() != 0 || copy.Season() != "S100000000000000000000001" {
		t.Fatal("state coalesced presence/empty/zero or rounded an integer")
	}
	if _, e := DecodeMetaObjectState(append(encoded, []byte(` {}`)...)); e == nil {
		t.Fatal("accepted trailing state")
	}
}
