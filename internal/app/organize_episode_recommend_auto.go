package app

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta"
)

// MP app/helper/format.py:327-647. Keep automatic candidate filtering distinct
// from each configured rule's min_file_size_mb. Size is relaxed only when the
// first candidate set is empty, not when detection subsequently fails.
func filterAutomaticEpisodeSamples(samples []organizeEpisodeSample, ignoreSize bool) []organizeEpisodeSample {
	out := make([]organizeEpisodeSample, 0, len(samples))
	for _, s := range samples {
		if s.media && !ignoreSize && s.size < 100*1024*1024 {
			continue
		}
		out = append(out, s)
	}
	return out
}
func episodeSamplePriority(s organizeEpisodeSample) int {
	if s.media {
		return 0
	}
	if s.kind == "audio" {
		return 2
	}
	return 1
}

type episodeDetectedSample struct {
	episodeCompatibilitySample
	start, end, priority int // rune offsets, matching Python str slicing
	verified, fallback   bool
}
type episodeDetectionStats struct{ conflicts, notDetected, candidateMedia, validMedia, verified, fallback int }

func detectRecommendedSamples(ctx context.Context, candidates []organizeEpisodeSample, extract func(organizeEpisodeSample) episodeExtraction) ([]episodeDetectedSample, episodeDetectionStats, error) {
	ordered := append([]organizeEpisodeSample(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if episodeSamplePriority(a) != episodeSamplePriority(b) {
			return episodeSamplePriority(a) < episodeSamplePriority(b)
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.path < b.path
	})
	var stats episodeDetectionStats
	valid := []episodeDetectedSample{}
	for _, sample := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
		if episodeSpecialSample.MatchString(sample.name) {
			continue
		}
		if sample.media {
			stats.candidateMedia++
		}
		e := extract(sample)
		if e.value != "" && e.native != "" && !e.usedNativeFallback && !e.nativeVerified {
			stats.conflicts++
			continue
		}
		first, _, ok := recommendationEpisodeBounds(e.value)
		if !ok {
			stats.notDetected++
			continue
		}
		if first <= 0 {
			continue
		}
		start, end, located := locateRecommendedEpisode(sample.name, e.value)
		if !located {
			stats.notDetected++
			continue
		}
		if !episodeDigits.MatchString(e.value) {
			continue
		}
		valid = append(valid, episodeDetectedSample{episodeCompatibilitySample{sample.name, e.value, e.native}, start, end, episodeSamplePriority(sample), e.nativeVerified, e.usedNativeFallback})
		if sample.media {
			stats.validMedia++
		}
		if e.nativeVerified {
			stats.verified++
		}
		if e.usedNativeFallback {
			stats.fallback++
		}
	}
	return valid, stats, nil
}

func recommendEpisodeAutomatically(ctx context.Context, samples []organizeEpisodeSample, native *releasemeta.MetaInfoParser) (organizeEpisodeRecommendResult, error) {
	return recommendEpisodeAutomaticallyWithExtractor(ctx, samples, func(s organizeEpisodeSample) episodeExtraction { return extractRecommendedEpisode(s, native) })
}

