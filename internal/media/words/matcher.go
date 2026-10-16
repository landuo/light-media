// Package words implements MoviePilot's CustomIdentifiers preprocessing. Rules
// transform recognition text only; callers retain the real filename and path.
package words

import (
	"container/list"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

const MaxBytes = 8 << 20

var errRecognitionLimit = errors.New("recognition processing exceeded limits")

type cached struct {
	pattern string
	re      *regexp2.Regexp
	err     error
}
type Matcher struct {
	words []string
	mu    sync.Mutex
	cache map[string]*list.Element
	lru   list.List
}
type Result struct {
	Title   string
	Applied []int
	Failed  int
	Limited bool
}

func New(rules []string) (*Matcher, error) {
	total := 0
	for _, rule := range rules {
		total += len(rule)
		if len(rule) > 64<<10 {
			return nil, errors.New("recognition rule too large")
		}
	}
	if total > MaxBytes || len(rules) > 100000 {
		return nil, errors.New("recognition word list too large")
	}
	return &Matcher{words: append([]string(nil), rules...), cache: map[string]*list.Element{}}, nil
}

// Python named captures use the default Unicode classes, dollar boundary and
// source capture order. RE2 mode would silently change these semantics.
func (m *Matcher) compile(pattern string) (*regexp2.Regexp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hit := m.cache[pattern]; hit != nil {
		m.lru.MoveToFront(hit)
		c := hit.Value.(cached)
		return c.re, c.err
	}
	re, err := pyregex.Compile(pattern, false)
	m.cache[pattern] = m.lru.PushFront(cached{pattern, re, err})
	if m.lru.Len() > 1024 {
		tail := m.lru.Back()
		delete(m.cache, tail.Value.(cached).pattern)
		m.lru.Remove(tail)
	}
	return re, err
}

var combined = regexp.MustCompile(`^\s*(.*?)\s*=>\s*(.*?)\s*&&\s*(.*?)\s*<>\s*(.*?)\s*>>\s*(.*?)\s*$`)

func (m *Matcher) Prepare(title string) Result {
	out := Result{Title: title, Applied: []int{}}
	if m == nil {
		return out
	}
	// Bound pathological remote rules without changing the on-disk user list.
	if len(title) > 64<<10 {
		out.Limited = true
		return out
	}
	deadline := time.Now().Add(2 * time.Second)
	for i, word := range m.words {
		if i%16 == 0 && time.Now().After(deadline) {
			out.Limited = true
			break
		}
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		var applied bool
		var err error
		switch {
		case strings.Contains(word, " => ") && strings.Contains(word, " && ") && strings.Contains(word, " >> ") && strings.Contains(word, " <> "):
			fields := combined.FindStringSubmatch(word)
			if fields == nil {
				out.Failed++
				continue
			}
			for n := 1; n < len(fields); n++ {
				fields[n] = strings.TrimSpace(fields[n])
			}
			out.Title, applied, err = m.replace(out.Title, fields[1], fields[2])
			if applied {
				out.Title, applied, err = m.offset(out.Title, fields[3], fields[4], fields[5])
			}
		case strings.Contains(word, " => "):
			fields := strings.Split(word, " => ")
			out.Title, applied, err = m.replace(out.Title, fields[0], fields[1])
		case strings.Contains(word, " >> ") && strings.Contains(word, " <> "):
			fields := strings.Split(word, " <> ")
			offset := strings.Split(fields[1], " >> ")
			if len(offset) < 2 {
				out.Failed++
				continue
			}
			out.Title, applied, err = m.offset(out.Title, fields[0], offset[0], offset[1])
		case strings.TrimSpace(word) != "":
			out.Title, applied, err = m.replace(out.Title, word, "")
		}
		if err != nil {
			out.Failed++
			if errors.Is(err, errRecognitionLimit) {
				out.Limited = true
				break
			}
		}
		if applied {
			out.Applied = append(out.Applied, i)
		}
	}
	return out
}

type replacementPart struct {
	literal   string
	group     int
	reference bool
}

// Python replacement syntax is deliberately independent of .NET's $ syntax.
func replacement(re *regexp2.Regexp, value string) ([]replacementPart, error) {
	parts := []replacementPart{}
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			parts = append(parts, replacementPart{literal: literal.String()})
			literal.Reset()
		}
	}
	group := func(name string) error {
		number := re.GroupNumberFromName(name)
		if n, err := strconv.Atoi(name); err == nil {
			number = -1
			for _, slot := range re.GetGroupNumbers() {
				if slot == n {
					number = n
					break
				}
			}
		}
		if number < 0 {
			return errors.New("invalid group reference")
		}
		flush()
		parts = append(parts, replacementPart{group: number, reference: true})
		return nil
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			literal.WriteByte(value[i])
			continue
		}
		i++
		if i == len(value) {
			return nil, errors.New("invalid replacement escape")
		}
		ch := value[i]
		if ch == 'g' && i+1 < len(value) && value[i+1] == '<' {
			end := strings.IndexByte(value[i+2:], '>')
			if end < 0 {
				return nil, errors.New("invalid group reference")
			}
			if err := group(value[i+2 : i+2+end]); err != nil {
				return nil, err
			}
			i += end + 2
			continue
		}
		if ch >= '0' && ch <= '9' {
			start := i
			if ch == '0' || (i+2 < len(value) && ch <= '7' && value[i+1] >= '0' && value[i+1] <= '7' && value[i+2] >= '0' && value[i+2] <= '7') {
				for i+1 < len(value) && i-start < 2 && value[i+1] >= '0' && value[i+1] <= '7' {
					i++
				}
				n, err := strconv.ParseInt(value[start:i+1], 8, 32)
				if err != nil || n > 255 {
					return nil, errors.New("invalid octal escape")
				}
				literal.WriteRune(rune(n))
				continue
			}
			if i+1 < len(value) && value[i+1] >= '0' && value[i+1] <= '9' {
				i++
			}
			if err := group(value[start : i+1]); err != nil {
				return nil, err
			}
			continue
		}
		escapes := map[byte]byte{'\\': '\\', 'n': '\n', 'r': '\r', 't': '\t', 'a': '\a', 'b': '\b', 'f': '\f', 'v': '\v'}
		if decoded, ok := escapes[ch]; ok {
			literal.WriteByte(decoded)
		} else if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			return nil, errors.New("invalid replacement escape")
		} else {
			literal.WriteByte('\\')
			literal.WriteByte(ch)
		}
	}
	flush()
	return parts, nil
}

