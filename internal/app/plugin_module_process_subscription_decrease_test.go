package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func verifyOwnedProductionSubscriptionDecrease(t *testing.T, dir, id, trace string, request func(string, string, any, int) []byte, stop, start func()) {
	t.Helper()
	// Only this initial source note/old total are synthetic existing row data.
	// The two checks, intervening reset and cold recovery are actual native HTTP.
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var initial Subscription
	if err = seed.Store.Get(context.Background(), "subscription", id, &initial); err != nil {
		t.Fatal(err)
	}
	initial.TotalEpisodes, initial.SourceNote = 12, []int{10}
	lack := 11
	initial.LackEpisode = &lack
	if err = seed.Store.Put(context.Background(), "subscription", id, initial); err != nil {
		t.Fatal(err)
	}
	// The earlier owned check deliberately had no downloader and may remain
	// retry_wait. Retire only that test row's old generation while preparing
	// the new initial condition, rather than weakening JOB_EXISTS admission.
	if _, err = seed.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0 WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, id); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	await := func(total, missing int) Subscription {
		t.Helper()
		until := time.Now().Add(8 * time.Second)
		for {
			var out struct {
				Data struct{ Items []Subscription }
			}
			if json.Unmarshal(request("GET", "/api/subscriptions", nil, 200), &out) != nil {
				t.Fatal("actual decrease list")
			}
			for _, s := range out.Data.Items {
				if s.ID == id && s.TotalEpisodes == total && s.LackEpisode != nil && *s.LackEpisode == missing {
					return s
				}
			}
			if time.Now().After(until) {
				t.Fatal("actual decreased total did not follow provider/confirmed facts", total, missing)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	request("POST", "/api/subscriptions/"+id+"/check", map[string]any{}, 200)
	confirmed := await(10, 9)
	if len(confirmed.SourceNote) != 1 || confirmed.SourceNote[0] != 10 {
		t.Fatal("actual precheck dropped confirmed source note")
	}
	request("POST", "/api/subscriptions/"+id+"/reset", map[string]any{}, 200)
	request("POST", "/api/subscriptions/"+id+"/check", map[string]any{}, 200)
	cleared := await(8, 8)
	if len(cleared.SourceNote) != 0 {
		t.Fatal("actual reset did not clear confirmed note")
	}
	copyOwnedEventsProcessEvidence(t, "events36-decrease-sdk-trace.jsonl", trace)
	body, _ := json.Marshal(map[string]any{"initial": initial, "confirmed": confirmed, "after_reset_check": cleared})
	writeOwnedEventsProcessEvidence(t, "events36-decrease-states.json", body)
	stop()
	start()
	await(8, 8)
	t.Log("actual Linux precheck decrease: synthetic initial old total12/note10/lack11; real check provider5+override8 -> total10/lack9; real reset clears note and next check -> total8/lack8; cold state remains8 without permanent max(old,total)")
}
