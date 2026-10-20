package acquisitionbatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestOriginalDownloaderPostAddFileSelectionOracle(t *testing.T) {
	type call struct {
		Kind               string
		Attempts, Interval int
		IDs                []json.Number
		Wanted, Force      bool
	}
	var corpus struct {
		Sources map[string]string
		Cases   []struct {
			Kind                                      string
			Episodes                                  []int
			Files                                     []EpisodeFile
			ByName                                    map[string][]int
			Force, SetAcknowledged, StartAcknowledged bool
			Calls                                     []call
			MetadataMissing                           bool
			OriginalResult                            []any
		}
	}
	raw, e := os.ReadFile("testdata/file-selection-source-oracle.json")
	if e != nil || json.Unmarshal(raw, &corpus) != nil || len(corpus.Cases) != 144 || len(corpus.Sources) != 2 {
		t.Fatal("incomplete actual downloader post-Add AST oracle", e)
	}
	for i, tc := range corpus.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			calls := []call{}
			out, e := SelectFiles(context.Background(), tc.Kind, tc.Episodes, tc.Force, FileSelectionDependencies{
				Files: func(_ context.Context, attempts int, interval time.Duration) ([]EpisodeFile, error) {
					calls = append(calls, call{Kind: "files", Attempts: attempts, Interval: int(interval / time.Second)})
					return tc.Files, nil
				},
				Episodes: func(name string) ([]int, error) { return tc.ByName[name], nil },
				SetFiles: func(_ context.Context, ids []json.Number, wanted bool) (bool, error) {
					calls = append(calls, call{Kind: "set", IDs: append([]json.Number{}, ids...), Wanted: wanted})
					return tc.SetAcknowledged, nil
				},
				Start: func(_ context.Context, force bool) (bool, error) {
					calls = append(calls, call{Kind: "start", Force: force})
					return tc.StartAcknowledged, nil
				},
			})
			if e != nil || !reflect.DeepEqual(calls, tc.Calls) || out.MetadataMissing != tc.MetadataMissing {
				t.Fatalf("source post-Add behavior differs: got %#v want %#v, metadata missing %v/%v, err %v", calls, tc.Calls, out.MetadataMissing, tc.MetadataMissing, e)
			}
			// The original module returns its already-accepted hash even when
			// metadata is missing or a setter/start returned False. Native effect
			// acknowledgments therefore must remain separate from Add acceptance.
			if len(tc.Episodes) > 0 && (len(tc.OriginalResult) != 4 || tc.OriginalResult[1] != "ownedhash") {
				t.Fatal("source hash acceptance fixture was weakened")
			}
		})
	}
}
