package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/acquisitionbatch"
	"light-media/internal/catalog"
	"light-media/internal/metainfo"
	"light-media/internal/priorityrules"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

type larkBatchTorrent struct {
	Ref, Hash, Failure string
	Episodes           []int
}

func larkNativeNumbers(values []*big.Int) ([]int, error) {
	out := []int{}
	for _, value := range values {
		if value == nil || !value.IsInt64() || value.Sign() < 0 || value.Int64() > int64(^uint(0)>>1) {
			return nil, bad("INVALID_SOURCE_METADATA", "原资源季集超出本机整数范围")
		}
		out = append(out, int(value.Int64()))
	}
	return out, nil
}

func larkMetaNumbers(meta *releasemeta.MetaObject) ([]int, []int, error) {
	seasons, e := meta.SeasonList()
	if e != nil {
		return nil, nil, e
	}
	s, e := larkNativeNumbers(seasons)
	if e != nil {
		return nil, nil, e
	}
	episodes, e := meta.EpisodeList()
	if e != nil {
		return nil, nil, e
	}
	eps, e := larkNativeNumbers(episodes)
	return s, eps, e
}

// Sort against each original cached MetaInfo, not a later title reparse. The
// installed source applies current priority/site statistics on every batch.
func (a *App) larkSortCandidateMetas(ctx context.Context, candidates []Candidate, metas []json.RawMessage, media catalog.Media) ([]Candidate, []json.RawMessage, error) {
	if len(candidates) != len(metas) {
		return nil, nil, larkEvidenceError()
	}
	sorter, e := a.preparePrioritySorter(ctx, SearchRequest{priorityMedia: &media}, a.Settings())
	if e != nil {
		return nil, nil, e
	}
	type item struct {
		candidate Candidate
		meta      json.RawMessage
		key       string
	}
	items := make([]item, len(candidates))
	for i, candidate := range candidates {
		meta, e := releasemeta.DecodeMetaObjectState(metas[i])
		if e != nil {
			return nil, nil, e
		}
		seasons, episodes, e := larkMetaNumbers(meta)
		if e != nil {
			return nil, nil, e
		}
		r := candidate.Resource
		items[i] = item{candidate, metas[i], priorityrules.SortKey(priorityrules.SortInput{Title: media.Title, SiteOrder: sorter.orders[r.SiteID], Upload: sorter.uploads[sorter.names[r.SiteID]], Priority: r.PriorityOrder, Seeders: r.Seeders, Seasons: len(seasons), Episodes: len(episodes)}, sorter.priorities)}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].key > items[j].key })
	out, states := make([]Candidate, len(items)), make([]json.RawMessage, len(items))
	for i, item := range items {
		out[i], states[i] = item.candidate, item.meta
	}
	return out, states, nil
}

func larkBatchFingerprint(candidate Candidate, media catalog.Media, meta *releasemeta.MetaObject) string {
	kind := "电影"
	if media.MediaType == "tv" {
		kind = "电视剧"
	}
	values := map[string]string{"episodes": meta.Episode(), "media_key": "themoviedb:" + strconv.FormatInt(media.ID, 10), "media_type": kind, "resource": candidate.Resource.ID, "season": meta.Season(), "site": candidate.Resource.SiteID}
	keys := []string{"episodes", "media_key", "media_type", "resource", "season", "site"}
	var text strings.Builder
	text.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			text.WriteString(", ")
		}
		encode := func(value string) string {
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(value)
			return strings.TrimSuffix(b.String(), "\n")
		}
		text.WriteString(encode(key))
		text.WriteString(": ")
		text.WriteString(encode(values[key]))
	}
	text.WriteByte('}')
	return tokenHash(text.String())
}

func larkBatchNeeds(facts pluginLarkMediaTargets) []acquisitionbatch.MediaNeed {
	if facts.Media.MediaType != "tv" {
		return nil
	}
	out := acquisitionbatch.MediaNeed{MediaKey: mediaKey(facts.Media), Seasons: []acquisitionbatch.Need{}}
	for _, request := range facts.Requests {
		known, missing := facts.Known[request.Season], facts.Missing[request.Season]
		if facts.Exists {
			missing = known
		}
		if len(missing) == 0 {
			continue
		}
		start := missing[0]
		for _, ep := range missing {
			if ep < start {
				start = ep
			}
		}
		episodes := append([]int{}, missing...)
		if len(missing) == len(known) {
			episodes = []int{}
		}
		out.Seasons = append(out.Seasons, acquisitionbatch.Need{Season: request.Season, Episodes: episodes, TotalEpisode: len(known), StartEpisode: start})
	}
	if len(out.Seasons) == 0 {
		return nil
	}
	return []acquisitionbatch.MediaNeed{out}
}

