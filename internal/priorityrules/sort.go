package priorityrules

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// SortInput is the data consumed by the original TorrentHelper.sort_torrents.
// Metadata parsing belongs to its caller; counts must describe the resource,
// rather than the episodes the user is currently requesting.
type SortInput struct {
	Title     string `json:"title"`
	SiteOrder int    `json:"site_order"`
	Upload    int64  `json:"upload"`
	Priority  int    `json:"priority"`
	Seeders   int    `json:"seeders"`
	Seasons   int    `json:"seasons"`
	Episodes  int    `json:"episodes"`
}

func pad(value string, width int, left bool, fill string) string {
	if count := width - utf8.RuneCountInString(value); count > 0 {
		if left {
			return strings.Repeat(fill, count) + value
		}
		return value + strings.Repeat(fill, count)
	}
	return value
}
func SortKey(input SortInput, priorities []string) string {
	if len(priorities) == 0 {
		priorities = []string{"torrent", "upload", "seeder"}
	}
	var key strings.Builder
	key.WriteString(pad(input.Title, 200, false, " "))
	for _, priority := range priorities {
		switch priority {
		case "torrent":
			key.WriteString(pad(strconv.Itoa(input.Priority), 3, true, "0"))
		case "site":
			key.WriteString(pad(strconv.Itoa(999-input.SiteOrder), 3, true, "0"))
		case "upload":
			key.WriteString(pad(strconv.FormatInt(input.Upload, 10), 30, true, "0"))
		case "seeder":
			key.WriteString(pad(strconv.Itoa(input.Seeders), 10, true, "0"))
		}
	}
	key.WriteString(pad(strconv.Itoa(input.Seasons), 3, true, "0"))
	if input.Episodes == 0 {
		key.WriteString("9999")
	} else {
		key.WriteString(pad(strconv.Itoa(input.Episodes), 4, true, "0"))
	}
	return key.String()
}
