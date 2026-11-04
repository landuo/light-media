package sourcecharset

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

type cnUnicodeAsset struct {
	UCD, PythonVersion, RawSHA256, CompressedBase64 string
	Count, RecordSize                               int
	Categories                                      []string
	Lower                                           []struct {
		Code   int
		Points []rune
	}
	Decompositions []struct {
		Code  int
		Value string
	}
}

// The file must be losslessly projected from the independently accepted actual
// fixed-runtime generator output. No empty or host-Unicode fallback is allowed.
//
//go:embed reference/unicode15-properties.json
var cnUnicodeJSON []byte
var cnUnicodeOnce sync.Once
var cnUnicodeError error
var cnUnicode cnUnicodeAsset
var cnUnicodeRecords []byte
var cnLowerMappings map[rune][]rune
var cnDecompositions map[rune]string

func cnLoadUnicode() error {
	cnUnicodeOnce.Do(func() {
		if err := json.Unmarshal(cnUnicodeJSON, &cnUnicode); err != nil {
			cnUnicodeError = err
			return
		}
		if cnUnicode.UCD != "15.0.0" || cnUnicode.PythonVersion != "3.12.14" || cnUnicode.Count != 0x110000 || cnUnicode.RecordSize != 4 || len(cnUnicode.Categories) != 30 {
			cnUnicodeError = errors.New("fixed Unicode metadata differs")
			return
		}
		packed, err := base64.StdEncoding.DecodeString(cnUnicode.CompressedBase64)
		if err != nil {
			cnUnicodeError = err
			return
		}
		reader, err := zlib.NewReader(bytes.NewReader(packed))
		if err != nil {
			cnUnicodeError = err
			return
		}
		raw, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			cnUnicodeError = err
			return
		}
		if closeErr != nil {
			cnUnicodeError = closeErr
			return
		}
		sum := sha256.Sum256(raw)
		if len(raw) != cnUnicode.Count*cnUnicode.RecordSize || hex.EncodeToString(sum[:]) != cnUnicode.RawSHA256 {
			cnUnicodeError = errors.New("fixed Unicode records corrupted")
			return
		}
		cnUnicodeRecords = raw
		cnLowerMappings = make(map[rune][]rune, len(cnUnicode.Lower))
		cnDecompositions = make(map[rune]string, len(cnUnicode.Decompositions))
		last := -1
		for _, item := range cnUnicode.Lower {
			if item.Code <= last || item.Code >= cnUnicode.Count || len(item.Points) == 0 {
				cnUnicodeError = errors.New("invalid Unicode lower order")
				return
			}
			last = item.Code
			for _, p := range item.Points {
				if p < 0 || p >= 0x110000 {
					cnUnicodeError = errors.New("invalid Unicode lower codepoint")
					return
				}
			}
			cnLowerMappings[rune(item.Code)] = append([]rune(nil), item.Points...)
		}
		last = -1
		for _, item := range cnUnicode.Decompositions {
			if item.Code <= last || item.Code >= cnUnicode.Count || item.Value == "" {
				cnUnicodeError = errors.New("invalid Unicode decomposition order")
				return
			}
			last = item.Code
			cnDecompositions[rune(item.Code)] = item.Value
		}
	})
	return cnUnicodeError
}
func cnProperty(c rune) (byte, uint16, string, error) {
	if err := cnLoadUnicode(); err != nil {
		return 0, 0, "", err
	}
	if c < 0 || c >= 0x110000 {
		return 0, 0, "", errors.New("invalid Python codepoint")
	}
	off := int(c) * 4
	category := int(cnUnicodeRecords[off+3])
	if category >= len(cnUnicode.Categories) {
		return 0, 0, "", errors.New("invalid fixed category index")
	}
	return cnUnicodeRecords[off], uint16(cnUnicodeRecords[off+1]) | uint16(cnUnicodeRecords[off+2])<<8, cnUnicode.Categories[category], nil
}

type cnInfoEntry struct {
	once sync.Once
	info cnCharInfo
	err  error
}

var cnInfoCache sync.Map

