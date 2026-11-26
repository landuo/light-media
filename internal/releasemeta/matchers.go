package releasemeta

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

func matchTeam(title string, options Options) (string, error) {
	value, err := matchTeamOption(title, options)
	if value == nil {
		return "", err
	}
	return *value, err
}

const releaseTeamPrefix = `(?<=[-@\[￡【&])(?:(?:`
const releaseTeamSuffix = `))(?=$|[@.\s\]\[】&])`

// The installed group expression never varies between parser instances.
var builtinReleaseTeam = sync.OnceValues(func() (*regexp2.Regexp, error) {
	return pyregex.Compile(releaseTeamPrefix+builtinGroups+releaseTeamSuffix, true)
})

// Rust Option<String> distinguishes a nullable-expression match Some("") from
// no match. Its anime wrapper uses Option::or, not a string-truthiness fallback.
func matchTeamOption(title string, options Options) (*string, error) {
	if title == "" {
		return nil, nil
	}
	groups := builtinGroups
	for _, value := range options.ReleaseGroups {
		if value != "" {
			groups += "|" + value
		}
	}
	var re *regexp2.Regexp
	var err error
	if groups == builtinGroups {
		if !strings.ContainsAny(title, "-@[￡【&") {
			return nil, nil
		}
		re, err = builtinReleaseTeam()
	} else {
		re, err = options.patterns.compile(releaseTeamPrefix+groups+releaseTeamSuffix, true)
	}
	if err != nil {
		if options.SourceMode == "rust" {
			return nil, nil
		}
		return nil, errors.New("release_meta_release_groups_invalid")
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
	if len(unique) == 0 {
		return nil, nil
	}
	value := strings.Join(unique, "@")
	return &value, nil
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
	re, err := options.patterns.compile(strings.Join(patterns, "|"), false)
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
