package fileextensions

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func knownInput() Inputs {
	return Inputs{Namespace: "owned.native", SourceID: "source1", EnvironmentKnown: true, ConfigurationKnown: true, Environment: map[string]string{}, Configuration: map[string]json.RawMessage{}}
}

func TestActualOriginalConfigurationAST(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		SourceSHA string `json:"source_sha256"`
		Cases     []struct {
			Key    string          `json:"key"`
			Input  json.RawMessage `json:"input"`
			Values []any           `json:"values"`
			Error  string          `json:"error"`
		} `json:"cases"`
	}
	if e = json.Unmarshal(raw, &corpus); e != nil {
		t.Fatal(e)
	}
	for i, c := range corpus.Cases {
		t.Run(c.Key+"/"+string(rune('A'+i)), func(t *testing.T) {
			in := knownInput()
			in.Configuration[c.Key] = c.Input
			got, e := Resolve(in)
			if c.Error != "" {
				if !errors.Is(e, ErrSourceValidation) {
					t.Fatal("original rejection changed", c.Error, e)
				}
				return
			}
			want := make([]string, 0, len(c.Values))
			for _, v := range c.Values {
				s, ok := v.(string)
				if !ok {
					if !errors.Is(e, ErrUnsupportedMember) {
						t.Fatal("unsupported original member claimed", e)
					}
					return
				}
				want = append(want, s)
			}
			if e != nil || !reflect.DeepEqual(got.Values(c.Key), want) || got.DefaultSourceSHA != corpus.SourceSHA {
				t.Fatal("actual original converter mismatch", got.Values(c.Key), want, e)
			}
		})
	}
}
func TestSourceDefaultsAndPreciseCustomExtensionInputs(t *testing.T) {
	base := knownInput()
	s, e := Resolve(base)
	if e != nil || len(s.Values(MediaKey)) != 18 || len(s.Values(SubtitleKey)) != 4 || len(s.Values(AudioKey)) != 37 || !s.Valid() {
		t.Fatal(s, e)
	}
	for _, field := range Keys() {
		t.Run(field, func(t *testing.T) {
			in := knownInput()
			in.Configuration[field] = json.RawMessage(`[".owned",".UPPER",".owned"]`)
			v, e := Resolve(in)
			if e != nil || !reflect.DeepEqual(v.Values(field), []string{".owned", ".UPPER", ".owned"}) || !v.Contains(field, ".owned") || v.Contains(field, ".upper") {
				t.Fatal("configuration changed source membership/order", v, e)
			}
			in.Environment[field] = `[]`
			v, e = Resolve(in)
			if e != nil || v.Values(field) == nil || len(v.Values(field)) != 0 || v.Fields[field].Origin != "environment" {
				t.Fatal("explicit empty env fell back", v, e)
			}
			delete(in.Environment, field)
			in.Configuration[field] = json.RawMessage(`"[\".second\"]"`)
			v, e = Resolve(in)
			if e != nil || !reflect.DeepEqual(v.Values(field), []string{".second"}) {
				t.Fatal("source JSON list string rejected", v, e)
			}
			in.Configuration[field] = json.RawMessage(`null`)
			if _, e = Resolve(in); !errors.Is(e, ErrSourceValidation) {
				t.Fatal("null became defaults", e)
			}
			in.Configuration[field] = json.RawMessage(`[".owned",42]`)
			if _, e = Resolve(in); !errors.Is(e, ErrUnsupportedMember) {
				t.Fatal("unsupported original list member claimed success", e)
			}
		})
	}
}
func TestUnknownOriginalSourceNeverReadsAnotherEnvironment(t *testing.T) {
	in := knownInput()
	native, _ := Resolve(in)
	in.Namespace = "moviepilot.source"
	source, e := Resolve(in)
	if e != nil || source.InputSHA == native.InputSHA {
		t.Fatal("different source origin identities were collapsed", e)
	}
	in.EnvironmentKnown = false
	if _, e = Resolve(in); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	in.EnvironmentKnown = true
	in.ConfigurationKnown = false
	if _, e = Resolve(in); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	native.Fields[MediaKey] = Field{Origin: "environment", Values: []string{".forged"}}
	if native.Valid() {
		t.Fatal("snapshot values need actual input derivation")
	}
}
