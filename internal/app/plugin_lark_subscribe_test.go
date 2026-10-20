package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

func sourceSubscriptionFixture(t *testing.T, a *App) (*pluginLarkCoreExecution, context.Context, *pluginLarkMediaSession, pluginLarkMediaTargets) {
	t.Helper()
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	m, e := a.media(ctx, "tv", 206484)
	if e != nil {
		t.Fatal(e)
	}
	installNoticeSourceFixtures(t, a, m, 4, 5)
	options, e := a.globalRecognitionOptions(ctx)
	if e != nil {
		t.Fatal(e)
	}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if e != nil {
		t.Fatal(e)
	}
	meta, e := parser.ParseTitle("诛仙 S04", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := meta.EncodeState()
	if e != nil {
		t.Fatal(e)
	}
	s := &pluginLarkMediaSession{Request: "owned-subscribe", User: "ou_owned", Action: "Subscribe", Selected: &m, Meta: raw}
	facts := pluginLarkMediaTargets{Media: m, Known: map[int][]int{4: {1, 2, 3, 4, 5}}, Missing: map[int][]int{4: {1, 2, 3, 4, 5}}, Requests: []SearchRequest{{MediaType: "tv", TMDBID: m.ID, Season: 4, Episodes: []int{1, 2, 3, 4, 5}}}}
	return core, ctx, s, facts
}

func TestPluginLarkSourceSubscriptionDefaultsUsernameAtomicOriginDuplicateAndNoticeOrder(t *testing.T) {
	a := subscriptionTestApp(t)
	source := t.TempDir()
	a.config.Directories = []DirectoryRule{{ID: "owned-source", SourcePath: source, MediaType: "tv", RootID: "test-tv", Mode: "hardlink"}}
	a.config.AllowedRoots = []string{source, a.config.Roots[0].Path}
	a.config.DefaultTVSubscribeConfig = mustCleanSeedJSON(map[string]any{"quality": "WEB-?DL", "resolution": "1080p", "effect": "HDR", "include": "Team", "exclude": "BAD", "best_version": true, "best_version_full": true, "search_imdbid": 1, "downloader": "test-qb", "save_path": "local:" + source, "filter_groups": []string{"original-default"}})
	core, ctx, s, facts := sourceSubscriptionFixture(t, a)
	noticePreferenceTest(t, a, "user", "alice", map[string]any{"feishu_userid": "ou_owned"})
	noticePreferenceTest(t, a, "user", "bob", map[string]any{"feishu_userid": "ou_owned"})
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.pluginStore.saveSourceUserOrderTx(ctx, tx, []string{"bob", "alice"}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	ctx = context.WithValue(ctx, pluginLarkCallerKey{}, pluginLarkCaller{Username: json.RawMessage(`"caller"`)})
	best := false
	request, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, s.User, "N", nil, &best)
	if e != nil || request.source == nil || string(request.source.Username) != `"bob"` || request.source.Defaults["best_version"] != false || request.Policy.FilterParams == nil || *request.Policy.FilterParams.Quality != "WEB-?DL" || request.DownloadPath != source || request.DownloaderID != "test-qb" {
		t.Fatal("source defaults/user binding changed", request, e)
	}
	if !reflect.DeepEqual(request.Policy.FilterGroups, []string{"original-default"}) || request.TotalEpisodes != 5 || request.Season != 4 {
		t.Fatal("source season/default scope changed", request)
	}
	mutationOrdinal := core.ordinal
	saved, e := larkCoreWrite(ctx, a, core, "subscription.save", larkSourceSubscriptionInput(request), func(ctx context.Context) (Subscription, error) {
		saved, e := a.saveSubscription(ctx, "", request)
		if e != nil {
			return saved, e
		}
		return saved, errors.New("owned ACK lost after actual subscription and source origin Commit")
	})
	if e != nil || saved.ID == "" || saved.SourceState != "N" || saved.SourceContextSHA == "" || !saved.SourceSearchIMDb {
		t.Fatal("actual source creation was not atomic", saved, e)
	}
	origin, e := a.sourceSubscriptionOrigin(ctx, saved)
	if e != nil || origin.State != "N" || origin.Note != nil || string(origin.Username) != `"bob"` {
		t.Fatal("source creation fact lost original nulls/order", origin, e)
	}
	rows, e := a.Store.List(ctx, "subscription_added", 10, 0)
	if e != nil || len(rows) != 0 {
		t.Fatal("SubscribeAdded event ran before original notice", len(rows), e)
	}
	core.ordinal = mutationOrdinal
	replay, e := a.larkSaveSourceSubscription(ctx, core, request)
	if e != nil || replay.ID != saved.ID || replay.SourceContextSHA != saved.SourceContextSHA {
		t.Fatal("source lost-ACK replay allocated another subscription", replay, e)
	}
	// A distinct add invocation on the same media returns the old row, does not
	// mutate its old N state, note, revision or original defaults, and can still
	// continue through its current caller's notice/event sequence.
	copy := request
	newOrigin := *request.source
	newOrigin.State = "R"
	newOrigin.Note = []int{2}
	copy.source = &newOrigin
	copy.Policy.Include = "current-call-only"
	duplicate, e := a.larkSaveSourceSubscription(ctx, core, copy)
	if e != nil || duplicate.ID != saved.ID || duplicate.Revision != saved.Revision || duplicate.SourceState != "N" || duplicate.SourceContextSHA != saved.SourceContextSHA {
		t.Fatal("existing source add updated/rejected old subscription", duplicate, e)
	}
	if e = a.larkCompleteSourceSubscription(ctx, core, copy, duplicate); e != nil {
		t.Fatal("actual original template/route did not return normally", e)
	}
	rows, e = a.Store.List(ctx, "subscription_added", 10, 0)
	if e != nil || len(rows) != 1 {
		t.Fatal("actual normal notice did not publish exactly one source event", len(rows), e)
	}
	endOrdinal := core.ordinal
	a.config.DefaultTVSubscribeConfig = json.RawMessage(`{"quality":"changed-after-source-read"}`)
	noticePreferenceTest(t, a, "user", "bob", map[string]any{"feishu_userid": "changed-after-source-read"})
	core.ordinal = 0
	frozen, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, s.User, "N", nil, &best)
	if e != nil || string(frozen.source.Username) != `"bob"` || *frozen.Policy.FilterParams.Quality != "WEB-?DL" || !larkSameRaw(mustCleanSeedJSON(json.RawMessage(frozen.source.Meta)), mustCleanSeedJSON(json.RawMessage(request.source.Meta))) {
		t.Fatal("frozen source defaults/username/metainfo reinterpreted during retry", frozen.source, e)
	}
	core.ordinal = endOrdinal
	if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='source_subscription_origin' AND id=?`, saved.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.sourceSubscriptionOrigin(ctx, saved); e == nil {
		t.Fatal("deleted sealed origin was silently recreated")
	}
}

func TestPluginLarkSourceSubscriptionRNoteAndNullableOrLargeCallerUsername(t *testing.T) {
	for _, username := range []json.RawMessage{json.RawMessage("null"), json.RawMessage("900719925474099312345")} {
		t.Run(string(username), func(t *testing.T) {
			a := subscriptionTestApp(t)
			core, ctx, s, facts := sourceSubscriptionFixture(t, a)
			ctx = context.WithValue(ctx, pluginLarkCallerKey{}, pluginLarkCaller{Username: username})
			request, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, s.User, "R", []int{1, 3}, nil)
			if e != nil || !reflect.DeepEqual(request.source.Note, []int{1, 3}) || string(request.source.Username) != string(username) {
				t.Fatal("R/notes/source caller changed", request.source, e)
			}
			saved, e := a.larkSaveSourceSubscription(ctx, core, request)
			if e != nil || saved.SourceState != "R" || !reflect.DeepEqual(saved.SourceNote, []int{1, 3}) {
				t.Fatal("real source R state was replaced by plain native active", saved, e)
			}
			origin, e := a.sourceSubscriptionOrigin(ctx, saved)
			if e != nil || string(origin.Username) != string(username) {
				t.Fatal("nullable/large source caller rounded during actual core read", origin, e)
			}
		})
	}
}

func TestPluginLarkSourceUserOrderMissingDoesNotGuessAdministrator(t *testing.T) {
	a := subscriptionTestApp(t)
	core, ctx, _, _ := sourceSubscriptionFixture(t, a)
	noticePreferenceTest(t, a, "user", "original-user", map[string]any{"feishu_userid": "ou_owned"})
	if _, e := a.larkSubscriptionUsername(ctx, core, "ou_owned"); pluginErrorCodeInternal(e) != "SOURCE_USER_ORDER_MISSING" {
		t.Fatal("unordered imported users chose an arbitrary native name", e)
	}
}

func TestLarkSourceMediaMessageImageAndSubscriptionLink(t *testing.T) {
	media := catalog.Media{BackdropPath: "/background.jpg", PosterPath: "/poster.jpg"}
	if got := larkSourceMediaImage(media, ""); got != "https://image.tmdb.org/t/p/w500/background.jpg" {
		t.Fatal(got)
	}
	media.BackdropPath = ""
	if got := larkSourceMediaImage(media, "images.example.invalid"); got != "https://images.example.invalid/t/p/w500/poster.jpg" {
		t.Fatal(got)
	}
	if sourceSubscriptionLink("", "tv") != nil || sourceSubscriptionLink("https://example.invalid/base", "tv") != "https://example.invalid/base/#/subscribe/tv?tab=mysub" {
		t.Fatal("source APP_DOMAIN absence/base fragment changed")
	}
}

func TestPluginLarkSourceNoteNoneEmptyAndRuntimeEditKeepOriginalIdentity(t *testing.T) {
	for _, c := range []struct {
		name string
		note []int
	}{{"None", nil}, {"empty", []int{}}, {"values", []int{1, 3}}} {
		t.Run(c.name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			core, ctx, s, facts := sourceSubscriptionFixture(t, a)
			request, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, s.User, "R", c.note, nil)
			if e != nil {
				t.Fatal(e)
			}
			saved, e := a.larkSaveSourceSubscription(ctx, core, request)
			if e != nil {
				t.Fatal(e)
			}
			var cold Subscription
			if e = a.Store.Get(ctx, "subscription", saved.ID, &cold); e != nil || !reflect.DeepEqual(cold.SourceNote, c.note) {
				t.Fatal("SQL persistence conflated original None and []", cold.SourceNote, c.note, e)
			}
			origin, e := a.sourceSubscriptionOrigin(ctx, cold)
			if e != nil || !reflect.DeepEqual(origin.Note, c.note) {
				t.Fatal("sealed creation note lost original type", origin, e)
			}
			edit := subscriptionRequest(t)
			edit.Revision = cold.Revision
			edit.State = "paused"
			edited, e := a.saveSubscription(context.Background(), cold.ID, edit)
			if e != nil || edited.SourceContextSHA != cold.SourceContextSHA || !reflect.DeepEqual(edited.SourceNote, c.note) {
				t.Fatal("ordinary edit rewrote creation identity or runtime note type", edited, e)
			}
			if e = a.Store.Get(ctx, "subscription", saved.ID, &cold); e != nil || !reflect.DeepEqual(cold.SourceNote, c.note) {
				t.Fatal("edited note changed on SQL readback", cold.SourceNote, e)
			}
			if _, e = a.sourceSubscriptionOrigin(ctx, cold); e != nil {
				t.Fatal("runtime revision invalidated immutable source origin", e)
			}
		})
	}
}

func TestPluginLarkStatisticsKeepOriginalNoneYearApartFromParsedMeta(t *testing.T) {
	a := subscriptionTestApp(t)
	core, ctx, session, facts := sourceSubscriptionFixture(t, a)
	facts.Media.Title = "诛仙 2024"
	facts.Media.Year = 0
	request, e := a.larkSourceSubscriptionRequest(ctx, core, session, facts, session.User, "N", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	meta, e := releasemeta.DecodeMetaObjectState(request.source.Meta)
	if e != nil {
		t.Fatal(e)
	}
	if !sourceSubscriptionTruth(meta.Get("year")) {
		t.Fatal("oracle setup did not actually parse a title year")
	}
	var registration map[string]any
	if cleanupJournalStrict(request.source.Registration, &registration) != nil || registration["name"] != "诛仙 2024" || registration["year"] != nil || len(registration) != 16 {
		t.Fatal("parsed MetaInfo overwrote original add() statistic locals", registration)
	}
	saved, e := a.larkSaveSourceSubscription(ctx, core, request)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
		t.Fatal(e)
	}
	_, receipt := subscriptionStatsOnly(t, a)
	if receipt.Input.Lark == nil || !larkSameRaw(receipt.Input.Lark.Notice.Registration, request.source.Registration) {
		t.Fatal("actual core added did not bind the original statistic locals")
	}
}
