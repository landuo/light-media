// Package releasemeta ports the installed MoviePilot title metadata consumed by
// SubscribeGroup. It does not identify a film or authorize filesystem operations.
package releasemeta

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"light-media/internal/media/words"
)

// Options 保存本次解析所使用的原版配置快照。
type Options struct {
	SourceMode    string   `json:"source_mode,omitempty"`
	CustomWords   []string `json:"custom_words,omitempty"`
	ReleaseGroups []string `json:"release_groups,omitempty"`
	Customization []string `json:"customization,omitempty"`
	MediaExts     []string `json:"media_exts"`
}

// Metadata 保存资源属性及原版下载历史使用的季范围；空资源字符串对应 None。
type Metadata struct {
	Resolution         string `json:"resource_pix"`
	Quality            string `json:"resource_type"`
	Effect             string `json:"resource_effect"`
	Team               string `json:"resource_team"`
	Customization      string `json:"customization"`
	Season             string `json:"season"`
	BeginSeason        *int   `json:"begin_season"`
	EndSeason          *int   `json:"end_season"`
	BeginEpisode       *int   `json:"begin_episode,omitempty"`
	EndEpisode         *int   `json:"end_episode,omitempty"`
	Type               string `json:"type"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	legacyBeginSeason  string
	legacyEndSeason    string
	legacyBeginEpisode string
	legacyEndEpisode   string
}

// Parse 按已安装版本的 Rust 0.2.6 或 legacy 分支解析标题与副标题。
func Parse(title string, subtitle *string, options Options) (result Metadata, parseErr error) {
	originalTitle := title
	defer func() {
		if recover() != nil {
			result, parseErr = Metadata{}, errors.New("release_meta_parser_failed")
		}
	}()
	if options.SourceMode == "" {
		options.SourceMode = "rust"
	}
	if options.SourceMode != "rust" && options.SourceMode != "legacy" {
		return Metadata{}, errors.New("release_meta_source_mode_invalid")
	}
	if len(title) > 64<<10 || subtitle != nil && len(*subtitle) > 64<<10 {
		return Metadata{}, errors.New("release_meta_input_limit")
	}
	for _, config := range [][]string{options.CustomWords, options.ReleaseGroups, options.Customization, options.MediaExts} {
		bytes := 0
		for _, value := range config {
			bytes += len(value)
			if len(value) > 64<<10 || bytes > 8<<20 {
				return Metadata{}, errors.New("release_meta_config_limit")
			}
		}
		if len(config) > 100000 {
			return Metadata{}, errors.New("release_meta_config_limit")
		}
	}
	preparedTitle := title
	var err error
	if options.SourceMode == "rust" {
		preparedTitle, err = prepareRustWords(title, options.CustomWords)
		if errors.Is(err, errRustFallback) {
			options.SourceMode = "legacy"
		} else if err != nil {
			return Metadata{}, err
		}
	}
	if options.SourceMode == "legacy" {
		matcher, err := words.New(options.CustomWords)
		if err != nil {
			return Metadata{}, err
		}
		prepared := matcher.Prepare(title)
		if prepared.Limited {
			return Metadata{}, errors.New("release_meta_words_limit")
		}
		preparedTitle = prepared.Title
	}
	title, explicit := explicitTitle(preparedTitle, options.SourceMode)
	isfile := false
	if extension := strings.ToLower(filepath.Ext(title)); configuredMediaExtension(extension, options) {
		isfile = true
		title = strings.TrimSuffix(filepath.Base(title), filepath.Ext(title))
	}
	var meta Metadata
	anime := isAnime(title)
	if options.SourceMode == "legacy" {
		anime = isLegacyAnime(title)
	}
	if anime {
		meta, err = parseAnime(title, subtitle, options)
	} else {
		meta, err = parseVideo(title, subtitle, isfile, options)
	}
	if err != nil {
		if errors.Is(err, errRustFallback) && options.SourceMode == "rust" {
			options.SourceMode = "legacy"
			return Parse(originalTitle, subtitle, options)
		}
		return Metadata{}, err
	}
	if explicit.begin != nil {
		meta.BeginSeason = explicit.begin
		meta.legacyBeginSeason = ""
	}
	if explicit.end != nil {
		meta.EndSeason = explicit.end
		meta.legacyEndSeason = ""
	}
	if explicit.typ != "" {
		meta.Type = explicit.typ
	}
	meta.Season = seasonString(meta)
	return meta, nil
}

func seasonString(meta Metadata) string {
	if meta.legacyBeginSeason != "" {
		end := meta.legacyEndSeason
		if meta.EndSeason != nil {
			end = strconv.Itoa(*meta.EndSeason)
		}
		return legacySeasonLabel(meta.legacyBeginSeason, end)
	}
	if meta.BeginSeason != nil && meta.legacyEndSeason != "" {
		return legacySeasonLabel(strconv.Itoa(*meta.BeginSeason), meta.legacyEndSeason)
	}
	if meta.BeginSeason != nil {
		if meta.EndSeason != nil {
			return fmt.Sprintf("S%02d-S%02d", *meta.BeginSeason, *meta.EndSeason)
		}
		return fmt.Sprintf("S%02d", *meta.BeginSeason)
	}
	if meta.Type == "电视剧" {
		return "S01"
	}
	return ""
}

func intPointer(value int) *int { return &value }

func mediaExtension(extension string) bool {
	return extension != "" && strings.Contains("|.mp4|.mkv|.ts|.iso|.rmvb|.avi|.mov|.mpeg|.mpg|.wmv|.3gp|.asf|.m4v|.flv|.m2ts|.tp|.f4v|.strm|.srt|.ass|.ssa|.sup|.aac|.ac3|.amr|.caf|.cda|.dsf|.dff|.kar|.m4a|.mp1|.mp2|.mp3|.mid|.mod|.mka|.mpc|.nsf|.ogg|.pcm|.rmi|.s3m|.snd|.spx|.tak|.tta|.vqf|.wav|.wma|.aifc|.aiff|.alac|.adif|.adts|.flac|.midi|.opus|.sfalc|", "|"+extension+"|")
}

func configuredMediaExtension(extension string, options Options) bool {
	if options.MediaExts == nil {
		return mediaExtension(extension)
	}
	for _, configured := range options.MediaExts {
		if strings.EqualFold(configured, extension) {
			return true
		}
	}
	return false
}

// Rust's route uses regex-syntax 0.8.10 Unicode 16 decimal/space tables,
// independently of both the engine's Rust stdlib tables and Python regex.
func isAnime(title string) bool {
	if rustAnimeRouting[0].MatchString(title) || rustAnimeRouting[1].MatchString(title) {
		return true
	}
	if rustAnimeRouting[2].MatchString(title) {
		return false
	}
	return rustAnimeRouting[3].MatchString(title)
}

type explicitInfo struct {
	begin, end *int
	typ        string
}

func explicitTitle(title, mode string) (string, explicitInfo) {
	info := explicitInfo{}
	for _, m := range rxMatches(`(?<={\[)[\W\w]+(?=]})`, title, false) {
		value := m.String()
		recognized := false
		for _, expression := range []string{`tmdbid=\d+`, `doubanid=\d+`, `bangumiid=\d+`, `anilistid=\d+`, `(?:^|;)g=[0-9a-fA-F]+(?=;|$)`} {
			recognized = recognized || rxHas(expression, value, false)
		}
		if typ := rxFirst(`(?<=type=)\w+`, value, false); typ != nil {
			recognized = true
			switch typ.String() {
			case "movie", "movies":
				info.typ = "电影"
			case "tv":
				info.typ = "电视剧"
			}
		}
		for _, field := range []struct {
			pattern string
			target  **int
		}{{`(?<=s=)\d+`, &info.begin}, {`(?<=s=\d+-)\d+`, &info.end}} {
			if number := rxFirst(field.pattern, value, false); number != nil {
				if n, err := strconv.Atoi(number.String()); err == nil {
					*field.target = intPointer(n)
					recognized = true
				}
			}
		}
		if rxHas(`(?<=e=)\d+`, value, false) {
			recognized = true
		}
		if recognized {
			title = strings.ReplaceAll(title, "{["+value+"]}", "")
		}
	}
	if info.begin != nil && info.end != nil && *info.begin > *info.end && (mode == "rust" || *info.begin != 0 && *info.end != 0) {
		info.begin, info.end = info.end, info.begin
	}
	for _, pattern := range []string{`\[tmdbid[=\-](\d+)\]`, `\[tmdb[=\-](\d+)\]`, `\{tmdbid[=\-](\d+)\}`, `\{tmdb[=\-](\d+)\}`, `\[(?:bangumi|anilist)(?:id)?[=\-](\d+)\]`, `\{(?:bangumi|anilist)(?:id)?[=\-](\d+)\}`} {
		title = rxReplace(pattern, title, "", false)
	}
	return title, info
}
