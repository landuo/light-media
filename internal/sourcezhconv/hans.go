// Package sourcezhconv implements MoviePilot's pinned zhconv-rs 0.4.1 zh-hans
// behavior in Go, including its default MediaWiki conversion syntax.
// See NOTICE for the upstream code/data provenance and licenses.
package sourcezhconv

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

//go:embed hans.tsv
var table string

type trie struct {
	next     map[byte]*trie
	to       string
	terminal bool
}

func (t *trie) put(from, to string) {
	for i := 0; i < len(from); i++ {
		if t.next == nil {
			t.next = map[byte]*trie{}
		}
		if t.next[from[i]] == nil {
			t.next[from[i]] = &trie{}
		}
		t = t.next[from[i]]
	}
	t.to, t.terminal = to, true
}
func (t *trie) match(s string) (int, string) {
	n, to := 0, ""
	for i := 0; t != nil && i < len(s); i++ {
		t = t.next[s[i]]
		if t != nil && t.terminal {
			n, to = i+1, t.to
		}
	}
	return n, to
}

var base = func() *trie {
	t := &trie{}
	for line := range strings.SplitSeq(strings.TrimSuffix(table, "\n"), "\n") {
		from, to, ok := strings.Cut(line, "\t")
		if !ok || from == "" {
			panic("invalid embedded zh-hans table")
		}
		t.put(from, to)
	}
	return t
}()

// The fixed base table's maximum pattern is 12 UTF-8 bytes, so this scan has
// linear cost in input length. Custom in-page phrases have the same O(n*m)
// bound as upstream's secondary converter. Replacements are never re-converted.
func plain(text string, secondary *trie, removed map[string]bool) string {
	var out strings.Builder
	out.Grow(len(text))
	for pos := 0; pos < len(text); {
		n, to := base.match(text[pos:])
		sn, st := secondary.match(text[pos:])
		if sn > 0 && sn >= n {
			n, to = sn, st
		} else if n > 0 && removed[to] {
			// Upstream compares the matched TARGET to removes' source keys.
			// Preserve that observable behavior, including the one-rune skip.
			n = 0
		}
		if n == 0 {
			_, n = utf8.DecodeRuneInString(text[pos:])
			out.WriteString(text[pos : pos+n])
		} else {
			out.WriteString(to)
		}
		pos += n
	}
	return out.String()
}

var outer = regexp.MustCompile(`-\{|<script.*?>.*?</script>|<style.*?>.*?</style>|<code>.*?</code>|<pre.*?>.*?</pre>`)
var inner = regexp.MustCompile(`-\{|\}-`)
var global = regexp.MustCompile(`-\{.+?\}-`)

// Hans follows app/utils/zhconv.py's two-argument call, whose pinned dependency
// defaults wikitext=true. It intentionally does not implement other target
// variants or a general markup renderer.
func Hans(text string) (result string, err error) {
	// The pinned Rust parser raises PanicException for an empty mapping source.
	// Adapt that conversion failure to an error at the Go host boundary.
	defer func() {
		if problem := recover(); problem != nil {
			if _, ok := problem.(invalidRule); !ok {
				panic(problem)
			}
			err = fmt.Errorf("invalid zh-hans conversion rule: empty source")
		}
	}()
	return hans(text), nil
}

type invalidRule struct{}

func hans(text string) string {
	if !strings.Contains(text, "-{") && !strings.Contains(text, "<") {
		return plain(text, nil, nil)
	}
	adds, removed := map[string]string{}, map[string]bool{}
	for _, idx := range global.FindAllStringIndex(text, -1) {
		r, ok := parseRule(text[idx[0]+2 : idx[1]-2])
		if !ok || r.action == 0 {
			continue
		}
		for _, p := range r.pairs() {
			if p[0] == "" {
				continue
			}
			if r.action == '-' {
				removed[p[0]] = true
			} else {
				adds[p[0]] = p[1]
			}
		}
	}
	var secondary *trie
	for f, t := range adds {
		if !removed[f] {
			if secondary == nil {
				secondary = &trie{}
			}
			secondary.put(f, t)
		}
	}
	var out strings.Builder
	out.Grow(len(text))
	pos := 0
	for {
		idx := outer.FindStringIndex(text[pos:])
		if idx == nil {
			break
		}
		a, b := pos+idx[0], pos+idx[1]
		out.WriteString(plain(text[pos:a], secondary, removed))
		if text[a:b] != "-{" {
			out.WriteString(text[a:b])
			pos = b
			continue
		}
		pos = b
		pieces := []string{""}
		for {
			idx = inner.FindStringIndex(text[pos:])
			if idx == nil {
				break
			}
			a, b = pos+idx[0], pos+idx[1]
			last := len(pieces) - 1
			if text[a:b] == "-{" {
				pieces[last] += text[pos:a]
				pos = b
				if len(pieces) < 10 {
					pieces = append(pieces, "")
				}
				continue
			}
			piece := pieces[last] + text[pos:a]
			pieces = pieces[:last]
			r, ok := parseRule(piece)
			value := piece
			if ok {
				value = r.render()
			}
			if len(pieces) > 0 {
				pieces[len(pieces)-1] += value
			} else {
				out.WriteString(value)
			}
			pos = b
			if len(pieces) == 0 {
				break
			}
		}
		for _, piece := range pieces {
			out.WriteString("-{")
			out.WriteString(plain(piece, secondary, removed))
		}
	}
	out.WriteString(plain(text[pos:], secondary, removed))
	return out.String()
}
