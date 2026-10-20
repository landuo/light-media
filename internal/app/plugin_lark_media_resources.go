package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

type pluginLarkMediaTargets struct {
	Media    catalog.Media
	Requests []SearchRequest
	Known    map[int][]int
	Missing  map[int][]int
	Exists   bool
}

func (a *App) larkMediaTargets(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, m catalog.Media) (pluginLarkMediaTargets, error) {
	return larkCoreRead(ctx, a, core, "media_missing", struct {
		Meta  []byte
		Media catalog.Media
	}{s.Meta, m}, func(ctx context.Context) (pluginLarkMediaTargets, error) {
		out := pluginLarkMediaTargets{Known: map[int][]int{}, Missing: map[int][]int{}}
		meta, e := releasemeta.DecodeMetaObjectState(s.Meta)
		if e != nil {
			return out, e
		}
		out.Media, e = a.media(ctx, m.MediaType, m.ID)
		if e != nil {
			return out, e
		}
		seasons := []int{0}
		if m.MediaType == "tv" {
			seasons = nil
			if n := meta.Number("begin_season"); n != nil {
				if !n.IsInt64() || n.Sign() < 0 || n.Int64() > int64(^uint(0)>>1) {
					return out, bad("INVALID_SEASON", "标准季号超出元数据范围")
				}
				seasons = []int{int(n.Int64())}
			} else {
				for _, season := range out.Media.Seasons {
					if season.SeasonNumber > 0 {
						seasons = append(seasons, season.SeasonNumber)
					}
				}
			}
		}
		total, occupied := 0, 0
		for _, season := range seasons {
			known := []int{0}
			if m.MediaType == "tv" {
				value, e := a.season(ctx, m.ID, season)
				if e != nil {
					return out, e
				}
				known = nil
				for _, ep := range value.Episodes {
					known = append(known, ep.EpisodeNumber)
				}
			}
			out.Known[season] = known
			existing := map[int]bool{}
			rows, e := a.Store.DB.QueryContext(ctx, `SELECT episode FROM asset_slots WHERE media_key=? AND season=?`, mediaKey(m), season)
			if e != nil {
				return out, e
			}
			for rows.Next() {
				var ep int
				if e = rows.Scan(&ep); e != nil {
					rows.Close()
					return out, e
				}
				existing[ep] = true
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return out, e
			}
			for _, ep := range known {
				total++
				if existing[ep] {
					occupied++
				} else {
					out.Missing[season] = append(out.Missing[season], ep)
				}
			}
			target := SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Season: season}
			if m.MediaType == "tv" {
				target.Episodes = append([]int{}, known...)
			}
			out.Requests = append(out.Requests, target)
		}
		out.Exists = total > 0 && occupied == total
		return out, nil
	})
}
func larkSourceSeries(values []int) string {
	if len(values) == 0 {
		return ""
	}
	values = append([]int(nil), values...)
	sort.Ints(values)
	parts := []string{}
	start, end := values[0], values[0]
	add := func() {
		if start == end {
			parts = append(parts, strconv.Itoa(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, end))
		}
	}
	for _, n := range values[1:] {
		if n == end+1 {
			end = n
		} else {
			add()
			start, end = n, n
		}
	}
	add()
	return strings.Join(parts, ",")
}
func (a *App) larkSelectMedia(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, index *big.Int, user, message, chat string) error {
	start, end, page, _ := larkMediaPage(len(s.Medias), s.Page)
	s.Page = page
	if s.Phase != "media" || index == nil || !index.IsInt64() || index.Sign() <= 0 || index.Int64() > int64(end-start) {
		return a.larkCoreReply(ctx, core, user, chat, "输入有误！", "")
	}
	m := s.Medias[start+int(index.Int64())-1]
	s.Selected = &m
	s, e := a.larkSaveMediaSession(ctx, core, *s)
	if e != nil {
		return e
	}
	facts, e := a.larkMediaTargets(ctx, core, s, m)
	if e != nil {
		return e
	}
	meta, e := releasemeta.DecodeMetaObjectState(s.Meta)
	if e != nil {
		return e
	}
	if s.Action == "Subscribe" || s.Action == "ReSubscribe" {
		if s.Action != "ReSubscribe" && facts.Exists {
			return a.larkCoreReply(ctx, core, user, chat, "【"+larkMediaTitleYear(m)+meta.Season()+" 媒体库中已存在，如需洗版请发送：洗版 XXX】", "")
		}
		return a.larkSubscribeSelected(ctx, core, s, facts, user, chat)
	}
	if facts.Exists && s.Action == "Search" {
		return a.larkCoreReply(ctx, core, user, chat, "【"+larkMediaTitleYear(m)+meta.Season()+" 媒体库中已存在，如需重新下载请发送：搜索 名称 或 下载 名称】", "")
	}
	messages := []string{}
	for _, request := range facts.Requests {
		if m.MediaType != "tv" {
			continue
		}
		if s.Action == "Search" && !facts.Exists {
			missing := facts.Missing[request.Season]
			if len(missing) == 0 {
				continue
			}
			text := larkSourceSeries(missing)
			if len(missing) == len(facts.Known[request.Season]) {
				text = strconv.Itoa(len(missing))
			}
			messages = append(messages, fmt.Sprintf("第 %d 季缺失 %s 集", request.Season, text))
		} else {
			messages = append(messages, fmt.Sprintf("第 %d 季总 %d 集", request.Season, len(facts.Known[request.Season])))
		}
	}
	if len(messages) > 0 {
		if e = a.larkCoreReply(ctx, core, user, chat, larkMediaTitleYear(m)+"：\n"+strings.Join(messages, "\n"), ""); e != nil {
			return e
		}
	}
	kind := "电影"
	if m.MediaType == "tv" {
		kind = "电视剧"
	}
	if e = a.larkCoreReply(ctx, core, user, chat, "开始搜索 "+kind+" "+larkMediaTitleYear(m)+" ...", ""); e != nil {
		return e
	}
	type result struct {
		Candidates []Candidate
		Metas      []json.RawMessage
		Names      map[string]string
		Requests   []SearchRequest
	}
	found, e := larkCoreRead(ctx, a, core, "resource_search", struct {
		Facts  pluginLarkMediaTargets
		Action string
	}{facts, s.Action}, func(ctx context.Context) (result, error) {
		out := result{Names: map[string]string{}}
		ctx, e := a.ensureResourceRecognition(ctx)
		if e != nil {
			return out, e
		}
		parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: ctx.Value(resourceRecognitionKey{}).(resourceRecognition).options})
		if e != nil {
			return out, e
		}
		seen := map[string]bool{}
		for _, site := range a.Settings().Sites {
			out.Names[site.ID] = site.Name
		}
		for _, request := range facts.Requests {
			if m.MediaType == "tv" && s.Action == "Search" && !facts.Exists {
				request.Episodes = append([]int{}, facts.Missing[request.Season]...)
				if len(request.Episodes) == 0 {
					continue
				}
			}
			resources, e := a.search(ctx, request, false)
			if e != nil {
				return out, e
			}
			out.Requests = append(out.Requests, request)
			for _, candidate := range resources.Candidates {
				if candidate.Accepted {
					key := candidate.Resource.SiteID + ":" + candidate.Resource.ID
					if seen[key] {
						continue
					}
					subtitle := candidate.Resource.SourceSubtitle
					if subtitle == nil && candidate.Resource.Subtitle != "" {
						text := candidate.Resource.Subtitle
						subtitle = &text
					}
					meta, e := parser.ParseTitle(candidate.Resource.Title, subtitle, nil)
					if e != nil {
						return out, e
					}
					raw, e := meta.EncodeState()
					if e != nil {
						return out, e
					}
					seen[key] = true
					out.Candidates = append(out.Candidates, candidate)
					out.Metas = append(out.Metas, raw)
				}
			}
		}
		// The same native TorrentHelper priority projection used by the P0 search
		// performs the final cross-season order, preserving site/priorities.
		out.Candidates, out.Metas, e = a.larkSortCandidateMetas(ctx, out.Candidates, out.Metas, facts.Media)
		return out, e
	})
	if e != nil {
		return e
	}
	if len(found.Candidates) == 0 {
		return a.larkCoreReply(ctx, core, user, chat, m.Title+meta.Season()+" 未搜索到需要的资源！", "")
	}
	s.Phase, s.Page, s.Title = "torrent", 0, m.Title
	s.Candidates = found.Candidates
	s.CandidateMetas = found.Metas
	s.SiteNames = found.Names
	s.Targets = found.Requests
	s.PendingNoExists = nil
	if larkShouldAutoDownload(a.Settings().AutoDownloadUser, user) {
		copy := facts
		s.PendingNoExists = &copy
	}
	s, e = a.larkSaveMediaSession(ctx, core, *s)
	if e != nil {
		return e
	}
	if s.PendingNoExists != nil {
		return a.larkSelectTorrent(ctx, core, s, big.NewInt(0), user, message, chat)
	}
	return a.larkRenderMedia(ctx, core, s, user, message, chat)
}
