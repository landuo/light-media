package app

import (
	"fmt"
	"strconv"
	"strings"
)

func originalChatMediaSeasonNotice(seasons []originalChatMediaSeason, missingOnly bool) string {
	var lines []string
	for _, season := range seasons {
		if len(season.needed) == 0 {
			continue
		}
		if !missingOnly {
			lines = append(lines, fmt.Sprintf("第 %d 季总 %d 集", season.number, len(season.all)))
			continue
		}
		missing := strconv.Itoa(len(season.all))
		if !season.whole {
			missing = originalChatMissingEpisodeRanges(season.needed)
		}
		lines = append(lines, fmt.Sprintf("第 %d 季缺失 %s 集", season.number, missing))
	}
	return strings.Join(lines, "\n")
}

// The season scope is in ascending provider episode order, as str_series
// sorts the source list before compressing consecutive values.
func originalChatMissingEpisodeRanges(episodes []int) string {
	var ranges []string
	for first := 0; first < len(episodes); {
		last := first
		for last+1 < len(episodes) && episodes[last+1] == episodes[last]+1 {
			last++
		}
		label := strconv.Itoa(episodes[first])
		if first != last {
			label += "-" + strconv.Itoa(episodes[last])
		}
		ranges = append(ranges, label)
		first = last + 1
	}
	return strings.Join(ranges, ",")
}
