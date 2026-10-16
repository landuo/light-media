package scrapefileclean

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstalledWatchfilesBatchTranslator(t *testing.T) {
	data, e := os.ReadFile("../../../testdata/plugins/scrape-file-clean-1.0.7/fallback-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Case struct {
				Name    string
				Files   []string
				Batches []struct {
					Move    []string
					Remove  string
					Changes [][2]string
				}
			}
			Trace []ObserverEvent
		}
	}
	if json.Unmarshal(data, &suite) != nil || suite.Source != "c14c36e17112605e6c856614e88ea425530be2462123126a85e1adfea009a3af" {
		t.Fatal("reference invalid")
	}
	for _, c := range suite.Cases {
		t.Run(c.Case.Name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range c.Case.Files {
				if e := os.WriteFile(filepath.Join(root, name), []byte("owned"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			ops := &fixtureOps{root: root}
			m := New(ops)
			b := NewFallbackBatch(m, root)
			trace := []ObserverEvent{}
			b.Dispatch = func(event ObserverEvent) error {
				event.Src = strings.ReplaceAll(event.Src, root, "/fixture")
				event.Dest = strings.ReplaceAll(event.Dest, root, "/fixture")
				trace = append(trace, event)
				return nil
			}
			for _, batch := range c.Case.Batches {
				if len(batch.Move) > 0 {
					if e := os.Rename(filepath.Join(root, batch.Move[0]), filepath.Join(root, batch.Move[1])); e != nil {
						t.Fatal(e)
					}
				}
				if batch.Remove != "" {
					if e := os.Remove(filepath.Join(root, batch.Remove)); e != nil {
						t.Fatal(e)
					}
				}
				changes := []Change{}
				for _, change := range batch.Changes {
					changes = append(changes, Change{change[0], filepath.Join(root, change[1])})
				}
				if e := b.Apply(changes); e != nil {
					t.Fatal(e)
				}
			}
			if !reflect.DeepEqual(trace, c.Trace) {
				t.Fatalf("source translator mismatch: %#v / %#v", trace, c.Trace)
			}
		})
	}
}