func (a *App) larkBatchTorrent(ctx context.Context, core *pluginLarkCoreExecution, candidate Candidate, meta json.RawMessage) (larkBatchTorrent, error) {
	return larkCoreRead(ctx, a, core, "batch.torrent", struct {
		Candidate Candidate
		Meta      json.RawMessage
	}{candidate, meta}, func(ctx context.Context) (larkBatchTorrent, error) {
		out := larkBatchTorrent{Episodes: []int{}}
		site, e := a.site(candidate.Resource.SiteID)
		if e != nil {
			out.Failure = pluginErrorCodeInternal(e)
			return out, nil
		}
		var raw []byte
		if e = a.withSite(ctx, site, false, func(c *sites.Client) error { var e error; raw, e = c.Torrent(ctx, candidate.Resource.ID); return e }); e != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Failure = pluginErrorCodeInternal(e)
			return out, nil
		}
		if len(raw) == 0 {
			return out, nil
		}
		material, e := metainfo.ParseMaterial(raw)
		if e != nil {
			out.Failure = "INVALID_TORRENT"
			return out, nil
		}
		options, e := a.globalRecognitionOptions(ctx)
		if e != nil {
			return out, e
		}
		parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
		if e != nil {
			return out, e
		}
		extensions := options.MediaExts
		if extensions == nil {
			extensions = removeLinkMediaExtensions()
		}
		for _, file := range material.Files {
			ext := strings.ToLower(filepath.Ext(file.Path))
			valid := false
			for _, configured := range extensions {
				if ext == strings.ToLower(configured) {
					valid = true
					break
				}
			}
			if !valid || ext == "" {
				continue
			}
			meta, e := parser.ParseTitle(filepath.Base(file.Path), nil, nil)
			if e != nil {
				return out, e
			}
			if n := meta.Number("begin_episode"); n == nil || n.Sign() == 0 {
				continue
			}
			_, eps, e := larkMetaNumbers(meta)
			if e != nil {
				return out, e
			}
			for _, ep := range eps {
				if !containsInt(out.Episodes, ep) {
					out.Episodes = append(out.Episodes, ep)
				}
			}
		}
		out.Hash = material.Hash
		out.Ref, e = a.persistMaterial(ctx, raw, material.Hash)
		return out, e
	})
}

