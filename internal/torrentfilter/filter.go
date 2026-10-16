// Package torrentfilter ports TorrentHelper.filter_torrent's additional
// subscription/workflow filters. It does not resolve media identities.
package torrentfilter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

// Nil means the original field has not replaced a legacy native filter. A
// pointer to empty text is an explicit empty original field. Both are falsey
// to the original filter method itself.
type Parameters struct {
	Include    *string `json:"include,omitempty"`
	Exclude    *string `json:"exclude,omitempty"`
	Quality    *string `json:"quality,omitempty"`
	Resolution *string `json:"resolution,omitempty"`
	Effect     *string `json:"effect,omitempty"`
	Size       *string `json:"size,omitempty"`
}
type Torrent struct {
	Title          string   `json:"title"`
	Description    *string  `json:"description"`
	Labels         []string `json:"labels"`
	UploadFactor   *float64 `json:"upload_factor"`
	DownloadFactor *float64 `json:"download_factor"`
	Size           int64    `json:"size"`
}
type Failure struct {
	Field string
	Kind  string
}

func (e *Failure) Error() string { return e.Kind }

type compiled struct {
	re  *regexp2.Regexp
	err error
}

var patterns = struct {
	sync.Mutex
	values map[string]compiled
	bytes  int
}{values: map[string]compiled{}}

var errPatternLimit = errors.New("additional filter expansion limit")

func compile(pattern string) compiled {
	patterns.Lock()
	if value, exists := patterns.values[pattern]; exists {
		patterns.Unlock()
		return value
	}
	patterns.Unlock()
	translated := standardClasses(pattern)
	if len(translated) > 256<<10 {
		return compiled{err: errPatternLimit}
	}
	re, err := pyregex.Compile(translated, true)
	value := compiled{re, err}
	patterns.Lock()
	defer patterns.Unlock()
	if existing, ok := patterns.values[pattern]; ok {
		return existing
	}
	// Class expansion must not turn the 512-entry source cache into an
	// unbounded retained AST. The weight includes an allowance for tree nodes.
	weight := len(pattern) + len(translated)*8
	if len(patterns.values) < 512 && patterns.bytes+weight <= 4<<20 {
		patterns.values[pattern] = value
		patterns.bytes += weight
	}
	return value
}
func VolumeFactor(upload, download *float64) string {
	if upload == nil || download == nil {
		return "未知"
	}
	values := map[string]string{"1.00 1.00": "普通", "1.00 0.00": "免费", "2.00 1.00": "2X", "4.00 1.00": "4X", "2.00 0.00": "2X免费", "4.00 0.00": "4X免费", "1.00 0.50": "50%", "2.00 0.50": "2X 50%", "1.00 0.70": "70%", "1.00 0.30": "30%", "1.00 0.75": "75%", "1.00 0.25": "25%"}
	if value, ok := values[fmt.Sprintf("%.2f %.2f", *upload, *download)]; ok {
		return value
	}
	return "未知"
}
func content(t Torrent) string {
	description := "None"
	if t.Description != nil {
		description = *t.Description
	}
	return t.Title + " " + description + " " + strings.Join(t.Labels, " ") + " " + VolumeFactor(t.UploadFactor, t.DownloadFactor)
}
func fields(p Parameters) []struct {
	name  string
	value *string
} {
	return []struct {
		name  string
		value *string
	}{{"include", p.Include}, {"exclude", p.Exclude}, {"quality", p.Quality}, {"resolution", p.Resolution}, {"effect", p.Effect}}
}
func Validate(p Parameters) error {
	for _, field := range fields(p) {
		if field.value == nil || *field.value == "" {
			continue
		}
		if len(*field.value) > 64<<10 {
			return &Failure{field.name, "RuleLimit"}
		}
	}
	if p.Size != nil && len(*p.Size) > 64<<10 {
		return &Failure{"size", "RuleLimit"}
	}
	return nil
}
func Match(ctx context.Context, p Parameters, t Torrent) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	all := content(t)
	if len(t.Title) > 64<<10 || len(all) > 256<<10 {
		return false, &Failure{"content", "RuleLimit"}
	}
	for _, field := range fields(p) {
		if field.value == nil || *field.value == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if len(*field.value) > 64<<10 {
			return false, &Failure{field.name, "RuleLimit"}
		}
		compiled := compile(*field.value)
		if compiled.err != nil {
			if errors.Is(compiled.err, errPatternLimit) {
				return false, &Failure{field.name, "RuleLimit"}
			}
			return false, &Failure{field.name, "error"}
		}
		value := t.Title
		if field.name == "include" || field.name == "exclude" {
			value = all
		}
		matched, err := compiled.re.MatchString(value)
		if err != nil {
			return false, &Failure{field.name, "RegexTimeout"}
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if field.name == "exclude" {
			if matched {
				return false, nil
			}
		} else if !matched {
			return false, nil
		}
	}
	if p.Size != nil && *p.Size != "" {
		kind, lower, upper, err := sizeRange(*p.Size)
		if err != nil {
			return false, err
		}
		switch kind {
		case "between":
			if float64(t.Size) < lower || float64(t.Size) > upper {
				return false, nil
			}
		case "gte":
			if float64(t.Size) < lower {
				return false, nil
			}
		case "lte":
			if float64(t.Size) > upper {
				return false, nil
			}
		}
	}
	return true, nil
}
func sizeRange(value string) (kind string, lower, upper float64, err error) {
	if len(value) > 64<<10 {
		return "", 0, 0, &Failure{"size", "RuleLimit"}
	}
	parse := func(text string) (float64, error) {
		text = strings.Map(func(r rune) rune {
			if r <= 127 {
				return r
			}
			for _, block := range unicode.Nd.R16 {
				if r >= rune(block.Lo) && r <= rune(block.Hi) && (r-rune(block.Lo))%rune(block.Stride) == 0 {
					return '0' + (r-rune(block.Lo))/rune(block.Stride)%10
				}
			}
			for _, block := range unicode.Nd.R32 {
				if r >= rune(block.Lo) && r <= rune(block.Hi) && (r-rune(block.Lo))%rune(block.Stride) == 0 {
					return '0' + (r-rune(block.Lo))/rune(block.Stride)%10
				}
			}
			return r
		}, text)
		// Python float accepts a single underscore only between decimal digits.
		for i, c := range text {
			if c == '_' && (i == 0 || i+1 == len(text) || text[i-1] < '0' || text[i-1] > '9' || text[i+1] < '0' || text[i+1] > '9') {
				return 0, &Failure{"size", "ValueError"}
			}
		}
		n, e := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(text), "_", ""), 64)
		if e != nil && !errors.Is(e, strconv.ErrRange) {
			return 0, &Failure{"size", "ValueError"}
		}
		return n * (1024 * 1024), nil
	}
	if strings.Contains(value, "-") {
		parts := strings.Split(value, "-")
		if len(parts) != 2 {
			return "", 0, 0, &Failure{"size", "ValueError"}
		}
		lower, err = parse(parts[0])
		if err != nil {
			return "", 0, 0, err
		}
		upper, err = parse(parts[1])
		return "between", lower, upper, err
	}
	if strings.HasPrefix(value, ">") {
		lower, err = parse(value[1:])
		return "gte", lower, 0, err
	}
	if strings.HasPrefix(value, "<") {
		upper, err = parse(value[1:])
		return "lte", 0, upper, err
	}
	return "unknown", 0, 0, nil
}