func cnFixedCharInfo(c rune) (cnCharInfo, error) {
	if c < 0 || c >= 0x110000 {
		return cnCharInfo{}, errors.New("invalid Python codepoint")
	}
	value, _ := cnInfoCache.LoadOrStore(c, &cnInfoEntry{})
	entry := value.(*cnInfoEntry)
	entry.once.Do(func() { entry.info, entry.err = cnBuildCharInfo(c) })
	return entry.info, entry.err
}
func cnBuildCharInfo(c rune) (cnCharInfo, error) {
	if err := cnLoad(); err != nil {
		return cnCharInfo{}, err
	}
	bits, flags, category, err := cnProperty(c)
	if err != nil {
		return cnCharInfo{}, err
	}
	i := cnCharInfo{character: c, printable: bits&1 != 0, alpha: bits&2 != 0, upper: bits&4 != 0, lower: bits&8 != 0, space: bits&16 != 0, digit: bits&32 != 0, unaccented: c}
	i.rangeName, i.rangePresent, err = cnUnicodeRange(c)
	if err != nil {
		return cnCharInfo{}, err
	}
	punctuation := strings.Contains(category, "P") || (i.rangePresent && strings.Contains(i.rangeName, "Punctuation"))
	symbol := strings.Contains(category, "S") || strings.Contains(category, "N") || (i.rangePresent && strings.Contains(i.rangeName, "Forms") && category != "Lo")
	if c < 128 {
		i.ascii = true
		i.safe = strings.ContainsRune(cnData.SafeASCII, c)
		// Original CharInfo's ASCII specialization deliberately does NOT use
		// str.isspace for controls0x1c..0x1f. Its separator still does, below.
		if c >= 'A' && c <= 'Z' {
			i.alpha = true
			i.upper = true
			i.lower = false
			i.space = false
			i.digit = false
			i.printable = true
			i.caseVariable = true
			i.flags = 1
			i.latin = true
		} else if c >= 'a' && c <= 'z' {
			i.alpha = true
			i.upper = false
			i.lower = true
			i.space = false
			i.digit = false
			i.printable = true
			i.caseVariable = true
			i.flags = 1
			i.latin = true
		} else if c >= '0' && c <= '9' {
			i.alpha = false
			i.upper = false
			i.lower = false
			i.space = false
			i.digit = true
			i.printable = true
		} else if c == ' ' || (c >= 9 && c <= 13) {
			i.alpha = false
			i.upper = false
			i.lower = false
			i.space = true
			i.digit = false
			i.printable = c == ' '
		} else {
			i.alpha = false
			i.upper = false
			i.lower = false
			i.space = false
			i.digit = false
			if i.printable {
				i.punct = punctuation
				i.symbol = symbol
			}
		}
	} else {
		i.flags = flags
		i.caseVariable = i.lower != i.upper
		i.accentuated = flags&(1<<1) != 0
		i.latin = flags&1 != 0
		i.cjk = flags&(1<<2) != 0
		i.katakana = flags&(1<<4) != 0
		i.halfwidthKatakana = flags&(1<<9) != 0
		i.arabic = flags&(1<<7) != 0
		i.ligature = flags&(1<<10) != 0
		i.superscript = flags&(1<<11) != 0
		i.sentenceOpen = flags&(1<<12) != 0
		i.glyph = flags&((1<<2)|(1<<3)|(1<<4)|(1<<5)|(1<<6)) != 0
		if !i.alpha && i.rangePresent {
			i.emoticon = strings.Contains(i.rangeName, "Emoticons") || strings.Contains(i.rangeName, "Pictographs")
		}
		if i.latin && i.accentuated {
			if raw := cnDecompositions[c]; raw != "" {
				words := strings.Split(raw, " ")
				point, e := strconv.ParseUint(words[0], 16, 32)
				if e != nil || point > 0x10ffff {
					return cnCharInfo{}, &CNSourceError{Class: "ValueError"}
				}
				i.unaccented = rune(point)
			}
		}
		i.commonCJK = i.cjk && strings.ContainsRune(cnData.CommonCJK, c)
		if i.printable {
			i.punct = punctuation
			i.symbol = symbol
		}
	}
	i.separator = bits&16 != 0 || c == '｜' || c == '+' || c == '<' || c == '>' || strings.Contains(category, "Z") || category == "Po" || category == "Pd" || category == "Pc"
	return i, nil
}

// CNSourceError identifies a real ordinary source exception, not dependency
// corruption, context cancellation, guard failure or arbitrary Go errors.
type CNSourceError struct{ Class string }

func (e *CNSourceError) Error() string { return "charset-normalizer source " + e.Class }
func cnFixedLower(points []rune) ([]rune, error) {
	if err := cnLoadUnicode(); err != nil {
		return nil, err
	}
	out := make([]rune, 0, len(points))
	for index, c := range points {
		if c < 0 || c >= 0x110000 {
			return nil, errors.New("invalid Python lower input")
		}
		if c == 0x03a3 {
			before := false
			for j := index - 1; j >= 0; j-- {
				bits, _, _, err := cnProperty(points[j])
				if err != nil {
					return nil, err
				}
				if bits&128 != 0 {
					continue
				}
				before = bits&64 != 0
				break
			}
			after := false
			for j := index + 1; j < len(points); j++ {
				bits, _, _, err := cnProperty(points[j])
				if err != nil {
					return nil, err
				}
				if bits&128 != 0 {
					continue
				}
				after = bits&64 != 0
				break
			}
			if before && !after {
				out = append(out, 0x03c2)
			} else {
				out = append(out, 0x03c3)
			}
			continue
		}
		if mapped, ok := cnLowerMappings[c]; ok {
			out = append(out, mapped...)
		} else {
			out = append(out, c)
		}
	}
	return out, nil
}
