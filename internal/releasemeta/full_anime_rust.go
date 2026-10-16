package releasemeta

import (
	"strings"

	"light-media/internal/releasemeta/rustanime"
)

func fullRustAnimeCandidate(title, prepared string, parsed, original rustanime.Result, matched *string) (*string, error) {
	name := rustAnimeFirst(parsed, rustanime.AnimeTitle)
	originName, originGroup := rustAnimeFirst(original, rustanime.AnimeTitle), rustAnimeFirst(original, rustanime.ReleaseGroup)
	if rustAnimeShouldReplace(name, originGroup, matched) && originName != nil && !rustAnimeShouldRetry(originName) && (originGroup == nil || !asciiEqualFold(*originName, *originGroup)) && (matched == nil || !asciiEqualFold(*originName, *matched)) {
		name = originName
	}
	if rustAnimeShouldReplace(name, originGroup, matched) {
		if candidate := rustAnimePreparedGroupTitle(prepared, matched); candidate != nil {
			name = candidate
		}
	}
	if rustAnimeShouldReplace(name, originGroup, matched) {
		if candidate := rustAnimePreferredBracket(title, originGroup, matched); candidate != nil {
			name = candidate
		}
	}
	if rustAnimeShouldReplace(name, originGroup, matched) {
		retry, _, e := rustAnimeParse("[ANIME]" + prepared)
		if e != nil {
			return nil, e
		}
		name = rustAnimeFirst(retry, rustanime.AnimeTitle)
	}
	if rustAnimeShouldReplace(name, originGroup, matched) {
		name = rustAnimeFirstBracket(prepared)
	}
	return name, nil
}
func parseFullRustAnime(title string, subtitle *string, isfile bool, options Options) (*MetaObject, error) {
	mode := "rust"
	m := fullBase(title, subtitle, isfile, "rust")
	m.Kind = "anime"
	if title == "" {
		return m, nil
	}
	prepared := prepareAnimeTitle(title, false)
	parsed, valid, e := rustAnimeParse(prepared)
	if e != nil {
		return nil, e
	}
	original, _, e := rustAnimeParse(title)
	if e != nil {
		return nil, e
	}
	matched, e := matchTeamOption(title, options)
	if e != nil {
		return nil, e
	}
	if valid {
		candidate, e := fullRustAnimeCandidate(title, prepared, parsed, original, matched)
		if e != nil {
			return nil, e
		}
		if candidate != nil {
			cn, en := fullRustAnimeNames(*candidate, title, prepared)
			if cn != "" {
				m.Set("cn_name", cn)
			}
			if en != "" {
				m.Set("en_name", en)
			}
		}
		if year := rustAnimeFirst(parsed, rustanime.AnimeYear); year != nil && digits(*year) {
			m.Set("year", *year)
		}
		if e := fullAnimeSetRange(m, "season", rustAnimeValues(parsed, rustanime.AnimeSeason), "rust"); e != nil {
			return nil, e
		}
		if e := fullAnimeSetRange(m, "episode", rustAnimeValues(parsed, rustanime.EpisodeNumber), "rust"); e != nil {
			return nil, e
		}
		if match := fullRXFirst(`TV\s+(\d{1,4})`, title, true, mode); match != nil {
			if n, e := fullInt(match.GroupByNumber(1).String(), "rust"); e == nil {
				m.Set("begin_episode", n)
				m.Set("end_episode", nil)
				m.Set("total_episode", 1)
				m.Set("type", "电视剧")
			}
		}
		if match := fullRXFirst(`TV\s+(\d{1,4})\s*-\s*(\d{1,4})`, title, true, mode); match != nil {
			begin, e1 := fullInt(match.GroupByNumber(1).String(), "rust")
			end, e2 := fullInt(match.GroupByNumber(2).String(), "rust")
			if e1 == nil && e2 == nil && end.Cmp(begin) >= 0 {
				m.Set("begin_episode", begin)
				m.Set("end_episode", end)
				m.Set("total_episode", int(end.Int64()-begin.Int64()+1))
				m.Set("type", "电视剧")
			}
		}
		if m.Text("type") == "未知" {
			if typ := rustAnimeFirst(parsed, rustanime.AnimeType); typ != nil && asciiEqualFold(*typ, "TV") {
				m.Set("type", "电视剧")
			} else if source := rustAnimeFirst(parsed, rustanime.Source); source == nil || !asciiEqualFold(*source, "WEBRip") {
				m.Set("type", "电影")
			}
		}
		if resolution := rustAnimeFirst(parsed, rustanime.VideoResolution); resolution != nil {
			pix := normalizeAnimeResolution(*resolution)
			if pix != "" {
				m.Set("resource_pix", pix)
			}
		}
		if matched != nil {
			m.Set("resource_team", *matched)
		} else if group := rustAnimeFirst(original, rustanime.ReleaseGroup); group != nil {
			m.Set("resource_team", *group)
		}
		custom, e := matchCustomization(title, options)
		if e != nil {
			return nil, e
		}
		if custom != "" {
			m.Set("customization", custom)
		}
		if video := rustAnimeFirst(parsed, rustanime.VideoTerm); video != nil {
			if !(asciiEqualFold(*video, "HEVC") && strings.Contains(title, "HEVC-10bit") || asciiEqualFold(*video, "hevc") && strings.Contains(title, "yuv420p10")) {
				m.Set("video_encode", *video)
			}
		}
		bit := fullVideoBit(title)
		if bit == nil {
			bit = fullVideoBit(m.Text("video_encode"))
		}
		if bit != nil {
			m.Set("video_bit", bit)
		}
		if audio := rustAnimeFirst(parsed, rustanime.AudioTerm); audio != nil {
			value := *audio
			if i := strings.Index(value, "_"); i >= 0 {
				value = value[:i]
			}
			m.Set("audio_encode", value)
		}
		if m.Get("audio_encode") == nil {
			if match := fullRXFirst(`(?:^|[^0-9])([567][.]1)(?:[^0-9]|$)`, title, false, mode); match != nil {
				m.Set("audio_encode", match.GroupByNumber(1).String())
			}
		}
		if e := fullAnimeFPS(m, title, "rust"); e != nil {
			return nil, e
		}
		if e := fullSubtitle(m, m.Text("org_string"), "rust"); e != nil {
			return nil, e
		}
		if !fullBool(m, "_subtitle_flag") && subtitle != nil {
			if e := fullSubtitle(m, *subtitle, "rust"); e != nil {
				return nil, e
			}
		}
	}
	if m.Text("type") == "未知" && m.Name() == "" {
		m.Set("type", "电视剧")
	}
	return m, nil
}
