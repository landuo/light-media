package fileextensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestInstalledParserExtensionUnion(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Input    map[string][]string
			Expected []string
		}
	}
	raw, e := os.ReadFile("testdata/parser-union-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for i, tc := range oracle.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			in := Inputs{Namespace: "oracle", SourceID: "actual-parser-union", EnvironmentKnown: true, ConfigurationKnown: true, Configuration: map[string]json.RawMessage{}}
			for key, name := range map[string]string{MediaKey: "media", SubtitleKey: "subtitle", AudioKey: "audio"} {
				in.Configuration[key], _ = json.Marshal(tc.Input[name])
			}
			snapshot, e := Resolve(in)
			if e != nil {
				t.Fatal(e)
			}
			got, e := ParserMediaExtensions(snapshot)
			if e != nil || !reflect.DeepEqual(got, tc.Expected) {
				t.Fatal(got, tc.Expected, e)
			}
			if len(got) > 0 {
				got[0] = "caller-mutated"
				again, e := ParserMediaExtensions(snapshot)
				if e != nil || !reflect.DeepEqual(again, tc.Expected) {
					t.Fatal("caller mutated input frame", again, e)
				}
			}
		})
	}
}
func TestParserExtensionUnknownRemainsDependency(t *testing.T) {
	if _, e := ParserMediaExtensions(Snapshot{}); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	s, e := Resolve(Inputs{Namespace: "native", SourceID: "declared", EnvironmentKnown: true, ConfigurationKnown: true})
	if e != nil {
		t.Fatal(e)
	}
	got, e := ParserMediaExtensions(s)
	if e != nil || len(got) != 59 {
		t.Fatal("actual declared18+4+37 defaults", len(got), e)
	}
}
