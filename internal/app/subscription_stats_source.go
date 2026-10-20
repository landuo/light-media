package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"light-media/internal/subscriptionstats"
)

// This is the current actual Lark→TMDB add call, not an arbitrary catalog DTO.
// Its Meta is the sealed add() parser state, and MediaInfo is the fresh source
// projection carried by the authenticated preceding notice.post statement.
func sourceLarkRegistration(payload sourceBusinessNotice) ([]byte, error) {
	if len(payload.Registration) == 0 {
		return nil, subscriptionstats.ErrDependency
	}
	return subscriptionstats.BuildRegistration(payload.Registration)
}

// Keep add() call locals separate from parsed MetaInfo fields: when input year
// is None but the title contains a year, meta.year may be parsed while the
// original statistics year must remain None.
func sourceRegistrationFields(name, year, kind, season any, mediaInfo json.RawMessage) (json.RawMessage, error) {
	value, e := decodeSourceJSON(mediaInfo)
	if e != nil {
		return nil, e
	}
	media := sourceMap(value)
	if media == nil {
		return nil, subscriptionstats.ErrDependency
	}
	fields := map[string]any{"name": name, "year": year, "type": kind, "season": season}
	for target, source := range map[string]string{"tmdbid": "tmdb_id", "imdbid": "imdb_id", "tvdbid": "tvdb_id", "doubanid": "douban_id", "bangumiid": "bangumi_id", "anilistid": "anilist_id", "poster": "poster", "backdrop": "backdrop", "vote": "vote_average", "description": "overview"} {
		v, ok := media[source]
		if !ok {
			return nil, subscriptionstats.ErrDependency
		}
		fields[target] = v
	}
	source, ok := media["source"].(string)
	if !ok || source != "themoviedb" {
		return nil, subscriptionstats.ErrDependency
	}
	id, ok := media["tmdb_id"]
	if !ok || id == nil {
		return nil, subscriptionstats.ErrDependency
	}
	fields["media_source"], fields["media_id"] = source, strings.TrimSpace(fmt.Sprint(id))
	raw, e := cleanupJournalJSON(fields)
	return raw, e
}
