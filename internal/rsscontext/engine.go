package rsscontext

import (
	"context"
	"errors"
	"light-media/internal/media/words"
	"light-media/internal/releasemeta"
	"math/big"
	"strings"
)

// Subscription is a frozen database projection, retaining null and numeric/string
// distinctions. Domain/site resolution and library facts are explicit inputs.
type Subscription struct{ Fields map[string]any }

func NewSubscription(fields map[string]any) *Subscription {
	return &Subscription{deep(fields).(map[string]any)}
}
func (s *Subscription) Get(k string) any {
	if s == nil {
		return nil
	}
	return s.Fields[k]
}
func (s *Subscription) Words() (*[]string, error) {
	v := s.Get("custom_words")
	if !truth(v) {
		return nil, nil
	}
	text, ok := v.(string)
	if !ok {
		return nil, errors.New("AttributeError")
	}
	list := strings.Split(text, "\n")
	return &list, nil
}

type RecognitionRequest struct {
	Phase  string
	API    string
	Meta   *releasemeta.MetaObject
	Kwargs map[string]any
}

// Returned MediaInfo must already be normalized, just as recognition returns a
// constructed source object. No provider HTTP is hidden inside this candidate.
type Recognizer interface {
	Recognize(context.Context, RecognitionRequest) (*MediaInfo, error)
}
type RecognizeFunc func(context.Context, RecognitionRequest) (*MediaInfo, error)

func (f RecognizeFunc) Recognize(ctx context.Context, r RecognitionRequest) (*MediaInfo, error) {
	return f(ctx, r)
}

var bigOne = big.NewInt(1)
var bigThree = big.NewInt(3)

type Engine struct {
	frame      Frame
	parser     *releasemeta.MetaInfoParser
	recognizer Recognizer
}

func NewEngine(frame Frame, recognizer Recognizer) (*Engine, error) {
	p, e := releasemeta.NewMetaInfoParser(frame.Global)
	if e != nil {
		return nil, e
	}
	if recognizer == nil {
		return nil, errors.New("recognizer_required")
	}
	return &Engine{cloneFrame(frame), p, recognizer}, nil
}
func (e *Engine) recognize(ctx context.Context, phase, api string, meta *releasemeta.MetaObject, kwargs map[string]any) (*MediaInfo, error) {
	return e.recognizer.Recognize(ctx, RecognitionRequest{phase, api, meta, kwargs})
}
func torrentTitle(t *TorrentInfo) (string, *string, error) {
	title, ok := t.Get("title").(string)
	if !ok {
		return "", nil, errors.New("torrent_title_string_required")
	}
	var subtitle *string
	if v := t.Get("description"); v != nil {
		s, ok := v.(string)
		if !ok {
			return "", nil, errors.New("torrent_description_string_required")
		}
		subtitle = &s
	}
	return title, subtitle, nil
}
func (e *Engine) Produce(ctx context.Context, torrent *TorrentInfo, stype string) (*Context, error) {
	if torrent == nil {
		return nil, errors.New("torrent_required")
	}
	if !truth(torrent.Get("enclosure")) {
		return nil, nil
	}
	title, subtitle, err := torrentTitle(torrent)
	if err != nil {
		return nil, err
	}
	meta, err := e.parser.ParseTitle(title, subtitle, nil)
	if err != nil {
		return nil, err
	}
	if meta.Get("type") != "电视剧" && torrent.Get("category") == "电视剧" {
		meta.Set("type", "电视剧")
	}
	media, err := e.recognize(ctx, "produce", "recognize_by_meta", meta, map[string]any{"obtain_images": false})
	if err != nil {
		return nil, err
	}
	if media == nil {
		media, _ = NewMediaInfo(nil)
	}
	media.Clear()
	c := NewContext(meta, media, torrent, e.frame)
	source := "rss"
	if stype == "spider" {
		source = "spider"
	}
	c.Set("resource_source", source)
	id := ResolveIdentity(media)
	recognized := id.Source != "" && id.ID != ""
	c.Set("candidate_recognized", recognized)
	if recognized {
		c.Set("match_source", MediaMatchSource(media))
	} else {
		c.Set("media_recognize_fail_count", 1)
	}
	return c, nil
}

