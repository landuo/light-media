package acquisitionbatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestOriginalBatchDownloadOracle(t *testing.T) {
	type call struct {
		Kind, ID, Error string
		Episodes        *[]int
		Content         bool
		MetaEpisodes    []int
	}
	type finalCandidate struct {
		ID                    string
		Episodes              []int
		ConfirmedFullCoverage bool
	}
	var corpus struct {
		SourceSHA256 string
		Cases        []struct {
			Input struct {
				Candidates    []*Candidate
				Needs         []MediaNeed
				SingleResults map[string]bool
				Materials     map[string]struct {
					Content, Magnet bool
					Episodes        []int
				}
				ActiveFailures []string
				StopAfter      *int
			}
			Calls           []call
			Downloaded      []string
			Remaining       []MediaNeed
			FinalCandidates []finalCandidate
		}
	}
	raw, e := os.ReadFile("testdata/batch-source-oracle.json")
	if e != nil || json.Unmarshal(raw, &corpus) != nil || len(corpus.Cases) != 640 || len(corpus.SourceSHA256) != 64 {
		t.Fatal("incomplete original AST batch corpus", e)
	}
	for index, tc := range corpus.Cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			calls := []call{}
			out, e := Run(context.Background(), tc.Input.Candidates, tc.Input.Needs, Dependencies{
				Sort: func(_ context.Context, c []*Candidate) ([]*Candidate, error) { return c, nil },
				ActiveFailures: func(context.Context, []*Candidate) (map[string]bool, error) {
					out := map[string]bool{}
					for _, f := range tc.Input.ActiveFailures {
						out[f] = true
					}
					return out, nil
				},
				Stopped: func() bool { return tc.Input.StopAfter != nil && len(calls) >= *tc.Input.StopAfter },
				Torrent: func(_ context.Context, c *Candidate) (Material, error) {
					calls = append(calls, call{Kind: "torrent", ID: c.ID})
					m := tc.Input.Materials[c.ID]
					out := Material{Magnet: m.Magnet, Episodes: m.Episodes}
					if m.Content && !m.Magnet {
						out.Content = []byte("owned")
					}
					return out, nil
				},
				Single: func(_ context.Context, d Download) (bool, error) {
					c := call{Kind: "single", ID: d.Candidate.ID, Episodes: d.Episodes, Content: len(d.Content) > 0, MetaEpisodes: append([]int{}, d.Candidate.Episodes...)}
					if c.Episodes != nil {
						copy := append([]int{}, (*c.Episodes)...)
						sort.Ints(copy)
						c.Episodes = &copy
					}
					calls = append(calls, c)
					return tc.Input.SingleResults[d.Candidate.ID], nil
				},
				RecordFailure: func(_ context.Context, c *Candidate, s string) error {
					calls = append(calls, call{Kind: "failure", ID: c.ID, Error: s})
					return nil
				},
			})
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(calls, tc.Calls) {
				t.Fatalf("source call sequence differs:\n got %#v\nwant %#v", calls, tc.Calls)
			}
			got := []string{}
			for _, c := range out.Downloaded {
				got = append(got, c.ID)
			}
			if !reflect.DeepEqual(got, tc.Downloaded) {
				t.Fatal("source accepted contexts differ", got, tc.Downloaded)
			}
			for i := range out.Remaining {
				for j := range out.Remaining[i].Seasons {
					sort.Ints(out.Remaining[i].Seasons[j].Episodes)
				}
			}
			if !reflect.DeepEqual(out.Remaining, tc.Remaining) {
				t.Fatalf("source missing scope differs: got %#v want %#v", out.Remaining, tc.Remaining)
			}
			final := []finalCandidate{}
			for _, c := range tc.Input.Candidates {
				final = append(final, finalCandidate{c.ID, c.Episodes, c.ConfirmedFullCoverage})
			}
			if !reflect.DeepEqual(final, tc.FinalCandidates) {
				t.Fatalf("source metadata mutation differs: got %#v want %#v", final, tc.FinalCandidates)
			}
		})
	}
}
