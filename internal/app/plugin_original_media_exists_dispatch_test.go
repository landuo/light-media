package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOriginalMediaExistsListInvokesNativeFixedSource(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-module-host/media-exists-dispatch-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name, Kind   string
			Plugin       json.RawMessage
			NativeError  bool `json:"native_error"`
			SystemErrors int  `json:"system_errors"`
			Satisfied    *bool
			Error        *string
		}
	}
	if e != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 {
		t.Fatal("fixed dispatcher oracle", e)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, s, _ := originalExistenceFixture(t, sample.Plugin)
			ctx := context.Background()
			s.Media.MediaType = sample.Kind
			if sample.NativeError {
				if _, e := a.Store.DB.Exec(`ALTER TABLE asset_slots RENAME TO owned_existence_unavailable`); e != nil {
					t.Fatal(e)
				}
				defer func() {
					if _, e := a.Store.DB.Exec(`ALTER TABLE owned_existence_unavailable RENAME TO asset_slots`); e != nil {
						t.Error(e)
					}
				}()
			}
			a.wg.Wait()
			a.systemMessages.get("system")
			resolved, e := a.resolveOriginalSubscriptionExistence(ctx, s, &standardSubscriptionRecognition{Information: sourceMediaInformation{MediaInfo: json.RawMessage(`{"tmdb_id":900043}`)}})
			if (e != nil) != (sample.Error != nil) {
				t.Fatal("source caller result/error", e, sample.Error)
			}
			a.wg.Wait()
			messages := a.systemMessages.get("system")
			if (len(messages) > 0) != (sample.SystemErrors > 0) {
				t.Fatal("source native dispatch/error side effect omitted", string(messages), sample.SystemErrors)
			}
			if len(messages) > 0 && !strings.Contains(string(messages), "文件整理发生了错误") {
				t.Fatal("source system module name", string(messages))
			}
			if e == nil {
				coverage, e := a.subscriptionCoverageWithExistence(ctx, s, []int{1, 2, 3}, resolved.Existence)
				if e != nil {
					t.Fatal("list caller must keep its result even when native fails", e)
				}
				if sample.Satisfied == nil || *sample.Satisfied != (len(coverage.Missing) == 0) {
					t.Fatal("source list truth semantics", coverage, sample.Satisfied)
				}
			}
		})
	}
}
