package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

type priorityFilter struct {
	engine    *priorityrules.Engine
	names     []string
	media     *priorityrules.Media
	siteNames map[string]string
}

func (a *App) preparePriorityFilter(ctx context.Context, in SearchRequest, settings Settings) (priorityFilter, error) {
	filter := priorityFilter{}
	if err := validateSubscriptionPolicy(in.Policy); err != nil {
		return filter, err
	}
	config := settings.PriorityRules
	if config == nil {
		return filter, nil
	}
	filter.names = append([]string{}, in.Policy.FilterGroups...)
	if len(filter.names) == 0 {
		if in.prioritySubscription {
			filter.names = append([]string{}, config.SubscribeGroups...)
		} else {
			filter.names = append([]string{}, config.SearchGroups...)
		}
	}
	if len(filter.names) == 0 {
		return filter, nil
	}
	engine, err := priorityrules.New(*config)
	if err != nil {
		return filter, bad("INVALID_PRIORITY_RULES", "优先级规则配置无法读取")
	}
	filter.engine = engine
	filter.siteNames = map[string]string{}
	for _, site := range settings.Sites {
		filter.siteNames[site.ID] = site.Name
	}
	if in.priorityMedia != nil {
		identity := *in.priorityMedia
		var category *string
		for _, group := range config.Groups {
			if group.MediaType == nil || *group.MediaType == "" {
				continue
			}
			selected := false
			for _, name := range filter.names {
				if name == group.Name {
					selected = true
					break
				}
			}
			if !selected {
				continue
			}
			fresh, name, err := a.importMediaCategory(ctx, identity)
			if err != nil {
				return filter, err
			}
			identity = fresh
			if name != "" {
				category = &name
			}
			break
		}
		filter.media = priorityMedia(identity, category)
	}
	return filter, nil
}
func priorityMedia(identity catalog.Media, category *string) *priorityrules.Media {
	raw, _ := json.Marshal(identity)
	attributes := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	_ = decoder.Decode(&attributes)
	if len(identity.GenreIDs) == 0 && len(identity.Genres) > 0 {
		ids := []any{}
		for _, g := range identity.Genres {
			ids = append(ids, json.Number(fmt.Sprint(g.ID)))
		}
		attributes["genre_ids"] = ids
	}
	typeName := "电影"
	if identity.MediaType == "tv" {
		typeName = "电视剧"
	}
	return &priorityrules.Media{Type: typeName, Category: category, Attributes: attributes}
}
func (filter priorityFilter) apply(ctx context.Context, a *App, out SearchOutput) (SearchOutput, error) {
	if filter.engine == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	torrents := make([]priorityrules.Torrent, len(out.Candidates))
	for i, candidate := range out.Candidates {
		resource := candidate.Resource
		description := resource.SourceSubtitle
		if description == nil {
			value := resource.Subtitle
			description = &value
		}
		age := 0.0
		if at, err := time.Parse(time.RFC3339, resource.PublishedAt); err == nil {
			age = math.Floor(time.Since(at).Minutes())
		} else if at, err := time.ParseInLocation("2006-01-02 15:04:05", resource.PublishedAt, shanghai()); err == nil {
			age = math.Floor(time.Since(at).Minutes())
		}
		torrents[i] = priorityrules.Torrent{Title: resource.Title, Description: description, Labels: resource.Labels, Size: resource.Size, Seeders: resource.Seeders, DownloadFactor: resource.DownloadFactor, AgeMinutes: age, Priority: resource.PriorityOrder, SiteName: filter.siteNames[resource.SiteID], EpisodeResolver: func() (int, error) { return a.priorityEpisodeCount(resource) }}
	}
	matches, err := filter.engine.Filter(ctx, filter.names, torrents, filter.media)
	if err != nil {
		out.Candidates = []Candidate{}
		message := "优先级规则匹配失败"
		var diagnostic *priorityrules.Diagnostic
		if errors.As(err, &diagnostic) {
			message += fmt.Sprintf("：规则组 %q，优先级 %d", diagnostic.Group, diagnostic.Tier)
			if diagnostic.Rule != "" {
				message += fmt.Sprintf("，规则 %q", diagnostic.Rule)
			}
		}
		message += "；请检查表达式、条件格式或资源集数"
		a.log("ERROR", "rules", "priority_rule_failed", message, out.WorkflowID, "")
		return out, bad("PRIORITY_RULE_FAILED", message)
	}
	filtered := make([]Candidate, 0, len(matches))
	for _, match := range matches {
		candidate := out.Candidates[match.Index]
		candidate.Resource.PriorityOrder = match.Priority
		filtered = append(filtered, candidate)
	}
	out.Candidates = filtered
	return out, nil
}

var priorityEpisodeHint = regexp.MustCompile(`(?i)(?:S[0-9]{1,2}E|(?:^|[^A-Za-z])EP?\s*[0-9]+|第[^\s]{1,12}[集话])`)

func (a *App) priorityEpisodeCount(resource sites.Resource) (int, error) {
	name := resource.Title
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".mov":
	default:
		name += ".mkv"
	}
	parsed := a.parseMediaName(name)
	if parsed.Status == media.Parsed && parsed.Original != nil {
		if n := len(parsed.Original.Episodes); n > 0 {
			return n, nil
		}
		return 1, nil
	}
	if priorityEpisodeHint.MatchString(resource.Title) {
		return 0, bad("PRIORITY_EPISODES_UNKNOWN", "优先级大小规则无法确认资源集数")
	}
	return 1, nil
}