// The extractor parameter permits deterministic source-oracle cases equivalent
// to MP's monkeypatched native/anitopy tests; production always uses both real parsers.
func recommendEpisodeAutomaticallyWithExtractor(ctx context.Context, samples []organizeEpisodeSample, extract func(organizeEpisodeSample) episodeExtraction) (organizeEpisodeRecommendResult, error) {
	fail := func(code, message string) (organizeEpisodeRecommendResult, error) {
		return organizeEpisodeRecommendResult{}, bad(code, message)
	}
	candidates := filterAutomaticEpisodeSamples(samples, false)
	relaxed := false
	if len(candidates) == 0 {
		candidates = filterAutomaticEpisodeSamples(samples, true)
		relaxed = len(candidates) > 0
	}
	if len(candidates) == 0 {
		return fail("EPISODE_AUTO_FAILED", "无匹配自定义定位规则，智能生成失败")
	}
	valid, stats, err := detectRecommendedSamples(ctx, candidates, extract)
	if err != nil {
		return organizeEpisodeRecommendResult{}, err
	}
	if len(valid) == 0 {
		if stats.conflicts > 0 {
			return fail("EPISODE_AUTO_NATIVE_CONFLICT", "样本命名与原生识别结果冲突，建议补充集数定位规则")
		}
		if stats.notDetected > 0 {
			return fail("EPISODE_AUTO_NOT_DETECTED", "样本未识别到有效集数，智能生成失败")
		}
		return fail("EPISODE_AUTO_FAILED", "无匹配自定义定位规则，智能生成失败")
	}
	if stats.conflicts > 0 && len(valid) < len(candidates) {
		return fail("EPISODE_AUTO_NATIVE_CONFLICT", "样本命名与原生识别结果冲突，建议补充集数定位规则")
	}
	if stats.candidateMedia > 1 && float64(stats.validMedia)/float64(stats.candidateMedia) < 0.6 {
		return fail("EPISODE_AUTO_LOW_COVERAGE", "有效正片样本覆盖率不足，建议补充集数定位规则")
	}
	majority, clear := selectEpisodeBaseSamples(valid)
	if len(valid) > 1 && !clear {
		return fail("EPISODE_AUTO_NO_MAJORITY", "样本命名差异过大，建议补充集数定位规则")
	}
	template := episodeOnlyTemplate(majority[0])
	if !validateAutomaticEpisodeTemplate(template, majority) {
		template, err = buildEpisodeDiffTemplate(ctx, majority)
		if err != nil {
			return organizeEpisodeRecommendResult{}, err
		}
		if !validateAutomaticEpisodeTemplate(template, majority) {
			return fail("EPISODE_AUTO_FAILED", "无匹配自定义定位规则，智能生成失败")
		}
	}
	reasons := []string{}
	if len(majority) <= 1 {
		reasons = append(reasons, "single_sample_only")
	}
	if relaxed {
		reasons = append(reasons, "small_files_fallback")
	}
	if stats.fallback > 0 {
		reasons = append(reasons, "native_meta_fallback")
	} else if stats.verified > 0 {
		reasons = append(reasons, "native_meta_verified")
	}
	if len(valid) != len(majority) {
		reasons = append(reasons, "majority_samples_only")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "auto_recommendation")
	}
	message := "无匹配自定义定位规则，已智能生成（仅供参考）"
	switch {
	case len(majority) <= 1:
		message = "样本不足，仅基于单文件智能生成（仅供参考）"
	case relaxed:
		message = "已放宽体积限制智能生成模板（仅供参考）"
	case stats.fallback > 0:
		message = "已结合原生集数识别智能生成模板（仅供参考）"
	case len(valid) != len(majority):
		message = "已根据多数派样本智能生成模板（仅供参考）"
	}
	confidence := "high"
	if len(majority) == 1 || relaxed {
		confidence = "low"
	}
	return organizeEpisodeRecommendResult{RuleName: "智能分析", RuleIndex: -1, EpisodeFormat: template, SampleFile: majority[0].name, SampleCount: len(valid), MatchedCount: len(majority), MajorityCount: len(majority), Confidence: confidence, SizeFilterRelaxed: relaxed, NativeVerifiedCount: stats.verified, NativeFallbackCount: stats.fallback, NativeConflictCount: stats.conflicts, Reason: reasons[0], Reasons: reasons, Message: message}, nil
}

