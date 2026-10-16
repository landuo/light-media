package releasemeta

import (
	"light-media/internal/releasemeta/rustanime"
	"strings"
)

// Full name pair retained from the source-consistent name selection wrapper.
func fullRustAnimeNames(name, original, prepared string) (string, string) {
	mode := "rust"
	cn, en := "", ""
	split := true
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		if animeContainsChinese(parts[0]) {
			cn = strings.TrimSpace(parts[0])
			en = strings.TrimSpace(parts[1])
			if !rustAnimeKeepChinese(cn, en, original, prepared) {
				cn = ""
			}
			split = false
		} else if animeContainsChinese(parts[len(parts)-1]) {
			cn = strings.TrimSpace(parts[len(parts)-1])
			en = strings.TrimSpace(parts[0])
			if !rustAnimeKeepChinese(cn, en, original, prepared) {
				cn = ""
			}
			split = false
		} else {
			name = parts[len(parts)-1]
		}
	}
	if split {
		last := ""
		for _, word := range strings.Fields(name) {
			word = strings.TrimSuffix(word, "]")
			if word == "" || digits(word) {
				if last == "cn" {
					cn += " " + word
				} else if last == "en" {
					en += " " + word
				}
			} else if animeContainsChinese(word) {
				cn += " " + word
				last = "cn"
			} else {
				en += " " + word
				last = "en"
			}
		}
		cn, en = strings.TrimSpace(cn), strings.TrimSpace(en)
	}
	// Restore only a slash candidate that is already ASCII-equivalent to en.
	if parts := strings.Split(original, "/"); len(parts) > 1 && en != "" {
		rest := strings.TrimSpace(parts[1])
		end := len(rest)
		for _, delimiter := range []string{" - ", " [", "[", "]", "("} {
			if at := strings.Index(rest, delimiter); at >= 0 && at < end {
				end = at
			}
		}
		candidate := strings.TrimSpace(rest[:end])
		key := rustAnimeASCIIKey(candidate)
		if strings.ContainsAny(candidate, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") && key != "" && rustAnimeASCIIKey(en) == key {
			en = candidate
		}
	}
	cn = rustAnimeCleanChinese(cn)
	if en != "" {
		cleaned := fullRXReplace(rustAnimeChineseSeason, en, "", false, mode)
		cleaned = fullRXReplace(`[\x{4e00}-\x{9fff}]+`, cleaned, " ", false, mode)
		cleaned = strings.TrimSpace(fullRXReplace(rustAnimeNoString, cleaned, "", true, mode))
		if cleaned != "" {
			en = rustanime.TitleCase(cleaned)
		}
	}
	return cn, en
}
