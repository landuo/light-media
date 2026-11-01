// Algorithm adapted from zhconv-rs 0.4.2 converter.rs, including its specific
// non-MediaWiki-compliant cases. MIT OR Apache-2.0; see licenses/.
package zhconv

import (
	"regexp"
	"strings"
)

const nestedRuleMaxDepth = 10

var (
	globalRulePattern = regexp.MustCompile(`-\{.+?\}-`)
	outerPattern      = regexp.MustCompile(`-\{|<script.*?>.*?</script>|<style.*?>.*?</style>|<code>.*?</code>|<pre.*?>.*?</pre>`)
	innerPattern      = regexp.MustCompile(`-\{|\}-`)
)

func pageConversion(text string, base *trie) (conversion, error) {
	adds := make(map[string]string)
	removes := make(map[string]struct{})
	for _, loc := range globalRulePattern.FindAllStringIndex(text, -1) {
		r, err := parseRule(text[loc[0]+2 : loc[1]-2])
		if err != nil {
			if _, fatal := err.(*SourcePanicError); fatal {
				return conversion{}, err
			}
			continue // PageRules ignores ordinary invalid conversion rules.
		}
		if r.action == noAction {
			continue
		}
		for _, p := range r.conv.pairsHans() {
			if p.from == "" {
				continue
			}
			if r.action == addAction {
				adds[p.from] = p.to
			} else {
				removes[p.from] = struct{}{}
			}
		}
	}
	var secondary *trie
	for from, to := range adds {
		if _, removed := removes[from]; removed {
			continue
		}
		if secondary == nil {
			secondary = newTrie()
		}
		secondary.insert(from, to)
	}
	return conversion{base: base, secondary: secondary, removed: removes}, nil
}

func convertWikitext(text string, base *trie) (string, error) {
	c, err := pageConversion(text, base)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	pos := 0
	var pieces []string
	for pos < len(text) {
		m := outerPattern.FindStringIndex(text[pos:])
		if m == nil {
			break
		}
		start, end := pos+m[0], pos+m[1]
		c.write(&out, text[pos:start])
		if text[start:end] != "-{" {
			out.WriteString(text[start:end])
			pos = end
			continue
		}
		pos = start + 2
		pieces = append(pieces, "")
		for pos < len(text) {
			n := innerPattern.FindStringIndex(text[pos:])
			if n == nil {
				break
			}
			ns, ne := pos+n[0], pos+n[1]
			if text[ns:ne] == "-{" {
				last := len(pieces) - 1
				pieces[last] += text[pos:ns]
				pos = ne
				if len(pieces) >= nestedRuleMaxDepth {
					continue
				}
				pieces = append(pieces, "")
			} else {
				last := len(pieces) - 1
				piece := pieces[last] + text[pos:ns]
				pieces = pieces[:last]
				r, err := parseRuleInfallible(piece)
				if err != nil {
					return "", err
				}
				if len(pieces) > 0 {
					pieces[len(pieces)-1] += r.render()
				} else {
					out.WriteString(r.render())
				}
				pos = ne
				if len(pieces) == 0 {
					break
				}
			}
		}
		for _, piece := range pieces {
			out.WriteString("-{")
			c.write(&out, piece)
		}
		pieces = pieces[:0]
	}
	if pos < len(text) {
		c.write(&out, text[pos:])
	}
	return out.String(), nil
}
