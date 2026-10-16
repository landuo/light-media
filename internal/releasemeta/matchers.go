package releasemeta

import (
	"errors"
	"sort"
	"strings"

	"light-media/internal/pyregex"
)

func matchTeam(title string, options Options) (string, error) {
	if title == "" {
		return "", nil
	}
	groups := builtinGroups
	for _, value := range options.ReleaseGroups {
		if value != "" {
			groups += "|" + value
		}
	}
	re, err := pyregex.Compile(`(?<=[-@\[￡【&])(?:(?:`+groups+`))(?=$|[@.\s\]\[】&])`, true)
	if err != nil {
		if options.SourceMode == "rust" {
			return "", nil
		}
		return "", errors.New("release_meta_release_groups_invalid")
	}
	unique := []string{}
	for m, err := re.FindStringMatch(title + " "); m != nil && err == nil; m, err = re.FindNextMatch(m) {
		value := m.String()
		if options.SourceMode == "legacy" && len(m.Groups()) > 1 {
			value = m.Groups()[1].String()
		}
		found := false
		for _, prior := range unique {
			found = found || prior == value
		}
		if !found {
			unique = append(unique, value)
		}
	}
	return strings.Join(unique, "@"), nil
}

func matchCustomization(title string, options Options) (string, error) {
	patterns := []string{}
	for _, pattern := range options.Customization {
		if pattern != "" {
			patterns = append(patterns, "("+pattern+")")
		}
	}
	if len(patterns) == 0 || title == "" {
		return "", nil
	}
	re, err := pyregex.Compile(strings.Join(patterns, "|"), false)
	if err != nil {
		if options.SourceMode == "rust" {
			return "", nil
		}
		return "", errors.New("release_meta_customization_invalid")
	}
	type captured struct {
		value string
		index int
	}
	unique := []captured{}
	for m, err := re.FindStringMatch(title); m != nil && err == nil; m, err = re.FindNextMatch(m) {
		for i, group := range m.Groups()[1:] {
			value := group.String()
			if value == "" {
				continue
			}
			found := false
			for _, prior := range unique {
				found = found || prior.value == value
			}
			if found {
				continue
			}
			if options.SourceMode == "rust" {
				for n := range unique {
					if unique[n].index == i {
						unique = append(unique[:n], unique[n+1:]...)
						break
					}
				}
			}
			unique = append(unique, captured{value, i})
		}
	}
	sort.SliceStable(unique, func(i, j int) bool { return unique[i].index < unique[j].index })
	result := []string{}
	for _, group := range unique {
		result = append(result, group.value)
	}
	return strings.Join(result, "@"), nil
}