func (a *App) larkRunAcquisitionBatch(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, facts pluginLarkMediaTargets, dirs larkDownloadDirectorySnapshot, explicit *string) (acquisitionbatch.Result, error) {
	var empty acquisitionbatch.Result
	if s.Selected == nil || len(s.Candidates) != len(s.CandidateMetas) {
		return empty, larkEvidenceError()
	}
	byID := map[string]Candidate{}
	metas := map[string]*releasemeta.MetaObject{}
	materialRefs := map[string]string{}
	candidates := []*acquisitionbatch.Candidate{}
	for i, candidate := range s.Candidates {
		id := strconv.Itoa(i)
		meta, e := releasemeta.DecodeMetaObjectState(s.CandidateMetas[i])
		if e != nil {
			return empty, e
		}
		seasons, episodes, e := larkMetaNumbers(meta)
		if e != nil {
			return empty, e
		}
		byID[id], metas[id] = candidate, meta
		candidates = append(candidates, &acquisitionbatch.Candidate{ID: id, Kind: facts.Media.MediaType, MediaKey: mediaKey(facts.Media), TitleYear: larkMediaTitleYear(facts.Media), Seasons: seasons, Episodes: episodes})
	}
	return acquisitionbatch.Run(ctx, candidates, larkBatchNeeds(facts), acquisitionbatch.Dependencies{
		Sort: func(ctx context.Context, values []*acquisitionbatch.Candidate) ([]*acquisitionbatch.Candidate, error) {
			order, e := larkCoreRead(ctx, a, core, "batch.sort", struct {
				Candidates []Candidate
				Metas      []json.RawMessage
			}{s.Candidates, s.CandidateMetas}, func(ctx context.Context) ([]string, error) {
				ordered, states, e := a.larkSortCandidateMetas(ctx, s.Candidates, s.CandidateMetas, facts.Media)
				if e != nil {
					return nil, e
				}
				ids := []string{}
				used := map[string]bool{}
				for i, value := range ordered {
					for index, original := range s.Candidates {
						id := strconv.Itoa(index)
						if !used[id] && value.Resource.SiteID == original.Resource.SiteID && value.Resource.ID == original.Resource.ID && larkSameRaw(states[i], s.CandidateMetas[index]) {
							ids = append(ids, id)
							used[id] = true
							break
						}
					}
				}
				if len(ids) != len(values) {
					return nil, larkEvidenceError()
				}
				return ids, nil
			})
			if e != nil {
				return nil, e
			}
			out := make([]*acquisitionbatch.Candidate, 0, len(order))
			for _, id := range order {
				n, e := strconv.Atoi(id)
				if e != nil || n < 0 || n >= len(values) {
					return nil, larkEvidenceError()
				}
				out = append(out, values[n])
			}
			return out, nil
		},
		// Lark is not a Subscribe| source: original DB failure cooldown lookup
		// and recording are disabled, but in-batch dynamic fingerprints remain.
		Fingerprint: func(c *acquisitionbatch.Candidate) string {
			meta := metas[c.ID]
			if len(c.Episodes) > 0 {
				_ = meta.SetEpisodes(c.Episodes[0], c.Episodes[len(c.Episodes)-1])
			}
			return larkBatchFingerprint(byID[c.ID], facts.Media, meta)
		},
		Torrent: func(ctx context.Context, c *acquisitionbatch.Candidate) (acquisitionbatch.Material, error) {
			raw, e := metas[c.ID].EncodeState()
			if e != nil {
				return acquisitionbatch.Material{}, e
			}
			probe, e := a.larkBatchTorrent(ctx, core, byID[c.ID], raw)
			if e != nil || probe.Ref == "" {
				return acquisitionbatch.Material{}, e
			}
			content, e := a.resolveMaterial(ctx, probe.Ref, materialPurpose)
			if e != nil {
				return acquisitionbatch.Material{}, e
			}
			materialRefs[c.ID] = probe.Ref
			return acquisitionbatch.Material{Content: content, Episodes: probe.Episodes}, nil
		},
		Single: func(ctx context.Context, d acquisitionbatch.Download) (bool, error) {
			c := d.Candidate
			multi := []SearchRequest{}
			if len(c.Seasons) > 1 {
				if d.Episodes != nil {
					return false, seasonScopeError()
				}
				for _, season := range c.Seasons {
					found := false
					for _, r := range facts.Requests {
						if r.Season == season {
							r.Episodes = qualityCopyNote(facts.Known[season])
							multi = append(multi, r)
							found = true
							break
						}
					}
					if !found {
						return false, seasonScopeError()
					}
				}
			}
			candidate := byID[c.ID]
			candidate.Episodes = append([]int{}, c.Episodes...)
			if facts.Media.MediaType == "tv" {
				candidate.Season = 1
				if len(c.Seasons) > 0 {
					candidate.Season = c.Seasons[0]
				}
				if len(candidate.Episodes) == 0 {
					candidate.Episodes = append([]int{}, facts.Known[candidate.Season]...)
				}
			}
			var selected []int
			if d.Episodes != nil {
				selected = append([]int{}, (*d.Episodes)...)
				candidate.Episodes = append([]int{}, selected...)
			}
			meta := metas[c.ID]
			if len(c.Episodes) > 0 {
				if e := meta.SetEpisodes(c.Episodes[0], c.Episodes[len(c.Episodes)-1]); e != nil {
					return false, e
				}
			}
			raw, e := meta.EncodeState()
			if e != nil {
				return false, e
			}
			var basis json.RawMessage
			if len(multi) > 0 {
				basis = mustCleanSeedJSON(downloadSeasonBasis{Version: 1, Mode: "whole_missing", SiteID: candidate.Resource.SiteID, ResourceID: candidate.Resource.ID, SourceMeta: raw, Targets: facts})
			}
			accepted, e := a.larkDownloadCandidateSeasons(ctx, core, s, candidate, dirs, explicit, selected, materialRefs[c.ID], raw, multi, basis)
			if e != nil {
				if larkKnownP0Failure(e) {
					return false, nil
				}
				var api *APIError
				if errors.As(e, &api) && (strings.HasPrefix(api.Code, "RESOURCE_") || strings.HasPrefix(api.Code, "REVIEW_TORRENT_") || api.Code == "INVALID_TORRENT" || api.Code == "NO_MEDIA_FILES") {
					return false, nil
				}
				return false, e
			}
			return accepted.State == "accepted", nil
		},
	})
}

func (a *App) larkAutoDownloadMedia(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, dirs larkDownloadDirectorySnapshot, explicit *string) error {
	if s.Selected == nil {
		return larkEvidenceError()
	}
	var facts pluginLarkMediaTargets
	var e error
	if s.PendingNoExists != nil {
		facts = *s.PendingNoExists
		s.PendingNoExists = nil
		s, e = a.larkSaveMediaSession(ctx, core, *s)
		if e != nil {
			return e
		}
	} else {
		facts, e = a.larkMediaTargets(ctx, core, s, *s.Selected)
		if e != nil {
			return e
		}
	}
	out, e := a.larkRunAcquisitionBatch(ctx, core, s, facts, dirs, explicit)
	if e != nil {
		return e
	}
	if len(out.Downloaded) > 0 && len(out.Remaining) == 0 {
		return nil
	}
	return a.larkSubscribeBatchRemainder(ctx, core, s, facts, out)
}