func (m *Matcher) replace(title, pattern, value string) (string, bool, error) {
	re, err := m.compile(pattern)
	if err != nil {
		return title, false, err
	}
	parts, err := replacement(re, value)
	if err != nil {
		return title, false, err
	}
	count := 0
	out, err := re.ReplaceFunc(title, func(match regexp2.Match) string {
		count++
		var b strings.Builder
		for _, p := range parts {
			if p.reference {
				if g := match.GroupByNumber(p.group); g != nil {
					b.WriteString(g.String())
				}
			} else {
				b.WriteString(p.literal)
			}
		}
		return b.String()
	}, -1, -1)
	if err != nil || len(out) > 64<<10 {
		return title, false, errRecognitionLimit
	}
	return out, count > 0, nil
}

func (m *Matcher) offset(title, front, back, expression string) (string, bool, error) {
	for _, pattern := range []string{back, front} {
		if pattern != "" {
			re, err := m.compile(pattern)
			if err != nil {
				return title, false, err
			}
			found, err := re.MatchString(title)
			if err != nil {
				return title, false, errRecognitionLimit
			}
			if !found {
				return title, false, nil
			}
		}
	}
	re, err := m.compile(`(?<=` + front + `.*?)[0-9一二三四五六七八九十]+(?=.*?` + back + `)`)
	if err != nil {
		return title, false, err
	}
	// regex.findall returns captures instead of the whole match when either
	// locator captures. Original cn2an then fails; do not invent a new meaning.
	if len(re.GetGroupNumbers()) > 1 {
		return title, false, errors.New("capturing offset locator")
	}
	match, err := re.FindStringMatch(title)
	if err != nil {
		return title, false, errRecognitionLimit
	}
	if match == nil {
		return title, false, nil
	}
	type pair struct{ old, new string }
	pairs := []pair{}
	positions := map[string]int{}
	earlier := false
	for match != nil {
		old := match.String()
		number, err := chineseNumber(old)
		if err != nil {
			return title, false, err
		}
		next, err := calculate(expression, number)
		if err != nil {
			return title, false, err
		}
		if number > next {
			earlier = true
		} else if number < next {
			earlier = false
		}
		formatted, err := formatNumber(old, next)
		if err != nil {
			return title, false, err
		}
		if pos, ok := positions[old]; ok {
			pairs[pos].new = formatted
		} else {
			positions[old] = len(pairs)
			pairs = append(pairs, pair{old, formatted})
		}
		match, err = re.FindNextMatch(match)
		if err != nil {
			return title, false, errRecognitionLimit
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if earlier {
			return pairs[i].new < pairs[j].new
		}
		return pairs[i].new > pairs[j].new
	})
	for _, p := range pairs {
		next, _, err := m.replace(title, `(?<=`+front+`.*?)`+p.old+`(?=.*?`+back+`)`, p.new)
		if err != nil {
			return title, false, err
		}
		title = next
	}
	return title, true, nil
}
