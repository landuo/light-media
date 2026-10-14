package media

import "fmt"

type Episode struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

type Source struct {
	Site  string `json:"site,omitempty"`
	Group string `json:"group,omitempty"`
	Form  string `json:"form,omitempty"`
}

type MappingRule struct {
	ID           string          `json:"id"`
	Revision     int             `json:"revision"`
	MediaID      string          `json:"media_id"`
	TargetSeason int             `json:"target_season"`
	From         int             `json:"from"`
	To           int             `json:"to"`
	Source       Source          `json:"source,omitempty"`
	Offset       *int            `json:"offset,omitempty"`
	Pairs        map[int]Episode `json:"pairs,omitempty"`
	Confirmed    bool            `json:"confirmed"`
}

type MappingResult struct {
	Status    Status    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	Original  Numbering `json:"original"`
	Standard  []Episode `json:"standard,omitempty"`
	MappingID string    `json:"mapping_id,omitempty"`
	Revision  int       `json:"revision,omitempty"`
}

// Resolve always consumes original numbering. The resulting standard numbers
// are stored separately; calling it with standard numbers is not supported.
// Known targets are authoritative metadata, not proof of broadcast/availability.
func Resolve(mediaID string, original Numbering, source Source, rules []MappingRule, known []Episode, correction []Episode) MappingResult {
	r := MappingResult{Status: NeedsReview, Original: original}
	fail := func(reason string) MappingResult { r.Reason = reason; return r }
	if mediaID == "" {
		return fail("unconfirmed_media_identity")
	}
	if len(original.Episodes) == 0 {
		return fail("missing_original_numbering")
	}
	knownSet := map[Episode]bool{}
	for _, ep := range known {
		if ep.Season > 0 && ep.Episode > 0 {
			knownSet[ep] = true
		}
	}
	finish := func(target []Episode) MappingResult {
		seen := map[Episode]bool{}
		for _, ep := range target {
			if !knownSet[ep] {
				return fail("unknown_target_episode")
			}
			if seen[ep] {
				return fail("target_episode_conflict")
			}
			seen[ep] = true
		}
		r.Status, r.Standard = Parsed, target
		return r
	}
	if len(correction) > 0 {
		if len(correction) != len(original.Episodes) {
			return fail("correction_count_mismatch")
		}
		r.MappingID = "explicit_file_correction"
		return finish(correction)
	}
	var eligible []MappingRule
	hasApplicableScope, hasPartialScope := false, false
	numberMatches := map[int]int{}
	for _, rule := range rules {
		if rule.MediaID != mediaID || !rule.Confirmed || !sourceMatches(rule.Source, source, original.Form) {
			continue
		}
		if err := rule.Validate(); err != nil {
			return fail("invalid_mapping_rule")
		}
		hasApplicableScope = true
		inside := 0
		for _, n := range original.Episodes {
			if n >= rule.From && n <= rule.To {
				inside++
				numberMatches[n]++
				if numberMatches[n] > 1 {
					return fail("multiple_matching_rules")
				}
			}
		}
		if inside == len(original.Episodes) {
			eligible = append(eligible, rule)
		} else if inside > 0 {
			hasPartialScope = true
		}
	}
	if len(eligible) > 1 {
		return fail("multiple_matching_rules")
	}
	if len(eligible) == 1 {
		rule := eligible[0]
		target := make([]Episode, 0, len(original.Episodes))
		for _, n := range original.Episodes {
			var ep Episode
			if rule.Offset != nil {
				ep = Episode{rule.TargetSeason, n + *rule.Offset}
			} else {
				var ok bool
				ep, ok = rule.Pairs[n]
				if !ok {
					return fail("unmapped_original_episode")
				}
			}
			target = append(target, ep)
		}
		r.MappingID, r.Revision = rule.ID, rule.Revision
		return finish(target)
	}
	if hasPartialScope {
		return fail("range_crosses_mapping_scope")
	}
	if hasApplicableScope {
		return fail("outside_mapping_range")
	}
	if original.Season == nil || *original.Season <= 0 {
		return fail("mapping_required_for_absolute_numbering")
	}
	target := make([]Episode, 0, len(original.Episodes))
	for _, n := range original.Episodes {
		target = append(target, Episode{*original.Season, n})
	}
	return finish(target)
}

func sourceMatches(rule, source Source, form string) bool {
	return (rule.Site == "" || rule.Site == source.Site) && (rule.Group == "" || rule.Group == source.Group) && (rule.Form == "" || rule.Form == form)
}

func (r MappingRule) Validate() error {
	if r.ID == "" || r.Revision <= 0 || r.MediaID == "" || r.TargetSeason <= 0 || r.From <= 0 || r.To < r.From {
		return fmt.Errorf("invalid rule identity or scope")
	}
	if (r.Offset == nil) == (len(r.Pairs) == 0) {
		return fmt.Errorf("exactly one mapping strategy is required")
	}
	if r.Offset != nil {
		if r.From+*r.Offset <= 0 || r.To+*r.Offset <= 0 {
			return fmt.Errorf("offset yields invalid episode")
		}
	}
	seen := map[Episode]bool{}
	for n, ep := range r.Pairs {
		if n < r.From || n > r.To || ep.Season != r.TargetSeason || ep.Episode <= 0 || seen[ep] {
			return fmt.Errorf("invalid or conflicting pair")
		}
		seen[ep] = true
	}
	return nil
}
