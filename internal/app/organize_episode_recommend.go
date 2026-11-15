package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/releasemeta"
	"light-media/internal/transferextensions"
)

type organizeEpisodeRecommendRequest struct {
	Paths      []string `json:"paths"`
	HistoryIDs []string `json:"history_ids"`
}
type organizeEpisodeRecommendResult struct {
	RuleName            string   `json:"rule_name"`
	RuleIndex           int      `json:"rule_index"`
	Pattern             string   `json:"pattern"`
	EpisodeFormat       string   `json:"episode_format"`
	SampleFile          string   `json:"sample_file"`
	MinFileSizeMB       int64    `json:"min_file_size_mb"`
	SampleCount         int      `json:"sample_count"`
	MatchedCount        int      `json:"matched_count"`
	Message             string   `json:"message"`
	MajorityCount       int      `json:"majority_count,omitempty"`
	Confidence          string   `json:"confidence,omitempty"`
	SizeFilterRelaxed   bool     `json:"size_filter_relaxed"`
	NativeVerifiedCount int      `json:"native_verified_count"`
	NativeFallbackCount int      `json:"native_fallback_count"`
	NativeConflictCount int      `json:"native_conflict_count"`
	Reason              string   `json:"reason,omitempty"`
	Reasons             []string `json:"reasons,omitempty"`
}
type organizeEpisodeSample struct {
	path  string
	name  string
	size  int64
	media bool
	kind  string // media, subtitle or audio, from the configured extension classifier.
}

// A single file samples its immediate parent, a directory samples only itself.
// Explicit multi-selection (including history) never broadens to sibling files.
func (a *App) episodeRecommendSamples(ctx context.Context, in organizeEpisodeRecommendRequest) ([]organizeEpisodeSample, error) {
	if len(in.Paths)+len(in.HistoryIDs) == 0 || len(in.Paths) > 2000 || len(in.HistoryIDs) > 1000 || (len(in.Paths) > 0 && len(in.HistoryIDs) > 0) {
		return nil, bad("INVALID_EPISODE_SELECTION", "请选择单个文件、目录或同目录的多个文件；历史选择请仅提交记录ID")
	}
	paths := append([]string(nil), in.Paths...)
	if len(in.HistoryIDs) > 0 {
		histories, err := a.transferHistoryItemsForOrganize(ctx, in.HistoryIDs)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, bad("EPISODE_HISTORY_UNAVAILABLE", "所选整理历史不存在或已变化，请刷新后重试")
			}
			return nil, err
		}
		for _, h := range histories {
			paths = append(paths, h.organizeSource())
		}
	}
	check := func(path string) (browserStamp, error) {
		allowed := a.sourceAllowed(path)
		for _, root := range a.browserRoots() {
			allowed = allowed || (importPath(path) && within(root.Path, path))
		}
		if !allowed || browserInternal(path) {
			return browserStamp{}, bad("INVALID_SOURCE", "推荐样本不在允许的文件目录内")
		}
		stamp, err := browserStat(path)
		if err != nil {
			return browserStamp{}, bad("EPISODE_SOURCE_UNAVAILABLE", "推荐源文件或目录已失效，或包含符号链接/特殊文件")
		}
		return stamp, nil
	}
	unique := []string{}
	seen := map[string]bool{}
	// Validate every selection before deduplication, preserving explicit-batch semantics.
	batch := len(paths) > 1
	parent := ""
	for _, path := range paths {
		stamp, err := check(path)
		if err != nil {
			return nil, err
		}
		if batch {
			if !os.FileMode(stamp.Mode).IsRegular() || (parent != "" && parent != filepath.Dir(path)) {
				return nil, bad("INVALID_EPISODE_SELECTION", "集数定位推荐仅支持同目录的多个普通文件，不支持目录混选或跨目录选择")
			}
			parent = filepath.Dir(path)
		}
		if !seen[path] {
			seen[path] = true
			unique = append(unique, path)
		}
	}
	if !batch {
		path := unique[0]
		stamp, err := check(path)
		if err != nil {
			return nil, err
		}
		if !os.FileMode(stamp.Mode).IsDir() {
			path = filepath.Dir(path)
		}
		if _, err = check(path); err != nil {
			return nil, err
		}
		dir, err := importDir(path, false)
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		unique = nil
		visited := 0
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			entries, readErr := dir.ReadDir(100)
			for _, entry := range entries {
				visited++
				if visited > 20000 {
					return nil, bad("EPISODE_SAMPLE_LIMIT", "目录样本超过上限，请缩小选择范围")
				}
				if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				// browserStat below independently rejects replaced entries and links.
				unique = append(unique, filepath.Join(path, entry.Name()))
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return nil, readErr
			}
		}
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return nil, err
	}
	if !batch {
		sort.Strings(unique)
	}
	samples := []organizeEpisodeSample{}
	for _, path := range unique {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hidden := strings.ReplaceAll(path, "\\", "/")
		if strings.Contains(hidden, "/.") || strings.Contains(hidden, "/@Recycle/") || strings.Contains(hidden, "/#recycle/") || strings.Contains(hidden, "/@eaDir") {
			continue
		}
		file := transferextensions.PathFile(path)
		if !classifier.Allowed(file) {
			continue
		}
		stamp, err := check(path)
		if err != nil {
			return nil, err
		}
		if !os.FileMode(stamp.Mode).IsRegular() {
			continue
		}
		media, _ := classifier.Media(file)
		kind := "audio"
		if media {
			kind = "media"
		} else if classifier.Subtitle(file) {
			kind = "subtitle"
		}
		samples = append(samples, organizeEpisodeSample{path: path, name: filepath.Base(path), size: stamp.Size, media: media, kind: kind})
		if len(samples) > 2000 {
			return nil, bad("EPISODE_SAMPLE_LIMIT", "一次最多使用2000个推荐样本，请缩小选择范围")
		}
	}
	if len(samples) == 0 {
		return nil, bad("EPISODE_NO_SAMPLES", "目录或所选文件中没有可用于推荐的视频、字幕或外挂音频")
	}
	return samples, nil
}

