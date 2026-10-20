package app

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"light-media/internal/catalog"
)

func TestPluginLarkOriginalDownloadDirectoryOracle(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugins/lark-message-chain-reference/directory-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	type choice struct {
		Name, Path, Label string
		Priority          int
		Auto              bool
	}
	var corpus struct {
		Source map[string]string
		Cases  []struct {
			Rules          []DirectoryRule
			Kind, Category string
			Explicit       *string
			Choices        []choice
			Path, Error    string
			Exception      string
		}
	}
	if e = json.Unmarshal(raw, &corpus); e != nil || len(corpus.Cases) != 960 || len(corpus.Source) != 3 {
		t.Fatal("incomplete original AST oracle", e)
	}
	for i, tc := range corpus.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := catalog.Media{MediaType: tc.Kind}
			dirs := larkDownloadDirectories(tc.Rules, m, tc.Category)
			got := []choice{}
			for _, d := range dirs {
				got = append(got, choice{Name: d.Name, Path: larkDirectoryPath(d), Label: larkDownloadDirectoryLabel(d), Priority: d.Priority, Auto: larkIsAutoDirectory(d)})
			}
			if !reflect.DeepEqual(got, tc.Choices) {
				t.Fatalf("source choices differ: got %#v want %#v", got, tc.Choices)
			}
			path, e := larkResolveDownloadPath(tc.Rules, m, tc.Category, tc.Explicit)
			if tc.Error != "" || tc.Exception != "" {
				if e == nil {
					t.Fatal("source rejection became a path", path, tc.Error, tc.Exception)
				}
			} else if e != nil || path != tc.Path {
				t.Fatal("source path differs", path, tc.Path, e)
			}
		})
	}
}
func TestPluginLarkAutoDownloadUserUsesOriginalExactList(t *testing.T) {
	for _, tc := range []struct {
		config, user string
		want         bool
	}{{"", "a", false}, {"all", "a", true}, {"ALL", "a", false}, {"a,b", "b", true}, {"a, b", "b", false}, {"a, b", " b", true}, {"alice", "ali", false}} {
		if got := larkShouldAutoDownload(tc.config, tc.user); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
