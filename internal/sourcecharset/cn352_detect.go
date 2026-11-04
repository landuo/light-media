package sourcecharset

import (
	"slices"
	"strings"

	"light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
)

func cnCodecRejected(err error) bool {
	class := encodingdetector.ErrorClass(err)
	return class == "UnicodeDecodeError" || class == "LookupError"
}
func cnIntersect(a []string, b map[string]bool) bool {
	for _, v := range a {
		if b[v] {
			return true
		}
	}
	return false
}

// DetectCharsetNormalizer352 is the actual fixed-default pipeline. Its Unicode
// classifier, CPython float sum and source sorting are required concrete core
// dependencies; there is no configurable fallback detector or network port.
func DetectCharsetNormalizer352(input []byte) (Encoding, error) {
	if err := cnLoad(); err != nil {
		return Encoding{}, err
	}
	// These exact implementations are supplied by the separate fixed Unicode
	// and CPython core blocks, not by a default-success stub.
	analyzer, err := cnNewAnalyzer(cnFixedCharInfo, cnFixedLower)
	if err != nil {
		return Encoding{}, err
	}
	data := append([]byte(nil), input...)
	match, err := cnFromBytes(data, analyzer)
	if err != nil {
		return Encoding{}, err
	}
	if match == nil {
		return Encoding{}, nil
	}
	name, err := cnLegacyName(match.encoding, match.bom)
	return Encoding{Name: name, Present: err == nil}, err
}
func cnFromBytes(data []byte, a *cnAnalyzer) (*cnMatch, error) {
	length := len(data)
	if length == 0 {
		text := ""
		return cnNewMatch(data, "utf_8", 0, false, nil, &text), nil
	}
	steps, size := 5, 512
	threshold := 0.2
	languageThreshold := 0.1
	if length <= size*steps {
		steps = 1
		size = length
	}
	if steps > 1 && float64(length)/float64(steps) < float64(size) {
		size = length / steps
	}
	tooLarge := length >= cnData.Big
	specified, err := cnSpecifiedEncoding(data)
	if err != nil {
		return nil, err
	}
	prioritized := []string{}
	if specified.Present {
		prioritized = append(prioritized, specified.Name)
	}
	signature, mark := cnIdentifySignature(data)
	if signature.Present {
		prioritized = append([]string{signature.Name}, prioritized...)
	}
	prioritized = append(prioritized, "ascii")
	if !slices.Contains(prioritized, "utf_8") {
		prioritized = append(prioritized, "utf_8")
	}
	order, err := cnCandidateOrder()
	if err != nil {
		return nil, err
	}
	order = append(prioritized, order...)
	tested, softSkip := map[string]bool{}, map[string]bool{}
	definitive := false
	definitiveLanguages := map[string]bool{}
	postDefinitiveSB := 0
	mbDefinitive := false
	var fallbackASCII, fallbackUTF8, fallbackSpecified *cnMatch
	results, early := cnMatches{}, cnMatches{}
	for _, name := range order {
		if tested[name] {
			continue
		}
		tested[name] = true
		bom := signature.Present && signature.Name == name
		strip := bom && cnShouldStripSignature(name)
		if (name == "utf_16" || name == "utf_32" || name == "utf_7") && !bom {
			continue
		}
		if softSkip[name] {
			continue
		}
		multibyte := cnMB[name]
		if definitive {
			var langs []string
			if multibyte {
				langs = cnMultibyteLanguages(name)
			} else {
				langs, err = a.encodingLanguages(name)
				if err != nil {
					return nil, err
				}
			}
			if !cnIntersect(langs, definitiveLanguages) {
				continue
			}
		}
		if definitive && !multibyte && postDefinitiveSB >= 7 {
			continue
		}
		if mbDefinitive && !multibyte {
			continue
		}
		deferred := !multibyte && !tooLarge
		var decoded *string
		var points []rune
		fullDecode := func() (string, error) {
			body := data
			if strip {
				body = data[len(mark):]
			}
			if name == "utf_7" && bom {
				text, e := cnDecode(data, name, "strict")
				return strings.TrimPrefix(text, "\ufeff"), e
			}
			return cnDecode(body, name, "strict")
		}
		if tooLarge && !multibyte {
			start := 0
			if strip {
				start = len(mark)
			}
			_, err = cnDecode(cnSliceBytes(data, start, 500000), name, "strict")
		} else if !deferred {
			var text string
			text, err = fullDecode()
			if err == nil {
				decoded = &text
				points, err = cnCodepoints(text)
			}
		}
		if err != nil {
			if cnCodecRejected(err) {
				err = nil
				continue
			}
			return nil, err
		}
		start := 0
		if bom {
			start = len(mark)
		}
		stride := length / steps
		offsets := []int{}
		for i := start; i < length; i += stride {
			offsets = append(offsets, i)
		}
		mbBonus := multibyte && decoded != nil && len(points) < length
		maxGaveUp := max(len(offsets)/4, 2)
		gaveUp := 0
		lazyFailure := false
		chunks := [][]rune{}
		ratios := []float64{}
		err = cnCutChunks(data, name, offsets, size, bom, strip, mark, multibyte, points, decoded != nil, deferred, func(chunk []rune) (bool, error) {
			ratio, e := cnMessRatio(chunk, a.info, threshold)
			if e != nil {
				return false, e
			}
			chunks = append(chunks, append([]rune(nil), chunk...))
			ratios = append(ratios, ratio)
			if ratio >= threshold {
				gaveUp++
			}
			return !(gaveUp >= maxGaveUp || (bom && !strip)), nil
		})
		if err != nil {
			if !cnCodecRejected(err) {
				return nil, err
			}
			err = nil
			if deferred {
				continue
			}
			gaveUp = maxGaveUp
			lazyFailure = true
		}
		mean := 0.0
		if len(ratios) > 0 {
			mean = cnFloatSum(ratios) / float64(len(ratios))
		}
		if !lazyFailure && tooLarge && !multibyte && mean < threshold && gaveUp < maxGaveUp {
			_, err = cnDecode(data[min(50000, length):], name, "strict")
			if err != nil {
				if cnDecodeFailure(err) {
					err = nil
					continue
				}
				return nil, err
			}
		}
		if mean >= threshold || gaveUp >= maxGaveUp {
			for _, similar := range cnData.Similar[name] {
				softSkip[similar] = true
			}
			fallbackEligible := name == "ascii" || name == "utf_8" || (specified.Present && name == specified.Name) || name == "utf_16" || name == "utf_32"
			if fallbackEligible && !lazyFailure {
				if decoded == nil {
					text, e := fullDecode()
					if e != nil {
						if cnCodecRejected(e) {
							continue
						}
						return nil, e
					}
					decoded = &text
					if tooLarge {
						decoded = nil
					}
				}
				entry := cnNewMatch(data, name, threshold, bom, nil, decoded)
				if specified.Present && name == specified.Name {
					fallbackSpecified = entry
				} else if name == "ascii" {
					fallbackASCII = entry
				} else {
					fallbackUTF8 = entry
				}
			}
			continue
		}
		if deferred {
			text, e := fullDecode()
			if e != nil {
				if cnCodecRejected(e) {
					continue
				}
				return nil, e
			}
			decoded = &text
			points, err = cnCodepoints(text)
			if err != nil {
				return nil, err
			}
		}
		var targetLanguages []string
		if multibyte {
			targetLanguages = cnMultibyteLanguages(name)
		} else {
			targetLanguages, err = a.encodingLanguages(name)
			if err != nil {
				return nil, err
			}
		}
		coherenceChunks := [][]cnCoherence{}
		if name != "ascii" {
			var inclusion *string
			if len(targetLanguages) > 0 {
				joined := strings.Join(targetLanguages, ",")
				inclusion = &joined
			}
			for _, chunk := range chunks {
				ratios, e := a.coherence(chunk, languageThreshold, inclusion)
				if e != nil {
					return nil, e
				}
				coherenceChunks = append(coherenceChunks, ratios)
			}
		}
		coherence, err := cnMergeCoherence(coherenceChunks)
		if err != nil {
			return nil, err
		}
		retained := decoded
		if tooLarge && name != "ascii" && name != "utf_8" && !(specified.Present && name == specified.Name) {
			retained = nil
		}
		current := cnNewMatch(data, name, mean, bom, coherence, retained)
		if err := results.append(current); err != nil {
			return nil, err
		}
		if definitive && !multibyte && mean < 0.02 {
			postDefinitiveSB++
		}
		if (name == "ascii" || name == "utf_8" || (specified.Present && name == specified.Name)) && mean < 0.1 {
			if mean == 0 {
				return current, nil
			}
			if err := early.append(current); err != nil {
				return nil, err
			}
		}
		if len(early.items) > 0 && (!specified.Present || tested[specified.Name]) && tested["ascii"] && tested["utf_8"] {
			return early.best()
		}
		if !definitive && !multibyte {
			best := 0.0
			for _, ratio := range coherence {
				best = max(best, ratio.Ratio)
			}
			if best >= 0.5 && tested["ascii"] && tested["utf_8"] {
				definitive = true
				for _, language := range targetLanguages {
					definitiveLanguages[language] = true
				}
			}
		}
		if !mbDefinitive && multibyte && mbBonus && decoded != nil && float64(len(points)) < float64(length)*0.98 && !slices.Contains([]string{"utf_8", "utf_8_sig", "utf_16", "utf_16_be", "utf_16_le", "utf_32", "utf_32_be", "utf_32_le", "utf_7"}, name) && tested["ascii"] && tested["utf_8"] {
			mbDefinitive = true
		}
		if signature.Present && name == signature.Name {
			return results.byEncoding(name)
		}
	}
	if len(results.items) == 0 {
		var fallback *cnMatch
		if fallbackSpecified != nil {
			fallback = fallbackSpecified
		} else if fallbackUTF8 != nil {
			fallback = fallbackUTF8
		} else {
			fallback = fallbackASCII
		}
		if fallback != nil {
			if err := results.append(fallback); err != nil {
				return nil, err
			}
		}
	}
	return results.best()
}
