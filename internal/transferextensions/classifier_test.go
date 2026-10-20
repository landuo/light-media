package transferextensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"light-media/internal/fileextensions"
)

func TestInstalledClassificationMethods(t *testing.T) {
	var oracle struct {
		Configs []map[string][]string `json:"configs"`
		Cases   []struct {
			Config    int
			Extension *string
			Type      string
			Bluray    bool
			Expected  map[string]bool
		} `json:"cases"`
		Paths []struct{ Name, Extension string } `json:"path_extensions"`
	}
	raw, e := os.ReadFile("testdata/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for i, tc := range oracle.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			cfg := oracle.Configs[tc.Config]
			in := fileextensions.Inputs{Namespace: "oracle", SourceID: "selected-source-methods", EnvironmentKnown: true, ConfigurationKnown: true, Configuration: map[string]json.RawMessage{}}
			for key, name := range map[string]string{fileextensions.MediaKey: "media", fileextensions.SubtitleKey: "subtitle", fileextensions.AudioKey: "audio"} {
				in.Configuration[key], _ = json.Marshal(cfg[name])
			}
			snapshot, e := fileextensions.Resolve(in)
			if e != nil {
				t.Fatal(e)
			}
			c, e := New(snapshot)
			if e != nil {
				t.Fatal(e)
			}
			item := File{tc.Type, tc.Extension, true, tc.Bluray}
			media, e := c.Media(item)
			if e != nil {
				t.Fatal(e)
			}
			got := map[string]bool{"media": media, "subtitle": c.Subtitle(item), "audio": c.Audio(item), "allowed": c.Allowed(item), "handler_subtitle": c.Subtitle(item), "handler_extra": c.Extra(item)}
			for key, want := range tc.Expected {
				if got[key] != want {
					t.Fatalf("%s got%v want%v", key, got[key], want)
				}
			}
		})
	}
	for _, p := range oracle.Paths {
		if got := *PathFile(p.Name).Extension; got != p.Extension {
			t.Fatalf("path %q got %q want %q", p.Name, got, p.Extension)
		}
	}
}
func TestUnknownInputAndBlurayRemainDependencies(t *testing.T) {
	if _, e := New(fileextensions.Snapshot{}); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	s, e := fileextensions.Resolve(fileextensions.Inputs{Namespace: "native", SourceID: "test", EnvironmentKnown: true, ConfigurationKnown: true})
	if e != nil {
		t.Fatal(e)
	}
	c, e := New(s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Media(File{Type: "dir"}); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
}
