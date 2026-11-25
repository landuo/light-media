package language

import (
	"fmt"
	"iter"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/markusmobius/go-dateparser/internal/data"
	"github.com/markusmobius/go-dateparser/internal/strutil"
)

var defaultLocaleNames = [...]func() ([]string, error){
	sync.OnceValues(func() ([]string, error) {
		return getLocaleNames(nil, nil, "", false, false)
	}),
	sync.OnceValues(func() ([]string, error) {
		return getLocaleNames(nil, nil, "", true, false)
	}),
}

// GetLocales returns list of locale data based on the specified `locales`, `languages` and `region`.
//
// Parameter `locales` is a list of codes of locales which are to be loaded e.g. ['fr-PF', 'qu-EC',
// 'af-NA'], `languages` is a list of language codes e.g. ['en', 'es', 'zh-Hant'], and `region` is
// a region code, e.g. 'IN', '001', 'NE'. If `locales` are empty, languages and region are used to
// construct locales to load.
//
// If `useGivenOrder` is true, the returned list is ordered in the order locales are given. If set
// to false, the list will be sorted by how common the locale is used in the world.
//
// If `allowConflictingLocales` is set to true, locales with same language and different region
// can be loaded.
func GetLocales(locales []string, languages []string, region string, useGivenOrder bool, allowConflictingLocales bool) ([]*data.LocaleData, error) {
	seq, err := IterLocales(locales, languages, region, useGivenOrder, allowConflictingLocales)
	if err != nil {
		return nil, err
	}
	return slices.Collect(seq), nil
}

// IterLocales validates and orders names up front. Date parsing stops at its
// first successful locale, so later language tables are never materialized.
func IterLocales(locales, languages []string, region string, useGivenOrder, allowConflictingLocales bool) (iter.Seq[*data.LocaleData], error) {
	var names []string
	var err error
	if len(locales) == 0 && len(languages) == 0 && strings.TrimSpace(region) == "" {
		idx := 0
		if useGivenOrder {
			idx = 1
		}
		names, err = defaultLocaleNames[idx]()
	} else {
		names, err = getLocaleNames(locales, languages, region, useGivenOrder, allowConflictingLocales)
	}
	return func(yield func(*data.LocaleData) bool) {
		for _, name := range names {
			locale, _ := data.GetLocaleData(name)
			if !yield(locale) {
				return
			}
		}
	}, err
}

func getLocaleNames(locales, languages []string, region string, useGivenOrder, allowConflictingLocales bool) ([]string, error) {
	var validLocales []string
	localeTracker := strutil.NewDict()
	languageTracker := strutil.NewDict()

	// If locales specified, process it first
	if len(locales) > 0 {
		var invalidLocales []string

		for _, locale := range locales {
			// If locale already registered, skip
			if localeTracker.Contain(locale) {
				continue
			}

			// Check if the locale valid
			if !data.HasLocale(locale) {
				invalidLocales = append(invalidLocales, locale)
				continue
			}

			// Make sure language is only used once
			if !allowConflictingLocales {
				language := strutil.RemoveRegion(locale)
				if languageTracker.Contain(language) {
					return nil, ErrConflictingLocales
				}

				languageTracker.Add(language)
			}

			// Save the locale
			localeTracker.Add(locale)
			validLocales = append(validLocales, locale)
		}

		// If there are invalid locales, stop
		if len(invalidLocales) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrUnknownLocales, strings.Join(invalidLocales, ", "))
		}
	} else {
		// If languages is not specified, generate it from language order
		if len(languages) == 0 {
			languages = make([]string, len(data.LanguageOrder))
			for lang, order := range data.LanguageOrder {
				languages[order] = lang
			}
		}

		// Tidy up region value
		region = strings.ToUpper(region)
		region = strings.TrimSpace(region)

		// Generate locales from languages and regions
		var unknownLanguages []string

		for _, language := range languages {
			// If language already checked, skip
			if languageTracker.Contain(language) {
				continue
			} else {
				languageTracker.Add(language)
			}

			// Check if language is known
			if _, exist := data.LanguageOrder[language]; !exist {
				unknownLanguages = append(unknownLanguages, language)
				continue
			}

			// Generate and save locale
			locale := language
			if region != "" {
				locale = language + "-" + region
			}

			// If locale already registered, skip
			if localeTracker.Contain(locale) {
				continue
			}

			// Make sure generated locale is valid
			if !data.HasLocale(locale) {
				continue
			}

			// Save the generated locale
			localeTracker.Add(locale)
			validLocales = append(validLocales, locale)
		}

		// If there are unknown languages, stop
		if len(unknownLanguages) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrUnknownLanguages, strings.Join(unknownLanguages, ", "))
		}
	}

	// If required, sort the locales
	if !useGivenOrder {
		sort.Slice(validLocales, func(a, b int) bool {
			localeA := validLocales[a]
			localeB := validLocales[b]
			orderA := data.LocaleOrder[localeA]
			orderB := data.LocaleOrder[localeB]

			if orderA != orderB {
				return orderA < orderB
			} else {
				return localeA < localeB
			}
		})
	}

	if len(validLocales) == 0 {
		return nil, ErrNotFound
	}
	return validLocales, nil
}

// GetLocale returns a single locale instance based on the specified locale `code`,
// e.g. 'fr-PF', 'qu-EC', 'af-NA'.
func GetLocale(code string) (*data.LocaleData, error) {
	locales, err := GetLocales([]string{code}, nil, "", false, false)
	if err != nil {
		return nil, err
	}

	return locales[0], nil
}
