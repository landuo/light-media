// Package sourcecharset ports the fixed apparent-encoding backend used by the
// selected Requests runtime. It does not call the unrelated Chardet detector.
package sourcecharset

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Encoding preserves Python None separately from a present empty name.
// A completed detector will return this value; this partial core deliberately
// does not expose a fake/fallback Detect entrypoint.
type Encoding struct {
	Name    string
	Present bool
}

type cnRange struct {
	Name        string
	Start, Stop int
}
type cnFrequency struct{ Language, Characters string }
type cnFixedData struct {
	Version                                                string
	Small, Big                                             int
	Ranges                                                 []cnRange
	RangeFamilies                                          map[string]string
	CompatibleFamilies                                     [][2]string
	CompatibleAny, BasicLatinCompatible, SecondaryKeywords []string
	Frequencies                                            []cnFrequency
	SafeASCII, CommonCJK                                   string
	Similar                                                map[string][]string
	Correspondence, IANANames                              map[string]string
	Supported, Multibyte, Korean, Chinese, AccentKeywords  []string
	DeclarationPattern                                     string
}

// These are mechanically extracted original literal tables and fixed stdlib
// aliases/codec declarations. They are algorithm data, not fixture answers.
//
//go:embed reference/charset-normalizer-3.5.2.json
var cn352Tables []byte
var cnDataOnce sync.Once
var cnData cnFixedData
var cnDataError error
var cnMB map[string]bool
var cnSecondary map[string]bool
var cnDeclaration *regexp.Regexp

func cnLoad() error {
	cnDataOnce.Do(func() {
		if err := json.Unmarshal(cn352Tables, &cnData); err != nil {
			cnDataError = err
			return
		}
		if cnData.Version != "3.5.2" || cnData.Small != 32 || cnData.Big != 10000000 || len(cnData.Supported) != 99 {
			cnDataError = errors.New("invalid fixed charset-normalizer data")
			return
		}
		cnMB = make(map[string]bool, len(cnData.Multibyte))
		for _, name := range cnData.Multibyte {
			cnMB[name] = true
		}
		cnSecondary = make(map[string]bool)
		for i, r := range cnData.Ranges {
			if r.Start >= r.Stop || (i > 0 && cnData.Ranges[i-1].Start >= r.Start) {
				cnDataError = errors.New("invalid fixed Unicode range order")
				return
			}
			for _, word := range cnData.SecondaryKeywords {
				if strings.Contains(r.Name, word) {
					cnSecondary[r.Name] = true
					break
				}
			}
		}
		cnDeclaration, cnDataError = regexp.Compile("(?i)" + cnData.DeclarationPattern)
	})
	return cnDataError
}
func cnUnicodeRange(point rune) (string, bool, error) {
	if err := cnLoad(); err != nil {
		return "", false, err
	}
	if point < 0 || point > 0x10ffff {
		return "", false, errors.New("invalid Unicode codepoint")
	}
	if point < 32 {
		return "Control character", true, nil
	}
	if point < 128 {
		return "Basic Latin", true, nil
	}
	i := sort.Search(len(cnData.Ranges), func(i int) bool { return cnData.Ranges[i].Start > int(point) }) - 1
	if i >= 0 && int(point) < cnData.Ranges[i].Stop {
		return cnData.Ranges[i].Name, true, nil
	}
	return "", false, nil
}
func cnIANALookup(name string) (string, bool, error) {
	if err := cnLoad(); err != nil {
		return "", false, err
	}
	// Callers supply ASCII codec names from fixed tables or the ASCII-only
	// original declaration scan, not arbitrary Unicode aliases.
	key := strings.ReplaceAll(strings.ToLower(name), "-", "_")
	value, ok := cnData.IANANames[key]
	return value, ok, nil
}
func cnSpecifiedEncoding(data []byte) (Encoding, error) {
	if err := cnLoad(); err != nil {
		return Encoding{}, err
	}
	end := min(len(data), 8192)
	zone := data[:end]
	lower := bytes.ToLower(zone)
	if !bytes.Contains(lower, []byte("coding")) && !bytes.Contains(lower, []byte("charset")) {
		return Encoding{}, nil
	}
	ascii := make([]byte, 0, len(zone))
	for _, b := range zone {
		if b < 128 {
			ascii = append(ascii, b)
		}
	}
	for _, match := range cnDeclaration.FindAllSubmatch(ascii, -1) {
		value, ok, err := cnIANALookup(string(match[1]))
		if err != nil {
			return Encoding{}, err
		}
		if ok {
			return Encoding{Name: value, Present: true}, nil
		}
	}
	return Encoding{}, nil
}
func cnCandidateOrder() ([]string, error) {
	if err := cnLoad(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(cnData.Supported))
	// Source stable boolean sort: multibyte first, original IANA order within
	// each group. Preserve duplicates; the later source tested set removes them.
	for _, want := range []bool{true, false} {
		for _, name := range cnData.Supported {
			if cnMB[name] == want {
				out = append(out, name)
			}
		}
	}
	return out, nil
}
func cnLegacyName(name string, bom bool) (string, error) {
	if err := cnLoad(); err != nil {
		return "", err
	}
	if name == "utf_8" && bom {
		name += "_sig"
	}
	if other, ok := cnData.Correspondence[name]; ok {
		return other, nil
	}
	return name, nil
}
