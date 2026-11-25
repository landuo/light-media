package data

import (
	"slices"
	"strings"
	"sync"
	"weak"
)

type localeFamily struct {
	initialize func()
	names      []string
}

var localeFamilies = func() map[string]*localeFamily {
	result := make(map[string]*localeFamily, len(LocaleOrder))
	for name, initialize := range localeConstructors {
		family := &localeFamily{initialize: initialize, names: append([]string{name}, LanguageLocalesMap[name]...)}
		for _, code := range family.names {
			result[code] = family
		}
	}
	return result
}()

func HasLocale(code string) bool { return localeFamilies[code] != nil }

var localeCache struct {
	sync.Mutex
	// Active callers keep their immutable tables. Weak references preserve
	// pointer identity for them without pinning every language ever tried.
	byName map[string]weak.Pointer[LocaleData]
	recent [8]*LocaleData
}

func GetLocaleData(code string) (*LocaleData, bool) {
	family := localeFamilies[code]
	if family == nil {
		return nil, false
	}
	localeCache.Lock()
	defer localeCache.Unlock()
	result := localeCache.byName[code].Value()
	if result == nil {
		if localeCache.byName == nil {
			localeCache.byName = make(map[string]weak.Pointer[LocaleData], len(localeFamilies))
		}
		// Constructors use package variables to assemble regional variants.
		// Publish independent immutable values, then release the staging slots.
		family.initialize()
		for _, name := range family.names {
			slot, _ := generatedLocaleSlot(name)
			value := localeCache.byName[name].Value()
			if value == nil {
				value = new(LocaleData)
				*value = *slot
				value.wordDictionary = sync.OnceValue(value.buildWordDictionary)
				localeCache.byName[name] = weak.Make(value)
			}
			if name == code {
				result = value
			}
			*slot = LocaleData{}
		}
	}
	index := slices.Index(localeCache.recent[:], result)
	if index < 0 {
		index = len(localeCache.recent) - 1
	}
	copy(localeCache.recent[1:index+1], localeCache.recent[:index])
	localeCache.recent[0] = result
	return result, true
}

func (ld *LocaleData) WordDictionary() map[string]bool {
	if ld.wordDictionary != nil {
		return ld.wordDictionary()
	}
	return ld.buildWordDictionary()
}

func (ld *LocaleData) buildWordDictionary() map[string]bool {
	dictionary := map[string]bool{}
	add := func(phrase string, meaningful bool) {
		if !strings.Contains(phrase, " ") {
			dictionary[phrase] = dictionary[phrase] || meaningful
			return
		}
		for word := range strings.FieldsSeq(phrase) {
			dictionary[word] = dictionary[word] || meaningful
		}
	}
	for phrase, translations := range ld.Translations {
		add(phrase, slices.ContainsFunc(translations, func(word string) bool { return word != "" }))
	}
	for phrase, translation := range ld.RelativeType {
		add(phrase, translation != "")
	}
	return dictionary
}
