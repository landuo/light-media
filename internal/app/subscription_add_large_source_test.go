package app

import (
	"context"
	"strings"
	"testing"
)

func TestUISubscriptionActualLargeSourceMetadataPreservesCompleteOriginAndNotice(t *testing.T) {
	a, in, _ := uiSourceAddFixture(t)
	// A real fresh provider document and its original MediaInfo both retain
	// the source overview; their authenticated origin can exceed the ordinary
	// small configuration quota without any large user request body.
	media := in.Media
	media.Overview = strings.Repeat("owned large source overview ", 24000)
	fixture := installNoticeSourceFixtures(t, a, media, in.Season, 5)
	subscriptionStatsInstallSettings(t, a, "http://127.0.0.1:1", false)
	saved, e := a.createUISubscription(context.Background(), in, "owned-native-admin")
	if e != nil {
		t.Fatal("actual large trusted provider source failed", e)
	}
	origin, e := a.sourceSubscriptionOrigin(context.Background(), saved)
	if e != nil || len(mustCleanSeedJSON(origin)) <= pluginValueLimit || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 || noticeV2AddedCount(t, a) != 1 {
		t.Fatal("large source was lost or bypassed actual Notice", e, len(mustCleanSeedJSON(origin)), fixture.Metadata.Load(), fixture.Sends.Load())
	}
	for _, raw := range [][]byte{origin.MediaInfo, origin.ProviderInfo} {
		value, e := decodeSourceJSON(raw)
		if e != nil || sourceMap(value)["overview"] != media.Overview {
			t.Fatal("full provider overview was truncated", e)
		}
	}
	job, receipt := uiSourceOnlyAdd(t, a)
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	recovered, e := cold.sourceSubscriptionOrigin(context.Background(), saved)
	if e != nil || string(mustCleanSeedJSON(recovered)) != string(mustCleanSeedJSON(origin)) {
		t.Fatal("large source failed lossless cold read", e)
	}
	job = qualityRunningJob(t, cold, job)
	if e = cold.safeRun(context.Background(), job); e != nil || receipt.Stage != "complete" || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 {
		t.Fatal("cold completed large source replayed provider or Notice", e)
	}
}