// Preflight mutates the shared cache, whereas MatchSubscription only shallow
// copies its container. A present empty MediaInfo is a successful recognizer call
// and resets retries even when its identity remains missing.
func (e *Engine) Preflight(ctx context.Context, c *Context) error {
	if c == nil {
		return errors.New("context_required")
	}
	count := numericRat(c.Get("media_recognize_fail_count"))
	if count == nil {
		return errors.New("TypeError")
	}
	if c.Media != nil && ResolveIdentity(c.Media).ID != "" {
		return nil
	}
	if count.Cmp(new(big.Rat).SetInt(bigThree)) < 0 {
		m, err := e.recognize(ctx, "preflight", "recognize_by_meta", c.Meta, map[string]any{"obtain_images": false})
		if err != nil {
			return err
		}
		if m != nil {
			m.Clear()
			c.Media = m
			c.Set("match_source", MediaMatchSource(m))
			c.Set("candidate_recognized", ResolveIdentity(m).ID != "")
			c.Set("media_info_is_target", false)
			c.Set("media_recognize_fail_count", 0)
		} else {
			if integral := number(c.Get("media_recognize_fail_count")); integral != nil {
				c.Set("media_recognize_fail_count", integral.Add(integral, bigOne))
			} else {
				f, _ := count.Float64()
				c.Set("media_recognize_fail_count", f+1)
			}
		}
	}
	return nil
}

type Prepared struct {
	Subscription *Subscription
	Meta         *releasemeta.MetaObject
	Media        *MediaInfo
	Domains      []string
	Sites        []any
}

func (e *Engine) PrepareSubscription(ctx context.Context, s *Subscription) (*Prepared, error) {
	if s == nil {
		return nil, errors.New("subscription_required")
	}
	title, ok := s.Get("name").(string)
	if !ok {
		return nil, errors.New("subscription_name_string_required")
	}
	m, err := e.parser.ParseTitle(title, nil, nil)
	if err != nil {
		return nil, err
	}
	typ := s.Get("type")
	if typ != "电影" && typ != "电视剧" && typ != "未知" {
		return nil, errors.New("ValueError")
	}
	for _, key := range []string{"year", "tmdbid", "doubanid", "bangumiid", "anilistid", "media_source", "media_id"} {
		m.Set(key, deep(s.Get(key)))
	}
	m.Set("begin_season", deep(s.Get("season")))
	m.Set("type", typ)
	identity := ResolveIdentity(s)
	var source, id any
	if identity.Source != "" {
		source = identity.Source
	}
	if identity.ID != "" {
		id = identity.ID
	}
	kwargs := map[string]any{"mtype": typ, "source": source, "mediaid": id, "episode_group": s.Get("episode_group"), "cache": false}
	for _, key := range []string{"tmdbid", "doubanid", "bangumiid", "anilistid"} {
		kwargs[key] = s.Get(key)
	}
	media, err := e.recognize(ctx, "target", "recognize_media", m, kwargs)
	if err != nil {
		return nil, err
	}
	return &Prepared{Subscription: s, Meta: m, Media: media}, nil
}

// ClearTargetAfterLibraryCheck intentionally occurs after the caller has checked
// existing-library facts. Source clear removes aliases and season episode maps.
func (p *Prepared) ClearTargetAfterLibraryCheck() {
	if p.Media != nil {
		p.Media.Clear()
	}
}

type MatchFacts struct {
	MissingEpisodes        []any
	MissingEpisodesPresent bool
}
type MatchResult struct {
	Context      *Context
	Reason       string
	Rebuilt      bool
	AppliedWords []string
	NextStage    string
}

