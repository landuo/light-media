package app

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"light-media/internal/releasemeta"
	"light-media/internal/transferextensions"
)

// Directory planning uses the complete, already filtered main-file index.
// Unlike the single-file branch, duplicate matching stems are ambiguous.
func originalPlanTransferDirectory(items []originalTransferCandidate, classifier transferextensions.Classifier, syncExtra bool, buildMeta func(string) (*releasemeta.MetaObject, error)) ([]originalTransferCandidate, map[string]string, error) {
	mains := map[string][]originalTransferCandidate{}
	extras := map[string][]originalTransferCandidate{}
	key := func(item map[string]any) string {
		return sourceText(item["storage"]) + ":" + filepath.Clean(sourceText(item["path"]))
	}
	dir := func(item map[string]any) string {
		return sourceText(item["storage"]) + ":" + filepath.Dir(sourceText(item["path"]))
	}
	hasMain := false
	for _, candidate := range items {
		media, _ := classifier.Media(originalTransferItemClass(candidate.Item))
		hasMain = hasMain || media || candidate.Bluray
		item := candidate.Item
		if sourceText(item["type"]) != "file" {
			continue
		}
		if !candidate.Bluray && media {
			mains[dir(item)] = append(mains[dir(item)], candidate)
		} else if classifier.Extra(originalTransferItemClass(item)) {
			extras[dir(item)] = append(extras[dir(item)], candidate)
		}
	}
	if !hasMain {
		return items, map[string]string{}, nil
	}
	planned := []originalTransferCandidate{}
	inherited := map[string]string{}
	seen := map[string]bool{}
	appendItem := func(candidate originalTransferCandidate) bool {
		k := key(candidate.Item)
		if seen[k] {
			return false
		}
		seen[k] = true
		planned = append(planned, candidate)
		return true
	}
	metaCache := map[string]*releasemeta.MetaObject{}
	for _, main := range items {
		media, _ := classifier.Media(originalTransferItemClass(main.Item))
		if !media && !main.Bluray {
			continue
		}
		appendItem(main)
		if main.Bluray {
			continue
		}
		path := sourceText(main.Item["path"])
		meta, err := buildMeta(path)
		if err != nil {
			return nil, nil, err
		}
		if meta == nil {
			continue
		}
		for _, extra := range extras[dir(main.Item)] {
			if seen[key(extra.Item)] {
				continue
			}
			stem := sourceExtraStem(originalTransferItemName(extra.Item), classifier.Subtitle(originalTransferItemClass(extra.Item)))
			matches := []originalTransferCandidate{}
			for _, candidate := range mains[dir(main.Item)] {
				if s := sourceExtraStem(originalTransferItemName(candidate.Item), false); s != "" && s == stem {
					matches = append(matches, candidate)
				}
			}
			extraPath := sourceText(extra.Item["path"])
			if len(matches) == 1 {
				if key(matches[0].Item) == key(main.Item) && appendItem(extra) {
					inherited[extraPath] = path
				}
				continue
			}
			if !syncExtra {
				continue
			}
			extraMeta, cached := metaCache[extraPath]
			if !cached {
				extraMeta, err = buildMeta(extraPath)
				if err != nil {
					return nil, nil, err
				}
				metaCache[extraPath] = extraMeta
			}
			if originalTransferSameMeta(meta, extraMeta) && appendItem(extra) {
				inherited[extraPath] = extraPath
			}
		}
	}
	for _, item := range items {
		appendItem(item)
	}
	return planned, inherited, nil
}

func originalTransferSameMeta(a, b *releasemeta.MetaObject) bool {
	if a == nil || b == nil || a.Text("type") != b.Text("type") || originalTransferClearName(a.Name()) != originalTransferClearName(b.Name()) {
		return false
	}
	for _, field := range []string{"year", "begin_season", "end_season", "begin_episode", "end_episode"} {
		left, right := a.Get(field), b.Get(field)
		if left == nil && right == nil {
			continue
		}
		if left == nil || right == nil {
			return false
		}
		if field == "year" {
			if fmt.Sprint(left) != fmt.Sprint(right) {
				return false
			}
		} else {
			l, r := a.Number(field), b.Number(field)
			if l == nil || r == nil || l.Cmp(r) != 0 {
				return false
			}
		}
	}
	return true
}

func originalTransferClearName(s string) string {
	return cases.Upper(language.Und).String(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f || strings.ContainsRune("、.。,，·:：;；!！?？'’\"“”()（）[]【】「」-—―+|\\_/&#～~\u200b\u200c\u200d\ufeff", r) {
			return -1
		}
		return r
	}, s))
}

func originalDirectoryAllowed(item originalTransferCandidate, classifier transferextensions.Classifier, minMB int64, format *regexp.Regexp) bool {
	if originalTransferBlockedExtra(sourceText(item.Item["path"])) || format != nil && !format.MatchString(sourceText(item.Item["name"])) {
		return false
	}
	class := originalTransferItemClass(item.Item)
	if item.Bluray || classifier.Extra(class) {
		return true
	}
	media, _ := classifier.Media(class)
	size, _ := strconv.ParseFloat(fmt.Sprint(item.Item["size"]), 64)
	return media && (minMB == 0 || size > float64(minMB)*1024*1024)
}

func (a *App) originalDirectoryMetaBuilder(ctx context.Context, in manualOrganizeRequest) (func(string) (*releasemeta.MetaObject, error), error) {
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	format, err := organizeEpisodeTemplate(in.EpisodeFormat)
	if err != nil {
		return nil, err
	}
	return func(path string) (*releasemeta.MetaObject, error) {
		meta, err := parser.ParsePath(path, nil)
		if err != nil || meta == nil {
			return meta, err
		}
		if in.Season != nil {
			meta.Set("begin_season", *in.Season)
		}
		var episodes []int
		if in.EpisodeDetail != "" {
			episodes, err = organizeEpisodeRange(in.EpisodeDetail)
			if err != nil {
				return nil, err
			}
		}
		// A concrete single/range detail wins; otherwise the filename template
		// supplies the endpoints, as in FormatParser.split_episode.
		if format != nil && (len(episodes) == 0 || strings.Contains(in.EpisodeDetail, ",")) {
			if match := format.FindStringSubmatch(filepath.Base(path)); match != nil {
				for i, name := range format.SubexpNames() {
					if name == "ep" {
						episodes, err = organizeEpisodeRange(match[i])
					}
				}
				if err != nil {
					return nil, err
				}
			}
		}
		if len(episodes) > 0 {
			meta.Set("begin_episode", episodes[0])
			if len(episodes) > 1 {
				meta.Set("end_episode", episodes[len(episodes)-1])
			}
		}
		if in.EpisodeOffset != "" {
			for _, field := range []string{"begin_episode", "end_episode"} {
				if n := meta.Number(field); n != nil && n.IsInt64() && n.Sign() != 0 {
					offset, e := organizeEpisodeOffset(in.EpisodeOffset, int(n.Int64()))
					if e != nil {
						return nil, e
					}
					meta.Set(field, offset)
				}
			}
		}
		if in.EpisodePart != "" {
			meta.Set("part", in.EpisodePart)
		}
		return meta, nil
	}, nil
}
