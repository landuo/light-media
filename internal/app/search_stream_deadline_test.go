package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeywordStreamDeadlineKeepsLatestUsablePage(t *testing.T) {
	for _, pending := range []bool{false, true} {
		release := make(chan struct{})
		ended := make(chan struct{})
		seen := false
		deadline := time.Now().Add(keywordDeliveryReserve + 100*time.Millisecond)
		output, err := streamKeywordResults(context.Background(), deadline, func(frame SearchOutput) {
			seen = seen || frame.WorkflowID == "ready"
		}, func(ctx context.Context) (SearchOutput, error) {
			defer close(ended)
			emitSearchProgress(ctx, SearchOutput{
				WorkflowID: "ready", keywordFilterPending: pending,
				Sites: []SiteStatus{{ID: "fast", Status: "ok", Count: 2}, {ID: "slow", Status: "searching", Count: 1}, {ID: "silent", Status: "searching"}},
			})
			<-release // Simulate a provider winding down after cancellation.
			return SearchOutput{}, ctx.Err()
		})
		close(release)
		<-ended
		if !seen || time.Now().After(deadline) {
			t.Fatal("lost initial progress or waited for provider cleanup")
		}
		if pending {
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "SEARCH_FILTER_TIMEOUT" {
				t.Fatalf("unfiltered result escaped: %v", err)
			}
		} else if err != nil || len(output.Sites) != 3 || output.Sites[0].Status != "ok" || output.Sites[1].Status != "partial" || output.Sites[2].Status != "timeout" {
			t.Fatalf("wrong cutoff: %+v, %v", output, err)
		}
	}
}
