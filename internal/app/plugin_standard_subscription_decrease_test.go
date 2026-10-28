package app

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"light-media/internal/catalog"
)

func TestStandardSubscriptionDecreasedTotalFixedSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-subscription-events/decrease-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Record struct {
				Total  int   `json:"total_episode"`
				Start  int   `json:"start_episode"`
				Season int   `json:"season"`
				Lack   int   `json:"lack_episode"`
				TMDBID int64 `json:"tmdbid"`
			} `json:"subscription"`
			Candidate int `json:"candidate_total"`
			Inventory struct {
				Missing   []json.RawMessage
				Satisfied bool
				Mode      string
			}
			Result   int
			Prepared struct {
				Total int `json:"total_episode"`
				Lack  int `json:"lack_episode"`
			} `json:"prepared_fields"`
		}
		Flow []struct {
			Function    string
			Recognition []json.RawMessage `json:"recognition_calls"`
		} `json:"flow_proof"`
		Finish []struct {
			Kwargs map[string]string
		} `json:"finish_calls"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 || len(oracle.Flow) != 3 || len(oracle.Finish) != 2 {
		t.Fatal("fixed decrease and recognition-reuse source oracle", err)
	}
	for _, flow := range oracle.Flow {
		if len(flow.Recognition) != 0 {
			t.Fatal("fixed refresh/finish unexpectedly recognizes again", flow.Function)
		}
	}
	for _, call := range oracle.Finish {
		if call.Kwargs["mediainfo"] != "mediainfo" || call.Kwargs["meta"] != "meta" {
			t.Fatal("fixed finish did not receive the previous recognition")
		}
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			s := Subscription{ID: "owned-decrease-oracle", Media: catalog.Media{ID: sample.Record.TMDBID, MediaType: "tv"}, Season: sample.Record.Season, TotalEpisodes: sample.Record.Total, StartEpisode: sample.Record.Start, SourceNote: []int{}, LackEpisode: &sample.Record.Lack}
			missing, valid := map[int]bool{}, true
			for _, raw := range sample.Inventory.Missing {
				n, err := strconv.Atoi(string(raw))
				if err != nil || n < s.StartEpisode || n > s.TotalEpisodes {
					valid = false
					break
				}
				missing[n] = true
			}
			// Native missing facts have a typed SQL scope instead of a JSON
			// no_exists dictionary. Invalid source responses map to no confirmed
			// fact, not a newly invented inventory/parser extension.
			if sample.Inventory.Mode == "scope" || sample.Inventory.Mode == "not_dict" {
				valid = false
			}
			if valid && (sample.Inventory.Missing != nil || sample.Inventory.Satisfied) {
				for episode := s.StartEpisode; episode <= s.TotalEpisodes; episode++ {
					if !missing[episode] {
						if sample.Name == "confirmed_highest7" {
							// Synthetic SQL library-index input, not a file import
							// or an assertion of physical inventory acceptance.
							if _, err := a.Store.DB.Exec(`INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(s.Media), s.Season, episode, "owned-synthetic-slot"); err != nil {
								t.Fatal(err)
							}
						} else {
							s.SourceNote = append(s.SourceNote, episode)
						}
					}
				}
			}
			if sample.Inventory.Mode == "raise" {
				if _, err := a.Store.DB.Exec(`ALTER TABLE asset_slots RENAME TO owned_unavailable_slots`); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_slots RENAME TO asset_slots`); err != nil {
						t.Error(err)
					}
				}()
			}
			got := a.resolveStandardSubscriptionTotalDecrease(context.Background(), s, sample.Candidate)
			state := subscriptionQualityState(s).ChangeTotal(got)
			if got != sample.Result || state.TotalEpisode != sample.Prepared.Total || state.LackEpisode == nil || *state.LackEpisode != sample.Prepared.Lack {
				t.Fatal("fixed decrease/prepare output", got, sample.Result, state.TotalEpisode, state.LackEpisode)
			}
		})
	}
}
