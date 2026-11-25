package autosignin

import (
	"embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"

	plugins "light-media/builtinplugins/internal/sdk"
)

// Only Response.translate_text's static messages and ordered message_patterns
// are included. No language settings, ContextVar, or general locale framework.
//
//go:embed locales/*.json
var localeFiles embed.FS

type messagePattern struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	compiled *regexp.Regexp
}
type messageCatalog struct {
	Messages map[string]string `json:"messages"`
	Patterns []messagePattern  `json:"message_patterns"`
}

var catalogFields = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
var messageCatalogs = map[string]func() messageCatalog{
	"zh-CN": sync.OnceValue(func() messageCatalog { return loadMessageCatalog("zh-CN") }),
	"zh-TW": sync.OnceValue(func() messageCatalog { return loadMessageCatalog("zh-TW") }),
	"en-US": sync.OnceValue(func() messageCatalog { return loadMessageCatalog("en-US") }),
}

func loadMessageCatalog(locale string) messageCatalog {
	raw, e := localeFiles.ReadFile("locales/" + locale + ".json")
	if e != nil {
		panic(e)
	}
	var catalog messageCatalog
	if e := json.Unmarshal(raw, &catalog); e != nil {
		panic(e)
	}
	for i := range catalog.Patterns {
		pattern := &catalog.Patterns[i]
		if !catalogFields.MatchString(pattern.Source) {
			continue
		}
		var b strings.Builder
		last := 0
		for _, span := range catalogFields.FindAllStringSubmatchIndex(pattern.Source, -1) {
			b.WriteString(regexp.QuoteMeta(pattern.Source[last:span[0]]))
			b.WriteString("(?P<" + pattern.Source[span[2]:span[3]] + ">.+?)")
			last = span[1]
		}
		b.WriteString(regexp.QuoteMeta(pattern.Source[last:]))
		pattern.compiled = regexp.MustCompile(`\A` + b.String() + `\z`)
	}
	return catalog
}
func matchLocale(locale string) string {
	locale = strings.ToLower(strings.ReplaceAll(strings.TrimFunc(locale, sourcePythonSpace), "_", "-"))
	switch locale {
	case "zh", "zh-cn", "zh-hans", "zh-hans-cn":
		return "zh-CN"
	case "zh-tw", "zh-hant", "zh-hant-tw":
		return "zh-TW"
	case "en", "en-us":
		return "en-US"
	}
	return ""
}
func normalizeLocale(locale string) string {
	if got := matchLocale(locale); got != "" {
		return got
	}
	return "zh-CN"
}
func requestLocale(request plugins.HTTPRequest) string {
	if locale := request.LastQuery("locale"); locale != "" {
		return normalizeLocale(locale)
	}
	for _, header := range []string{"x-moviepilot-locale", "x-locale"} {
		if locale := request.Headers[header]; locale != "" {
			return normalizeLocale(locale)
		}
	}
	type choice struct {
		quality float64
		locale  string
	}
	choices := []choice{}
	for _, item := range strings.Split(request.Headers["accept-language"], ",") {
		parts := []string{}
		for _, part := range strings.Split(item, ";") {
			if part = strings.TrimFunc(part, sourcePythonSpace); part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			continue
		}
		quality := 1.0
		for _, part := range parts[1:] {
			if strings.HasPrefix(part, "q=") {
				var e error
				var raw any
				raw, e = sourceFloat(part[2:])
				if value, ok := raw.(float64); ok {
					quality = value
				} else {
					quality = 0
				}
				if e != nil {
					quality = 0
				}
			}
		}
		choices = append(choices, choice{quality, parts[0]})
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].quality > choices[j].quality })
	for _, choice := range choices {
		if locale := matchLocale(choice.locale); locale != "" {
			return locale
		}
	}
	return "zh-CN"
}
func lookupMessage(locale, text string) (string, bool) {
	load := messageCatalogs[locale]
	if load == nil {
		return "", false
	}
	value, ok := load().Messages[text]
	return value, ok
}
func lookupPattern(locale, text string) (string, bool) {
	load := messageCatalogs[locale]
	if load == nil {
		return "", false
	}
	for _, pattern := range load().Patterns {
		if pattern.compiled == nil {
			continue
		}
		matched := pattern.compiled.FindStringSubmatch(text)
		if matched == nil {
			continue
		}
		values := map[string]string{}
		for i, name := range pattern.compiled.SubexpNames() {
			if i == 0 || name == "" {
				continue
			}
			value := matched[i]
			values[name] = value
			translated, ok := lookupMessage(locale, value)
			if !ok && locale != "zh-CN" {
				translated, ok = lookupMessage("zh-CN", value)
			}
			if !ok || translated == "" {
				translated = value
			}
			values[name+"_i18n"] = translated
		}
		complete := true
		formatted := catalogFields.ReplaceAllStringFunc(pattern.Target, func(field string) string {
			value, ok := values[field[1:len(field)-1]]
			if !ok {
				complete = false
				return field
			}
			return value
		})
		if !complete {
			formatted = pattern.Target
		}
		return formatted, true
	}
	return "", false
}
func translateMessage(text, locale string) string {
	if text == "" {
		return ""
	}
	locale = normalizeLocale(locale)
	chinese := false
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			chinese = true
			break
		}
	}
	translated, found := lookupMessage(locale, text)
	if !found && chinese {
		translated, found = lookupPattern(locale, text)
	}
	if !found && locale != "zh-CN" {
		translated, found = lookupMessage("zh-CN", text)
	}
	if !found && locale != "zh-CN" && chinese {
		translated, found = lookupPattern("zh-CN", text)
	}
	if !found || translated == "" {
		return text
	}
	return translated
}
