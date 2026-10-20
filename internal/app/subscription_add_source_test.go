package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/releasemeta"
	"light-media/internal/subscriptionstats"
)

func TestUISubscriptionSourceFactoryPreservesOriginalLocalsAndFullProvider(t *testing.T) {
	for _, mode := range []string{"legacy_media_locals", "explicit_null", "explicit_text"} {
		t.Run(mode, func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			m, e := a.media(ctx, "tv", 206484)
			if e != nil {
				t.Fatal(e)
			}
			fixture := installNoticeSourceFixtures(t, a, m, 4, 5)
			m.Title, m.Year = "原请求名", 2030
			in := subscriptionRequest(t)
			in.Media = m
			if mode == "explicit_null" {
				in.SourceName = json.RawMessage(`null`)
				in.SourceYear = json.RawMessage(`null`)
			}
			if mode == "explicit_text" {
				in.SourceName = json.RawMessage(`"调用方名称"`)
				in.SourceYear = json.RawMessage(`"1999"`)
			}
			source, e := a.buildUISubscriptionAdd(ctx, in, "owned-native-admin")
			if e != nil {
				t.Fatal(e)
			}
			meta, e := releasemeta.DecodeMetaObjectState(source.Origin.Meta)
			if e != nil {
				t.Fatal(e)
			}
			var locals map[string]any
			if cleanupJournalStrict(source.Registration, &locals) != nil {
				t.Fatal("invalid source locals")
			}
			if len(locals) != 16 || source.Origin.State != "N" || source.Origin.Note != nil || string(source.Username) != `"owned-native-admin"` || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 0 {
				t.Fatal("factory fabricated mutation/send or lost source fields", len(locals), fixture.Metadata.Load(), fixture.Sends.Load())
			}
			switch mode {
			case "legacy_media_locals":
				if locals["name"] != "原请求名" || locals["year"] != "2030" || meta.Text("org_string") != "原请求名" {
					t.Fatal("fresh recognition rewrote original call locals", locals)
				}
			case "explicit_null":
				if locals["name"] != nil || locals["year"] != nil || meta.Get("org_string") != nil {
					t.Fatal("None became recognized title/year", locals)
				}
			case "explicit_text":
				if locals["name"] != "调用方名称" || locals["year"] != "1999" || meta.Text("year") != "1999" {
					t.Fatal("explicit source locals lost", locals)
				}
			}
			if _, e = subscriptionstats.BuildRegistration(source.Registration); e != nil {
				t.Fatal("source factory lacks original statistic fields", e)
			}
			var business sourceBusinessNotice
			if cleanupJournalStrict(source.Business, &business) != nil {
				t.Fatal("invalid frozen source business")
			}
			mediaValue, e := decodeSourceJSON(business.MediaInfo)
			if e != nil {
				t.Fatal(e)
			}
			media := sourceMap(mediaValue)
			if media["title"] != "诛仙" || media["source"] != "themoviedb" || media["tmdb_id"] != json.Number("206484") || len(media) < 66 || business.Envelope["source"] != nil || business.Envelope["channel"] != nil || business.Envelope["username"] != "owned-native-admin" {
				t.Fatal("compact catalog masqueraded as source MediaInfo", len(media), e)
			}
		})
	}
}
func TestSubscriptionStatsDisabledIsLazyBeforeInvalidPayload(t *testing.T) {
	a := subscriptionTestApp(t)
	disabled := false
	result, e := a.executeSubscriptionStats(context.Background(), subscriptionStatsInput{Operation: "done", Settings: subscriptionstats.SettingsSnapshot{Share: &disabled}, Subscribe: []byte("not-json"), MediaInfo: []byte("NaN")})
	if e != nil || !result.SourceReturned || result.SourceValue || result.Outcome != "disabled" {
		t.Fatal("disabled sharing evaluated child JSON", result, e)
	}
}