// MP :907-969. Group by the exact prefix before the located episode, choose
// the largest group with a strict lead over the runner-up (not a 50% rule).
func selectEpisodeBaseSamples(samples []episodeDetectedSample) ([]episodeDetectedSample, bool) {
	groups := map[string][]episodeDetectedSample{}
	for _, s := range samples {
		prefix := string([]rune(s.name)[:s.start])
		groups[prefix] = append(groups[prefix], s)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	if len(keys) == 0 {
		return nil, false
	}
	majority := groups[keys[0]]
	sort.SliceStable(majority, func(i, j int) bool {
		a, b := majority[i], majority[j]
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		if a.name != b.name {
			return a.name < b.name
		}
		if a.start != b.start {
			return a.start < b.start
		}
		return a.end < b.end
	})
	return majority, len(keys) == 1 || len(majority) > len(groups[keys[1]])
}

var episodeLiteralEscaper = strings.NewReplacer("{", "{{", "}", "}}")

func episodeOnlyTemplate(s episodeDetectedSample) string {
	text := []rune(s.name)
	return episodeLiteralEscaper.Replace(string(text[:s.start])) + "{ep}" + episodeLiteralEscaper.Replace(string(text[s.end:]))
}
func validateAutomaticEpisodeTemplate(template string, samples []episodeDetectedSample) bool {
	re, err := organizeEpisodeTemplate(template)
	if err != nil || re == nil {
		return false
	}
	compatibility := make([]episodeCompatibilitySample, 0, len(samples))
	for _, s := range samples {
		compatibility = append(compatibility, s.episodeCompatibilitySample)
	}
	return validateEpisodeCompatibility(re, compatibility)
}
func unmatchedEpisodeNames(template string, samples []episodeDetectedSample) []string {
	re, err := organizeEpisodeTemplate(template)
	failed := []string{}
	for _, s := range samples {
		if err != nil || re == nil {
			failed = append(failed, s.name)
			continue
		}
		match := re.FindStringSubmatch(s.name)
		if match == nil {
			failed = append(failed, s.name)
			continue
		}
		value := match[re.SubexpIndex("ep")]
		if _, e := organizeEpisodeRange(value); e != nil {
			failed = append(failed, s.name)
			continue
		}
		if first, _, ok := recommendationEpisodeBounds(value); !ok || first == 0 {
			failed = append(failed, s.name)
		}
	}
	return failed
}

// MP :971-1232. Only the suffix after {ep} is generalized, using a/b/c in
// that order. Each round probes literal gaps around existing variable spans.
func buildEpisodeDiffTemplate(ctx context.Context, samples []episodeDetectedSample) (string, error) {
	if len(samples) < 2 {
		return "", nil
	}
	prefix := string([]rune(samples[0].name)[:samples[0].start])
	suffixes := make([]string, 0, len(samples))
	different := false
	for _, s := range samples {
		text := []rune(s.name)
		if string(text[:s.start]) != prefix {
			return "", nil
		}
		suffix := string(text[s.end:])
		if len(suffixes) > 0 && suffix != suffixes[0] {
			different = true
		}
		suffixes = append(suffixes, suffix)
	}
	if !different {
		return "", nil
	}
	template := episodeOnlyTemplate(samples[0])
	for _, placeholder := range []string{"a", "b", "c"} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		failed := unmatchedEpisodeNames(template, samples)
		if len(failed) == 0 {
			break
		}
		updated := insertEpisodeVariable(template, failed, suffixes, samples, placeholder)
		if updated == template {
			break
		}
		template = updated
	}
	return template, nil
}

type episodeVariableSpan struct {
	start, end int
	name       string
}

