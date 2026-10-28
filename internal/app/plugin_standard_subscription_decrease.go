package app

import "context"

// Fixed __resolve_total_episode_decrease limits a lower provider total by the
// highest confirmed episode in the old target scope. Reuse this host's existing
// library slots and source downloaded note; pending reservations are not facts.
func (a *App) resolveStandardSubscriptionTotalDecrease(ctx context.Context, s Subscription, candidate int) int {
	return a.resolveStandardSubscriptionTotalDecreaseResolved(ctx, s, candidate, nil)
}

func (a *App) resolveStandardSubscriptionTotalDecreaseResolved(ctx context.Context, s Subscription, candidate int, resolved *standardSubscriptionRecognition) int {
	old, start := s.TotalEpisodes, subscriptionStartEpisode(s)
	if candidate >= old || old == 0 || s.Media.MediaType != "tv" || s.BestVersion && s.BestVersionFull {
		return candidate
	}
	highest := 0
	for _, episode := range s.SourceNote {
		if episode >= start && episode <= old {
			highest = max(highest, episode)
		}
	}
	if !s.BestVersion {
		observed, err := a.resolveOriginalSubscriptionExistence(ctx, s, resolved)
		if err != nil {
			a.log("WARN", "subscription", "total_episode_decrease_facts_failed", "订阅已存在分集事实查询失败，按元数据总集数继续", "", "")
			return candidate
		}
		if existence := originalExistenceFromRecognition(observed); existence != nil {
			episodes, err := existence.episodes(s)
			if err != nil {
				return candidate
			}
			for _, episode := range episodes {
				if episode >= start && episode <= old {
					highest = max(highest, episode)
				}
			}
			return max(candidate, highest)
		}
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT DISTINCT episode FROM asset_slots WHERE media_key=? AND season=? AND episode>=? AND episode<=?`, mediaKey(s.Media), s.Season, start, old)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var episode int
			if err = rows.Scan(&episode); err != nil {
				break
			}
			highest = max(highest, episode)
		}
		if err == nil {
			err = rows.Err()
		}
	}
	if err != nil {
		// The pinned source logs a failed existence query and still follows the
		// candidate total; it does not permanently retain the obsolete target.
		a.log("WARN", "subscription", "total_episode_decrease_facts_failed", "订阅已存在分集事实查询失败，按元数据总集数继续", "", "")
		return candidate
	}
	return max(candidate, highest)
}