func (a *App) recommendEpisodeFormat(ctx context.Context, in organizeEpisodeRecommendRequest) (organizeEpisodeRecommendResult, error) {
	config, err := a.episodeFormatRules(ctx)
	if err != nil {
		return organizeEpisodeRecommendResult{}, err
	}
	rules := enabledEpisodeRules(config.Rules)
	samples, err := a.episodeRecommendSamples(ctx, in)
	if err != nil {
		return organizeEpisodeRecommendResult{}, err
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return organizeEpisodeRecommendResult{}, err
	}
	native, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return organizeEpisodeRecommendResult{}, err
	}
	if len(rules) > 0 {
		compatibility, err := episodeCompatibilitySamples(ctx, samples, native)
		if err != nil {
			return organizeEpisodeRecommendResult{}, err
		}
		result, ruleErr := recommendEpisodeFromRules(ctx, rules, samples, compatibility)
		if ruleErr == nil {
			return result, nil
		}
		var apiErr *APIError
		if !errors.As(ruleErr, &apiErr) || apiErr.Code != "EPISODE_RULE_NO_MATCH" {
			return organizeEpisodeRecommendResult{}, ruleErr
		}
	}
	return recommendEpisodeAutomatically(ctx, samples, native)
}

type episodeRuleMatch struct {
	sample organizeEpisodeSample
	match  *regexp2.Match
}

func recommendEpisodeFromRules(ctx context.Context, rules []organizeEpisodeRule, samples []organizeEpisodeSample, compatibility []episodeCompatibilitySample) (organizeEpisodeRecommendResult, error) {
	deadline := time.Now().Add(2 * time.Second)
	for index, rule := range rules {
		re, err := compileEpisodeRule(rule)
		if err != nil {
			return organizeEpisodeRecommendResult{}, err
		}
		matches := []episodeRuleMatch{}
		compatible := true
		for _, sample := range samples {
			if err := ctx.Err(); err != nil {
				return organizeEpisodeRecommendResult{}, err
			}
			if time.Now().After(deadline) {
				return organizeEpisodeRecommendResult{}, bad("EPISODE_RULE_LIMIT", "规则匹配超过时间限制，请简化正则或缩小选择范围")
			}
			if sample.media && sample.size < rule.MinFileSizeMB*1024*1024 {
				continue
			}
			match, err := re.FindStringMatch(sample.name)
			if err != nil {
				return organizeEpisodeRecommendResult{}, bad("EPISODE_RULE_LIMIT", "规则“"+rule.Name+"”匹配超过限制，请简化正则")
			}
			if match == nil {
				continue
			}
			matches = append(matches, episodeRuleMatch{sample, match})
		}
		if len(matches) == 0 || !compatible {
			continue
		}
		format := episodeTemplateFromMatch(matches[0].sample.name, matches[0].match, re.GetGroupNames())
		parser, err := organizeEpisodeTemplate(format)
		if err != nil || parser == nil {
			continue
		}
		for _, item := range matches {
			ep := item.match.GroupByName("ep")
			parsed := parser.FindStringSubmatch(item.sample.name)
			if ep == nil || len(parsed) == 0 {
				compatible = false
				break
			}
			expected, err1 := organizeEpisodeRange(ep.String())
			actual, err2 := organizeEpisodeRange(parsed[parser.SubexpIndex("ep")])
			if err1 != nil || err2 != nil || !reflect.DeepEqual(expected, actual) {
				compatible = false
				break
			}
		}
		if !compatible || !validateEpisodeCompatibility(parser, compatibility) {
			continue
		}
		return organizeEpisodeRecommendResult{RuleName: rule.Name, RuleIndex: index, Pattern: rule.Pattern, EpisodeFormat: format, SampleFile: matches[0].sample.name, MinFileSizeMB: rule.MinFileSizeMB, SampleCount: len(samples), MatchedCount: len(matches), Message: "已根据配置规则生成并验证集数定位模板"}, nil
	}
	return organizeEpisodeRecommendResult{}, bad("EPISODE_RULE_NO_MATCH", "已配置规则未命中或生成模板未通过集数及附加文件兼容验证")
}

func episodeTemplateFromMatch(name string, match *regexp2.Match, names []string) string {
	type span struct {
		start, end int
		name       string
	}
	spans := []span{}
	for _, name := range names {
		if !episodeGroupName.MatchString(name) {
			continue
		}
		group := match.GroupByName(name)
		if group == nil || len(group.Captures) == 0 || group.RuneLength == 0 {
			continue
		}
		spans = append(spans, span{group.RuneIndex, group.RuneIndex + group.RuneLength, name})
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	text := []rune(name)
	escape := strings.NewReplacer("{", "{{", "}", "}}")
	var out strings.Builder
	cursor := 0
	for _, span := range spans {
		if span.start < cursor || span.end > len(text) {
			continue
		}
		out.WriteString(escape.Replace(string(text[cursor:span.start])))
		out.WriteString("{" + span.name + "}")
		cursor = span.end
	}
	out.WriteString(escape.Replace(string(text[cursor:])))
	return out.String()
}
