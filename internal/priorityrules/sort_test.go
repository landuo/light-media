package priorityrules

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestInstalledTorrentSortingReference(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/torrent-sort-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SHA   string `json:"source_sha256"`
		Cases []struct {
			Name       string      `json:"name"`
			Priorities []string    `json:"priorities"`
			Inputs     []SortInput `json:"inputs"`
			Result     struct {
				Keys  []string `json:"keys"`
				Order []int    `json:"order"`
			} `json:"result"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 88 || fixture.SHA != "1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5" {
		t.Fatal("invalid installed sorting reference")
	}
	for _, sample := range fixture.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			keys, order := []string{}, []int{}
			for i, input := range sample.Inputs {
				keys = append(keys, SortKey(input, sample.Priorities))
				order = append(order, i)
			}
			if !reflect.DeepEqual(keys, sample.Result.Keys) {
				t.Fatal("sorting keys differ from installed original")
			}
			slices.SortStableFunc(order, func(i, j int) int {
				if keys[i] > keys[j] {
					return -1
				}
				if keys[i] < keys[j] {
					return 1
				}
				return 0
			})
			if !reflect.DeepEqual(order, sample.Result.Order) {
				t.Fatal("sorting order differs", order, sample.Result.Order)
			}
		})
	}
}
