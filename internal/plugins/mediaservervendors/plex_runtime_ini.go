package mediaservervendors

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
)

// ParsePlexRuntimeINI builds the consumed _asDict view of default ConfigParser:
// raw strings, no interpolation or DEFAULT inheritance. No scanner line cap.
// This draft must be source-differentially checked before runtime admission.
func ParsePlexRuntimeINI(raw []byte) (map[string]map[string]string, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("Plex config UTF-8 decode failed")
	}
	type section struct {
		name   string
		keys   []string
		values map[string][]string
	}
	sections := []*section{}
	byName := map[string]*section{}
	defaults := &section{name: "DEFAULT", values: map[string][]string{}}
	var current *section
	option := ""
	indent := 0
	hadParseError := false
	seenOptions := map[string]map[string]bool{}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n"), "\n")
	for index, line := range lines {
		value := legacyanime.Strip(line)
		if strings.HasPrefix(value, "#") || strings.HasPrefix(value, ";") {
			continue
		}
		if value == "" {
			if current != nil && option != "" {
				current.values[option] = append(current.values[option], "")
			}
			continue
		}
		leading := 0
		for _, r := range line {
			if legacyanime.Strip(string(r)) != "" {
				break
			}
			leading++
		}
		if current != nil && option != "" && leading > indent {
			current.values[option] = append(current.values[option], value)
			continue
		}
		indent = leading
		if strings.HasPrefix(value, "[") {
			end := strings.LastIndex(value, "]")
			if end > 1 {
				name := value[1:end]
				if name == "DEFAULT" {
					current = defaults
				} else {
					if _, exists := byName[name]; exists {
						return nil, fmt.Errorf("Plex config duplicate section at line %d", index+1)
					}
					current = &section{name: name, values: map[string][]string{}}
					sections = append(sections, current)
					byName[name] = current
				}
				option = ""
				continue
			}
		}
		if current == nil {
			return nil, fmt.Errorf("Plex config missing section at line %d", index+1)
		}
		at := strings.IndexAny(value, "=:")
		if at < 0 {
			hadParseError = true
			continue
		}
		option = legacyanime.LowerCase(strings.TrimRightFunc(value[:at], func(r rune) bool { return legacyanime.Strip(string(r)) == "" }))
		if option == "" {
			hadParseError = true
		}
		if seenOptions[current.name] == nil {
			seenOptions[current.name] = map[string]bool{}
		}
		if seenOptions[current.name][option] {
			return nil, fmt.Errorf("Plex config duplicate option at line %d", index+1)
		}
		seenOptions[current.name][option] = true
		current.keys = append(current.keys, option)
		current.values[option] = []string{legacyanime.Strip(value[at+1:])}
	}
	if hadParseError {
		return nil, fmt.Errorf("Plex config parsing failed")
	}
	result := map[string]map[string]string{}
	for _, s := range sections {
		name := legacyanime.LowerCase(s.name)
		for _, key := range s.keys {
			if key != "__name__" {
				if result[name] == nil {
					result[name] = map[string]string{}
				}
				result[name][legacyanime.LowerCase(key)] = strings.TrimRightFunc(strings.Join(s.values[key], "\n"), func(r rune) bool { return legacyanime.Strip(string(r)) == "" })
			}
		}
	}
	return result, nil
}