// IdentityCandidate is the actual cachedContext prefix through missing-episode
// intersection. It is NOT approval to download. Rule filters, best-version
// coverage/priority and library lifecycle must run via the explicit next stage.
func (e *Engine) IdentityCandidate(ctx context.Context, cache *Context, p *Prepared, domain string, facts MatchFacts) (MatchResult, error) {
	out := MatchResult{Reason: "rejected", AppliedWords: []string{}}
	if cache == nil || p == nil || p.Subscription == nil || p.Meta == nil || p.Media == nil {
		return out, errors.New("matching_objects_required")
	}
	for _, d := range p.Domains {
		if d == domain {
			goto domainOK
		}
	}
	if len(p.Domains) > 0 {
		out.Reason = "domain"
		return out, nil
	}
domainOK:
	c := cache.ShallowCopy()
	out.Context = c
	localWords, wordErr := p.Subscription.Words()
	if wordErr != nil {
		return out, wordErr
	}
	c.Selection = new(LocalFrame)
	*c.Selection = localFrame(localWords, e.frame)
	c.Selection.Subscription = deep(p.Subscription.Fields).(map[string]any)
	c.Selection.Sites = deep(p.Sites).([]any)
	c.Selection.Domains = append(make([]string, 0, len(p.Domains)), p.Domains...)
	if len(p.Sites) > 0 {
		found := false
		for _, site := range p.Sites {
			if pyEqual(site, c.Torrent.Get("site")) {
				found = true
				break
			}
		}
		if !found {
			out.Reason = "site"
			return out, nil
		}
	}
	localMeta, localMedia := c.Meta, c.Media
	if list := localWords; list != nil && len(*list) > 0 {
		matcher, err := words.New(*list)
		if err != nil {
			return out, err
		}
		probe := matcher.Prepare(localMeta.Text("org_string"))
		if probe.Limited {
			return out, errors.New("rss_words_probe_limit")
		}
		for _, index := range probe.Applied {
			out.AppliedWords = append(out.AppliedWords, (*list)[index])
		}
		if len(probe.Applied) > 0 {
			title, subtitle, err := torrentTitle(c.Torrent)
			if err != nil {
				return out, err
			}
			localMeta, err = e.parser.ParseTitle(title, subtitle, list)
			if err != nil {
				return out, err
			}
			c.Meta = localMeta
			c.MetaFrame = cloneFrame(e.frame)
			out.Rebuilt = true
			localMedia, err = e.recognize(ctx, "local_rebuild", "recognize_by_meta", localMeta, map[string]any{"episode_group": p.Subscription.Get("episode_group"), "obtain_images": false})
			if err != nil {
				return out, err
			}
			if localMedia != nil {
				localMedia.Clear()
				c.Media = localMedia
				c.Set("match_source", MediaMatchSource(localMedia))
				c.Set("candidate_recognized", ResolveIdentity(localMedia).ID != "")
				c.Set("media_info_is_target", false)
			}
		}
	}
	if localMedia == nil || ResolveIdentity(localMedia).ID == "" {
		matched, err := MatchTorrent(p.Media, localMeta, c.Torrent)
		if err != nil {
			return out, err
		}
		if !matched {
			out.Reason = "title"
			return out, nil
		}
		localMedia = p.Media
		c.Media = p.Media
		c.Set("match_source", "title")
		c.Set("candidate_recognized", false)
		c.Set("media_info_is_target", true)
	}
	if ResolveIdentity(localMedia).ID == "" {
		out.Reason = "identity_missing"
		return out, nil
	}
	if !pyEqual(localMedia.Get("type"), p.Media.Get("type")) {
		out.Reason = "media_type"
		return out, nil
	}
	for _, key := range []string{"tmdb_id", "douban_id"} {
		if truth(localMedia.Get(key)) && !pyEqual(localMedia.Get(key), p.Media.Get(key)) {
			out.Reason = key
			return out, nil
		}
	}
	if c.Get("match_source") == "title" {
		c.Set("candidate_recognized", false)
		c.Set("media_info_is_target", true)
	} else {
		if c.Get("match_source") == "unknown" {
			c.Set("match_source", MediaMatchSource(localMedia))
		}
		c.Set("candidate_recognized", true)
		c.Set("media_info_is_target", false)
	}
	if localMedia.Get("type") == "电视剧" {
		derived, err := localMeta.Derived()
		if err != nil {
			return out, err
		}
		if len(stringSlice(derived["season_list"])) > 1 {
			out.Reason = "multiple_seasons"
			return out, nil
		}
		begin := localMeta.Get("begin_season")
		if begin != nil {
			if !pyEqual(p.Meta.Get("begin_season"), begin) {
				out.Reason = "season"
				return out, nil
			}
		} else if !pyEqual(p.Meta.Get("begin_season"), 1) {
			out.Reason = "season"
			return out, nil
		}
		if !truth(p.Subscription.Get("best_version")) && facts.MissingEpisodesPresent && len(facts.MissingEpisodes) > 0 {
			episodes := stringSlice(derived["episode_list"])
			if len(episodes) > 0 {
				found := false
				for _, x := range facts.MissingEpisodes {
					for _, y := range episodes {
						if pyEqual(x, y) {
							found = true
							break
						}
					}
				}
				if !found {
					out.Reason = "missing_episodes"
					return out, nil
				}
			}
		}
	}
	out.Reason = "identity_candidate"
	out.NextStage = "subscription_rules_and_best_version"
	return out, nil
}
func PluginCandidates(cached []*Context, s *Subscription, allowTitle bool, stype string) ([]*Context, error) {
	out := []*Context{}
	for _, c := range cached {
		if c == nil {
			continue
		}
		c.EnsureCompatibility(stype)
		copy := c.DeepCopy()
		if PluginStrictMatch(copy, s) {
			out = append(out, copy)
			continue
		}
		if allowTitle {
			ok, err := PluginTitleMatch(copy, s)
			if err != nil {
				return nil, err
			}
			if ok {
				m, err := NewMediaInfo(map[string]any{"type": s.Get("type"), "title": s.Get("name"), "tmdb_id": s.Get("tmdbid"), "douban_id": s.Get("doubanid"), "bangumi_id": s.Get("bangumiid"), "anilist_id": s.Get("anilistid"), "source": s.Get("media_source"), "season": s.Get("season")})
				if err != nil {
					return nil, err
				}
				copy.Media = m
				copy.Set("match_source", "title")
				copy.Set("candidate_recognized", false)
				copy.Set("media_info_is_target", true)
				out = append(out, copy)
			}
		}
	}
	return out, nil
}
