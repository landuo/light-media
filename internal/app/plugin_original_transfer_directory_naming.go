package app

import (
	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
	"light-media/internal/releasemeta/legacyanime"
)

var originalTransferDiscNumber, _ = pyregex.Compile(`(?:disc|disk|dvd|cd)[\s._-]*0*(\d{1,3})`, true)
var originalTransferSeasonDisc, _ = pyregex.Compile(`(?:^|[^A-Za-z0-9])S\d{1,3}D0*(\d{1,3})(?:[^A-Za-z0-9]|$)`, true)

func originalTransferDiscMatch(pattern *regexp2.Regexp, value string) string {
	match, err := pattern.FindStringMatch(value)
	if err != nil || match == nil {
		return ""
	}
	number, err := legacyanime.Integer(match.GroupByNumber(1).String())
	if err != nil {
		return ""
	}
	return "Disc " + number.String()
}

// TransHandler.__get_tv_bluray_dir_path: part, source disc number, SxxDxx,
// unchanged source name, then Disc. It never invents an episode for a disc.
func originalTransferDiscFolder(part, name string) string {
	for _, value := range []string{part, name} {
		if found := originalTransferDiscMatch(originalTransferDiscNumber, value); found != "" {
			return found
		}
	}
	if found := originalTransferDiscMatch(originalTransferSeasonDisc, name); found != "" {
		return found
	}
	if name != "" {
		return name
	}
	return "Disc"
}
