// Package zhconv implements the consumed zhconv-rs 0.4.2 two-argument Hans
// conversion, including its default MediaWiki rule processing. Algorithm port:
// MIT OR Apache-2.0; embedded MediaWiki data: GPL-2.0-or-later. See licenses/.
package zhconv

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed data/zh_to_hans.json
var hansData []byte

const hansDataSHA256 = "dfda8298299cdd7c6ddb19d0c76a1819605f9a6640523b7412bdcb92c1eb98f4"

var (
	builtinOnce    sync.Once
	builtinHans    *trie
	builtinErr     error
	ErrInvalidUTF8 = errors.New("zhconv: input is not valid UTF-8")
)

// SourcePanicError represents the explicit assertion in the original Rust
// rule parser. It is not an ordinary invalid-rule fallback, nor a claim about
// a particular Python wrapper exception class.
type SourcePanicError struct{}

func (*SourcePanicError) Error() string { return "assertion failed: 0 < i && i < s.len()" }

// SourceClass is bound to the actual native source4789 observation. Go returns
// a typed error rather than panicking the application process.
func (*SourcePanicError) SourceClass() string { return "PanicException" }

// Hans performs exactly the fixed zh-hans/default-wikitext operation. It does
// not expose alternative dictionaries, network lookup or a mutable registry.
func Hans(text string) (string, error) {
	if !utf8.ValidString(text) {
		return "", ErrInvalidUTF8
	}
	builtinOnce.Do(loadHans)
	if builtinErr != nil {
		return "", builtinErr
	}
	return convertWikitext(text, builtinHans)
}

func loadHans() {
	h := sha256.Sum256(hansData)
	if hex.EncodeToString(h[:]) != hansDataSHA256 {
		builtinErr = errors.New("zhconv: embedded Hans data checksum mismatch")
		return
	}
	var pairs [][2]string
	if err := json.Unmarshal(hansData, &pairs); err != nil {
		builtinErr = err
		return
	}
	if len(pairs) != 4685 {
		builtinErr = errors.New("zhconv: embedded Hans pair count mismatch")
		return
	}
	t := newTrie()
	for _, p := range pairs {
		if p[0] == "" || !utf8.ValidString(p[0]) || !utf8.ValidString(p[1]) {
			builtinErr = errors.New("zhconv: invalid embedded Hans pair")
			return
		}
		if !t.insert(p[0], p[1]) {
			builtinErr = fmt.Errorf("zhconv: duplicate embedded source %q", p[0])
			return
		}
	}
	builtinHans = t
}

type trie struct {
	next     map[rune]*trie
	value    string
	terminal bool
}

func newTrie() *trie { return &trie{next: make(map[rune]*trie)} }

// insert reports whether the source was previously absent. Construction is
// private and completed before publication; the published trie is read-only.
func (t *trie) insert(source, target string) bool {
	for _, r := range source {
		n := t.next[r]
		if n == nil {
			n = newTrie()
			t.next[r] = n
		}
		t = n
	}
	fresh := !t.terminal
	t.value, t.terminal = target, true
	return fresh
}

func (t *trie) longest(text string, pos int) (end int, value string, found bool) {
	if t == nil {
		return
	}
	for i := pos; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		t = t.next[r]
		if t == nil {
			break
		}
		i += n
		if t.terminal {
			end, value, found = i, t.value, true
		}
	}
	return
}

type conversion struct {
	base, secondary *trie
	removed         map[string]struct{}
}

// write is leftmost-longest, nonoverlapping and noncascading. The original
// secondary wins only at an earlier start or an equal start with >= length.
// Iterating rune positions gives that same choice without serialized DAAC.
func (c conversion) write(out *strings.Builder, text string) {
	for pos := 0; pos < len(text); {
		be, bv, bok := c.base.longest(text, pos)
		se, sv, sok := c.secondary.longest(text, pos)
		if sok && (!bok || se >= be) {
			out.WriteString(sv)
			pos = se
			continue
		}
		if bok {
			// Preserve actual converter.rs behavior: test replacement, NOT the
			// source substring suggested by the upstream variable/comment.
			if _, removed := c.removed[bv]; !removed {
				out.WriteString(bv)
				pos = be
				continue
			}
		}
		// A removed longest builtin skips one original rune; no shorter
		// alternative at this same start is reconsidered.
		_, n := utf8.DecodeRuneInString(text[pos:])
		out.WriteString(text[pos : pos+n])
		pos += n
	}
}