func insertEpisodeVariable(template string, failed, suffixes []string, samples []episodeDetectedSample, placeholder string) string {
	pos := strings.Index(template, "{ep}")
	if pos < 0 {
		return template
	}
	after := template[pos+len("{ep}"):]
	base := suffixes[0]
	spans := collectEpisodeVariableSpans(after, base)
	failedSet := map[string]bool{}
	for _, name := range failed {
		failedSet[name] = true
	}
	comparisons := []string{}
	for i, s := range samples {
		if failedSet[s.name] {
			comparisons = append(comparisons, suffixes[i])
		}
	}
	next, ok := nextEpisodeVariableSpan(base, comparisons, spans)
	if !ok {
		return template
	}
	next.name = placeholder
	spans = append(spans, next)
	return template[:pos] + "{ep}" + renderEpisodeSuffix(base, spans)
}
func collectEpisodeVariableSpans(template, base string) []episodeVariableSpan {
	re := compileEpisodeSuffix(template)
	if re == nil {
		return nil
	}
	indices := re.FindStringSubmatchIndex(base)
	if indices == nil {
		return nil
	}
	spans := []episodeVariableSpan{}
	for i, name := range re.SubexpNames() {
		if i == 0 || name == "" {
			continue
		}
		start, end := indices[i*2], indices[i*2+1]
		if start < 0 {
			continue
		}
		spans = append(spans, episodeVariableSpan{utf8.RuneCountInString(base[:start]), utf8.RuneCountInString(base[:end]), name})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

// MP _match_template has named, possibly-empty groups and works on suffixes
// without an ep field. The final recommendation still uses the real consumer.
func compileEpisodeSuffix(template string) *regexp.Regexp {
	var out strings.Builder
	out.WriteString("^")
	names := map[string]bool{}
	for i := 0; i < len(template); {
		if strings.HasPrefix(template[i:], "{{") {
			out.WriteString(`\{`)
			i += 2
			continue
		}
		if strings.HasPrefix(template[i:], "}}") {
			out.WriteString(`\}`)
			i += 2
			continue
		}
		if template[i] == '{' {
			end := strings.IndexByte(template[i:], '}')
			if end < 0 {
				return nil
			}
			name := template[i+1 : i+end]
			if !episodeGroupName.MatchString(name) || names[name] {
				return nil
			}
			names[name] = true
			out.WriteString("(?P<" + name + ">.*?)")
			i += end + 1
			continue
		}
		if template[i] == '}' {
			return nil
		}
		end := i + 1
		for end < len(template) && template[end] != '{' && template[end] != '}' {
			end++
		}
		out.WriteString(regexp.QuoteMeta(template[i:end]))
		i = end
	}
	out.WriteString("$")
	re, err := regexp.Compile(out.String())
	if err != nil {
		return nil
	}
	return re
}
func nextEpisodeVariableSpan(base string, comparisons []string, spans []episodeVariableSpan) (episodeVariableSpan, bool) {
	text := []rune(base)
	cursor := 0
	gaps := []episodeVariableSpan{}
	for _, span := range spans {
		if cursor < span.start {
			gaps = append(gaps, episodeVariableSpan{start: cursor, end: span.start})
		}
		cursor = span.end
	}
	if cursor < len(text) {
		gaps = append(gaps, episodeVariableSpan{start: cursor, end: len(text)})
	}
	for _, gap := range gaps {
		probeSpans := append(append([]episodeVariableSpan(nil), spans...), episodeVariableSpan{gap.start, gap.end, "probe"})
		re := compileEpisodeSuffix(renderEpisodeSuffix(base, probeSpans))
		if re == nil {
			continue
		}
		probeIndex := re.SubexpIndex("probe")
		if probeIndex < 0 {
			continue
		}
		baseGap := string(text[gap.start:gap.end])
		values := []string{}
		for _, comparison := range comparisons {
			match := re.FindStringSubmatch(comparison)
			if match != nil && match[probeIndex] != baseGap {
				values = append(values, match[probeIndex])
			}
		}
		if len(values) == 0 {
			continue
		}
		start, end, ok := calculateEpisodeVariableSpan(baseGap, values)
		if ok {
			return episodeVariableSpan{start: gap.start + start, end: gap.start + end}, true
		}
	}
	return episodeVariableSpan{}, false
}
func calculateEpisodeVariableSpan(base string, comparisons []string) (int, int, bool) {
	texts := [][]rune{[]rune(base)}
	minLen := len(texts[0])
	for _, s := range comparisons {
		r := []rune(s)
		texts = append(texts, r)
		if len(r) < minLen {
			minLen = len(r)
		}
	}
	prefix := 0
prefixLoop:
	for prefix < minLen {
		for _, text := range texts[1:] {
			if text[prefix] != texts[0][prefix] {
				break prefixLoop
			}
		}
		prefix++
	}
	suffix := 0
suffixLoop:
	for suffix < minLen-prefix {
		for _, text := range texts[1:] {
			if text[len(text)-suffix-1] != texts[0][len(texts[0])-suffix-1] {
				break suffixLoop
			}
		}
		suffix++
	}
	end := len(texts[0]) - suffix
	if prefix >= end {
		for _, text := range texts[1:] {
			if len(text)-suffix > prefix {
				return prefix, prefix, true
			}
		}
		return 0, 0, false
	}
	return prefix, end, true // Includes a nonempty base part that is absent in a comparison.
}
func renderEpisodeSuffix(base string, spans []episodeVariableSpan) string {
	ordered := append([]episodeVariableSpan(nil), spans...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	text := []rune(base)
	cursor := 0
	var out strings.Builder
	for _, span := range ordered {
		if span.start < cursor || span.end < span.start {
			continue
		}
		out.WriteString(episodeLiteralEscaper.Replace(string(text[cursor:span.start])))
		out.WriteString("{" + span.name + "}")
		cursor = span.end
	}
	out.WriteString(episodeLiteralEscaper.Replace(string(text[cursor:])))
	return out.String()
}
