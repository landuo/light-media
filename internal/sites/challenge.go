package sites

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

// The passive JS Detection asset also appears on authenticated pages. Ignore
// only this exact path after lowercasing, not other challenge-platform URLs.
const passiveChallengePath = "/cdn-cgi/challenge-platform/scripts/jsd/main.js"

var loginChallengeMarkers = [...]string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"}

// All needles are ASCII. A rune whose Unicode lowercase is still non-ASCII
// (including RuneError for invalid UTF-8) is just a non-matching separator.
func lowerChallengeByte(b []byte) (byte, int) {
	c := b[0]
	if c < utf8.RuneSelf {
		return lowerASCII(c), 1
	}
	return lowerNonASCII(b)
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func lowerNonASCII(b []byte) (byte, int) {
	r, n := utf8.DecodeRune(b)
	if r = unicode.ToLower(r); r < utf8.RuneSelf {
		return byte(r), n
	}
	return 0, n
}

// passivePathLen is called only on a slash. Count source bytes, not runes:
// for example, U+0130 lowercases to ASCII i inside the path.
func passivePathLen(b []byte) int {
	n := 1
	for i := 1; i < len(passiveChallengePath); i++ {
		if n == len(b) {
			return 0
		}
		c, size := lowerChallengeByte(b[n:])
		if c != passiveChallengePath[i] {
			return 0
		}
		n += size
	}
	return n
}

func hasLoginChallenge(b []byte) bool {
	// Search the logical stream after a single non-recursive ReplaceAll.
	// Carry the suffix across blocks so even markers joined by removing one
	// or more paths can match. No buffer grows with the response size.
	var block [4096]byte
	n, overlap := 0, 0
	for _, marker := range loginChallengeMarkers {
		overlap = max(overlap, len(marker)-1)
	}
	for len(b) > 0 {
		if b[0] == '/' {
			if size := passivePathLen(b); size != 0 {
				b = b[size:]
				continue
			}
		}
		if b[0] < utf8.RuneSelf {
			block[n] = lowerASCII(b[0])
			b = b[1:]
		} else {
			c, size := lowerNonASCII(b)
			block[n] = c
			b = b[size:]
		}
		n++
		if n == len(block) {
			if containsChallengeMarker(block[:]) {
				return true
			}
			n = copy(block[:], block[n-overlap:])
		}
	}
	return containsChallengeMarker(block[:n])
}

func containsChallengeMarker(block []byte) bool {
	for _, marker := range loginChallengeMarkers {
		if bytes.Contains(block, []byte(marker)) {
			return true
		}
	}
	return false
}
