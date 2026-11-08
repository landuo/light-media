package fnos

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestEpisodeInventoryVerifiesSeriesAndSeason(t *testing.T) {
	const otherSeries = "11111111111111111111111111111112"
	for _, scenario := range []string{"owned", "absent", "unidentified", "wrong_season", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			service := testService(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					t.Error("inventory made a write request")
				}
				switch request.URL.Path {
				case "/v/api/v1/search/list":
					if request.URL.Query().Get("q") != "吞噬星空" {
						t.Error("title was not decoded for provider search")
					}
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"type":"TV","title":"吞噬星空"},{"guid":%q,"type":"TV","title":"吞噬星空"}]}`, otherSeries, serviceTVGUID)
				case "/v/api/v1/item/" + otherSeries:
					fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"TV","title":"吞噬星空","trim_id":"tt41"}}`, otherSeries, serviceLibGUID)
				case "/v/api/v1/item/" + serviceTVGUID:
					trimID := "tt42"
					if scenario == "unidentified" {
						trimID = ""
					}
					fmt.Fprintf(writer, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"TV","title":"吞噬星空","trim_id":%q}}`, serviceTVGUID, serviceLibGUID, trimID)
				case "/v/api/v1/season/list/" + serviceTVGUID:
					if scenario == "unidentified" {
						t.Error("identity was not confirmed before reading season")
					}
					number := 1
					if scenario == "absent" {
						number = 2
					}
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"season_number":%d}]}`, serviceSeasonGUID, number)
				case "/v/api/v1/episode/list/" + serviceSeasonGUID:
					if scenario == "absent" {
						t.Error("another season was queried")
					}
					if scenario == "unavailable" {
						writer.WriteHeader(http.StatusBadGateway)
						return
					}
					number := 1
					if scenario == "wrong_season" {
						number = 2
					}
					fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"type":"Episode","parent_guid":%q,"ancestor_guid":%q,"season_number":%d,"episode_number":248},{"guid":"55555555555555555555555555555556","season_number":1,"episode_number":247}]}`, serviceEpisodeGUID, serviceSeasonGUID, serviceLibGUID, number)
				default:
					t.Errorf("unexpected route %q", request.URL.Path)
				}
			})
			episodes, err := service.EpisodeInventory(context.Background(), "吞噬星空", 42, 1)
			switch scenario {
			case "unidentified", "wrong_season":
				serviceErrorCode(t, err, "invalid_response")
			case "unavailable":
				serviceErrorCode(t, err, "unavailable")
			case "absent":
				if err != nil || episodes == nil || len(episodes) != 0 {
					t.Fatalf("expected verified empty inventory, got %v and %v", episodes, err)
				}
			case "owned":
				if err != nil || !slices.Equal(episodes, []int{247, 248}) {
					t.Fatalf("expected verified episodes 247 and 248, got %v and %v", episodes, err)
				}
			}
		})
	}
}
