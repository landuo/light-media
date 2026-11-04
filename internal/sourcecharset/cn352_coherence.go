package sourcecharset

import (
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
)

type cnLowerFunc func([]rune) ([]rune, error)
type cnAnalyzer struct {
	info                  cnInfoLookup
	lower                 cnLowerFunc
	encodingLanguageCache map[string][]string
	targetFeatureCache    map[string][2]bool
	frequency             map[string][]rune
	frequencyRank         map[string]map[rune]int
}

func cnNewAnalyzer(info cnInfoLookup, lower cnLowerFunc) (*cnAnalyzer, error) {
	if info == nil || lower == nil {
		return nil, errors.New("exact Unicode classification/lowercase required")
	}
	if err := cnLoad(); err != nil {
		return nil, err
	}
	a := &cnAnalyzer{info: info, lower: lower, encodingLanguageCache: map[string][]string{}, targetFeatureCache: map[string][2]bool{}, frequency: map[string][]rune{}, frequencyRank: map[string]map[rune]int{}}
	for _, entry := range cnData.Frequencies {
		points, err := cnCodepoints(entry.Characters)
		if err != nil {
			return nil, err
		}
		a.frequency[entry.Language] = points
		ranks := map[rune]int{}
		for i, c := range points {
			ranks[c] = i
		}
		a.frequencyRank[entry.Language] = ranks
	}
	return a, nil
}
func (a *cnAnalyzer) encodingUnicodeRanges(name string) ([]string, error) {
	if cnMB[name] {
		return nil, errors.New("single-byte range query on a multibyte codec")
	}
	seen := map[string]int{}
	count := 0
	// Original incremental SB decoders are stateless. Keep the original upper
	// bound 0xff exclusive; do not add the last byte as a supposed correction.
	for i := 0x40; i < 0xff; i++ {
		text, err := cnDecode([]byte{byte(i)}, name, "ignore")
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		points, err := cnCodepoints(text)
		if err != nil {
			return nil, err
		}
		if len(points) != 1 {
			return nil, errors.New("single byte codec produced non-scalar range sample")
		}
		info, err := a.info(points[0])
		if err != nil {
			return nil, err
		}
		if !info.rangePresent || cnSecondary[info.rangeName] {
			continue
		}
		seen[info.rangeName]++
		count++
	}
	ranges := []string{}
	for name, n := range seen {
		if float64(n)/float64(count) >= 0.15 {
			ranges = append(ranges, name)
		}
	}
	sort.Strings(ranges)
	return ranges, nil
}
func (a *cnAnalyzer) rangeLanguages(primary string) ([]string, error) {
	out := []string{}
	for _, entry := range cnData.Frequencies {
		for _, c := range a.frequency[entry.Language] {
			info, err := a.info(c)
			if err != nil {
				return nil, err
			}
			if info.rangePresent && info.rangeName == primary {
				out = append(out, entry.Language)
				break
			}
		}
	}
	return out, nil
}
func (a *cnAnalyzer) encodingLanguages(name string) ([]string, error) {
	if cached, ok := a.encodingLanguageCache[name]; ok {
		return append([]string(nil), cached...), nil
	}
	ranges, err := a.encodingUnicodeRanges(name)
	if err != nil {
		return nil, err
	}
	primary := ""
	for _, r := range ranges {
		if !strings.Contains(r, "Latin") {
			primary = r
			break
		}
	}
	result := []string{"Latin Based"}
	if primary != "" {
		result, err = a.rangeLanguages(primary)
		if err != nil {
			return nil, err
		}
	}
	a.encodingLanguageCache[name] = append([]string(nil), result...)
	return result, nil
}
func cnMultibyteLanguages(name string) []string {
	if strings.HasPrefix(name, "shift_") || strings.HasPrefix(name, "iso2022_jp") || strings.HasPrefix(name, "euc_j") || name == "cp932" {
		return []string{"Japanese"}
	}
	if strings.HasPrefix(name, "gb") || slices.Contains(cnData.Chinese, name) {
		return []string{"Chinese"}
	}
	if strings.HasPrefix(name, "iso2022_kr") || slices.Contains(cnData.Korean, name) {
		return []string{"Korean"}
	}
	return []string{}
}
func (a *cnAnalyzer) targetFeatures(language string) (bool, bool, error) {
	if cached, ok := a.targetFeatureCache[language]; ok {
		return cached[0], cached[1], nil
	}
	points, ok := a.frequency[language]
	if !ok {
		return false, false, errors.New("unknown fixed frequency language")
	}
	accents, pureLatin := false, true
	for _, c := range points {
		info, err := a.info(c)
		if err != nil {
			return false, false, err
		}
		if !accents && info.accentuated {
			accents = true
		}
		if pureLatin && !info.latin {
			pureLatin = false
		}
	}
	a.targetFeatureCache[language] = [2]bool{accents, pureLatin}
	return accents, pureLatin, nil
}
func (a *cnAnalyzer) alphabetLanguages(characters []rune, ignoreNonLatin bool) ([]string, error) {
	set := map[rune]bool{}
	sourceAccents := false
	for _, c := range characters {
		set[c] = true
		if !sourceAccents {
			info, err := a.info(c)
			if err != nil {
				return nil, err
			}
			sourceAccents = info.accentuated
		}
	}
	candidates := []cnCoherence{}
	for _, entry := range cnData.Frequencies {
		accents, latin, err := a.targetFeatures(entry.Language)
		if err != nil {
			return nil, err
		}
		if (ignoreNonLatin && !latin) || (!accents && sourceAccents) {
			continue
		}
		matches := 0
		for c := range a.frequencyRank[entry.Language] {
			if set[c] {
				matches++
			}
		}
		ratio := float64(matches) / float64(len(a.frequency[entry.Language]))
		if ratio >= 0.2 {
			candidates = append(candidates, cnCoherence{entry.Language, ratio})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Ratio > candidates[j].Ratio })
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.Language
	}
	return out, nil
}
func (a *cnAnalyzer) popularity(language string, ordered []rune) (float64, error) {
	target, ok := a.frequency[language]
	if !ok || len(ordered) == 0 {
		return 0, errors.New("invalid fixed language popularity input")
	}
	ranks := a.frequencyRank[language]
	n := len(target)
	large := n > 26
	largeThreshold := float64(n) / 3
	projection := float64(n) / float64(len(ordered))
	lr, orr := []int{}, []int{}
	for i, c := range ordered {
		if rank, ok := ranks[c]; ok {
			lr = append(lr, rank)
			orr = append(orr, i)
		}
	}
	approved := 0
	for i, lrank := range lr {
		rank := orr[i]
		projected := int(float64(rank) * projection)
		if !large && math.Abs(float64(projected-lrank)) > 4 {
			continue
		}
		if large && math.Abs(float64(projected-lrank)) < largeThreshold {
			approved++
			continue
		}
		if lrank == 0 {
			approved++
			continue
		}
		afterLen := n - lrank
		before, after := 0, 0
		for j, other := range lr {
			if other < lrank {
				if orr[j] < rank {
					before++
					if 5*before >= 2*lrank {
						approved++
						break
					}
				}
			} else if orr[j] >= rank {
				after++
				if 5*after >= 2*afterLen {
					approved++
					break
				}
			}
		}
	}
	return float64(approved) / float64(len(ordered)), nil
}
func (a *cnAnalyzer) alphaLayers(points []rune) ([][]rune, error) {
	type layer struct {
		name   string
		points []rune
	}
	layers := []layer{}
	index := map[string]int{}
	single := ""
	multi := false
	previousRange := ""
	previousPresent := false
	previousTarget := ""
	for _, c := range points {
		info, err := a.info(c)
		if err != nil {
			return nil, err
		}
		if !info.alpha || !info.rangePresent {
			continue
		}
		name := info.rangeName
		if previousPresent && name == previousRange {
			if previousTarget != "" {
				i := index[previousTarget]
				layers[i].points = append(layers[i].points, c)
			}
			continue
		}
		target := ""
		if multi {
			for _, l := range layers {
				suspicious, err := cnSuspiciousRanges(l.name, true, name, true)
				if err != nil {
					return nil, err
				}
				if !suspicious {
					target = l.name
					break
				}
			}
		} else if single != "" {
			suspicious, err := cnSuspiciousRanges(single, true, name, true)
			if err != nil {
				return nil, err
			}
			if !suspicious {
				target = single
			}
		}
		if target == "" {
			target = name
		}
		i, exists := index[target]
		if !exists {
			i = len(layers)
			index[target] = i
			layers = append(layers, layer{name: target})
			if single == "" {
				single = target
			} else {
				multi = true
			}
		}
		layers[i].points = append(layers[i].points, c)
		previousRange = name
		previousPresent = true
		previousTarget = target
	}
	out := make([][]rune, len(layers))
	for i, l := range layers {
		var err error
		out[i], err = a.lower(l.points)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func cnFilterAlternate(results []cnCoherence) []cnCoherence {
	order := []string{}
	indices := map[string]int{}
	groups := [][]float64{}
	duplicate := false
	for _, result := range results {
		name := strings.ReplaceAll(result.Language, "—", "")
		i, ok := indices[name]
		if !ok {
			i = len(order)
			indices[name] = i
			order = append(order, name)
			groups = append(groups, []float64{})
		}
		groups[i] = append(groups[i], result.Ratio)
		if len(groups[i]) > 1 {
			duplicate = true
		}
	}
	if !duplicate {
		return results
	}
	filtered := make([]cnCoherence, len(order))
	for i, name := range order {
		best := groups[i][0]
		for _, v := range groups[i][1:] {
			best = math.Max(best, v)
		}
		filtered[i] = cnCoherence{name, best}
	}
	return filtered
}
func (a *cnAnalyzer) coherence(points []rune, threshold float64, inclusion *string) ([]cnCoherence, error) {
	results := []cnCoherence{}
	ignoreNonLatin := false
	sufficient := 0
	languages := []string{}
	if inclusion != nil {
		languages = strings.Split(*inclusion, ",")
	}
	if i := slices.Index(languages, "Latin Based"); i >= 0 {
		ignoreNonLatin = true
		languages = append(languages[:i:i], languages[i+1:]...)
	}
	layers, err := a.alphaLayers(points)
	if err != nil {
		return nil, err
	}
	for _, layer := range layers {
		counts := map[rune]int{}
		order := []rune{}
		for _, c := range layer {
			if _, ok := counts[c]; !ok {
				order = append(order, c)
			}
			counts[c]++
		}
		if len(layer) <= cnData.Small {
			continue
		}
		sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
		targets := languages
		if len(targets) == 0 {
			targets, err = a.alphabetLanguages(order, ignoreNonLatin)
			if err != nil {
				return nil, err
			}
		}
		for _, language := range targets {
			ratio, err := a.popularity(language, order)
			if err != nil {
				return nil, err
			}
			if ratio < threshold {
				continue
			} else if ratio >= 0.8 {
				sufficient++
			}
			rounded, err := cnRound(ratio, 4)
			if err != nil {
				return nil, err
			}
			results = append(results, cnCoherence{language, rounded})
			if sufficient >= 3 {
				break
			}
		}
	}
	results = cnFilterAlternate(results)
	sort.SliceStable(results, func(i, j int) bool { return results[i].Ratio > results[j].Ratio })
	return results, nil
}

// cnMergeCoherence requires cnFloatSum's exact fixed CPython3.12 sum(double)
// implementation, a separate bound dependency, not naive Go += substitution.
func cnMergeCoherence(results [][]cnCoherence) ([]cnCoherence, error) {
	names := []string{}
	groups := map[string][]float64{}
	for _, group := range results {
		for _, r := range group {
			if _, ok := groups[r.Language]; !ok {
				names = append(names, r.Language)
			}
			groups[r.Language] = append(groups[r.Language], r.Ratio)
		}
	}
	merged := make([]cnCoherence, 0, len(names))
	for _, name := range names {
		values := groups[name]
		sum := cnFloatSum(values)
		rounded, err := cnRound(sum/float64(len(values)), 4)
		if err != nil {
			return nil, err
		}
		merged = append(merged, cnCoherence{name, rounded})
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Ratio > merged[j].Ratio })
	return merged, nil
}
