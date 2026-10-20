package app

import (
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/releasemeta"
)

func TestPluginNoticeV2CompletionBuildsOriginalMetaAndFreshMedia(t *testing.T) {
	for _, best := range []bool{false, true} {
		t.Run(map[bool]string{false: "subscribe", true: "best_version"}[best], func(t *testing.T) {
			a, _, ctx, request, s, fixture := noticeV2SubscriptionFixture(t)
			original := map[string]any{"name": "完成时原名称 S03", "year": nil, "season": 4, "type": "电视剧", "tmdbid": s.Media.ID, "doubanid": nil, "bangumiid": nil, "anilistid": nil, "media_source": "themoviedb", "media_id": "206484", "best_version": best, "username": nil}
			source, e := a.buildSubscriptionCompletionBusiness(ctx, s, mustCleanSeedJSON(original))
			if e != nil {
				t.Fatal(e)
			}
			if fixture.Metadata.Load() != 2 || !larkSameRaw(source.Origin, mustCleanSeedJSON(request.source)) || !larkSameRaw(source.Subscription, mustCleanSeedJSON(s)) {
				t.Fatal("completion used compact cached media or changed source identity", fixture.Metadata.Load())
			}
			meta, e := releasemeta.DecodeMetaObjectState(source.Meta)
			if e != nil || meta.Get("year") != nil || meta.Get("media_id") != "206484" || meta.Get("type") != "电视剧" || meta.Number("begin_season").Int64() != 4 {
				t.Fatal("source build_subscribe_meta failed to override parsed values incl None", meta, e)
			}
			if !strings.Contains(string(source.ProviderInfo), "source_poster") || !strings.Contains(string(source.MediaInfo), "tmdb_info") {
				t.Fatal("full fresh provider/class projection missing")
			}
			var business sourceBusinessNotice
			if e = cleanupJournalStrict(source.Business, &business); e != nil {
				t.Fatal(e)
			}
			if business.Envelope["ctype"] != "subscribeComplete" || business.Envelope["mtype"] != "订阅" || business.Envelope["channel"] != nil || business.Envelope["userid"] != nil || business.Envelope["username"] != nil || business.Kwargs["username"] != nil || business.Envelope["image"] != "https://image.tmdb.org/t/p/w500/source_backdrop.jpg" {
				t.Fatal("completion source envelope shape changed", business.Envelope)
			}
			want := "订阅"
			if best {
				want = "洗版"
			}
			if business.Kwargs["msgstr"] != want {
				t.Fatal("best version completion used ordinary template context")
			}
			if !json.Valid(source.Subscribe) {
				t.Fatal("original Subscribe.to_dict was not frozen")
			}
			delete(original, "username")
			if _, e = a.buildSubscriptionCompletionBusiness(ctx, s, mustCleanSeedJSON(original)); e == nil || fixture.Metadata.Load() != 2 {
				t.Fatal("missing original user was guessed or dispatched metadata", e)
			}
		})
	}
}
