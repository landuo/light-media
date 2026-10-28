package app

import (
	"context"
	"encoding/json"

	"light-media/internal/decision"
	"light-media/internal/priorityrules"
	"light-media/internal/rsscontext"
)

func originalPrioritySourceMedia(raw json.RawMessage) (*priorityrules.Media, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, originalResourceProjectionError()
	}
	fields := sourceMap(value)
	var category *string
	if value, ok := fields["category"].(string); ok {
		category = &value
	}
	return &priorityrules.Media{Type: sourceText(fields["type"]), Category: category, Attributes: fields}, nil
}

func originalFilterSourceMediaJSON(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, originalResourceProjectionError()
	}
	model, err := rsscontext.NewMediaInfo(nil)
	if err != nil {
		return nil, err
	}
	if err = model.FromDict(sourceMap(value)); err != nil {
		return nil, err
	}
	fields, err := model.ToDict()
	if err != nil {
		return nil, err
	}
	// Ordinary module arguments are the live source model, not a public
	// Context.to_dict response. Recompute descriptors above while retaining
	// its actual provider payloads, which that public serializer hides.
	for _, key := range []string{"tmdb_info", "douban_info", "bangumi_info", "anilist_info"} {
		fields[key] = sourceMap(value)[key]
	}
	// These are ordinary live attributes. Public to_dict infers IDs for its
	// response, which must not turn a callback's None into a different value.
	for _, key := range []string{"source", "media_id"} {
		fields[key] = sourceMap(value)[key]
	}
	if prefix, present := sourceMap(value)["mediaid_prefix"]; present {
		fields["mediaid_prefix"] = prefix
	} else {
		delete(fields, "mediaid_prefix")
	}
	return json.Marshal(fields)
}

func originalClearMediaSearchInformation(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, originalResourceProjectionError()
	}
	model, err := rsscontext.NewMediaInfo(nil)
	if err != nil {
		return nil, err
	}
	if err = model.FromDict(sourceMap(value)); err != nil {
		return nil, err
	}
	model.Clear()
	encoded, err := json.Marshal(model.Fields)
	if err != nil {
		return nil, err
	}
	return originalFilterSourceMediaJSON(encoded)
}

// Fixed __parse_result groups by (site,site_name). A single group retains its
// returned list and aliases; multiple groups select original objects by actual
// identity in original order. Equal model copies never satisfy that membership.
func (a *App) originalMediaFilterResources(ctx context.Context, filter priorityFilter, out SearchOutput) (SearchOutput, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return out, err
	}
	if !originalModuleGroupsHaveMethod(groups, "filter_torrents") {
		return filter.applyNative(ctx, a, out)
	}
	rows := []json.RawMessage{}
	for _, candidate := range out.Candidates {
		raw, err := a.originalTitleFilterResourceModel(ctx, candidate)
		if err != nil {
			return out, err
		}
		rows = append(rows, raw)
	}
	input, err := newOriginalTitleFilterState(filter.names, rows)
	if err != nil {
		return out, err
	}
	media := &filter.sourceMedia.MediaInfo
	input.media = media
	bySite, siteOrder := map[string][]*originalTitleFilterModel{}, []string{}
	for i, model := range input.input.rows {
		candidate := out.Candidates[i]
		model.candidate = &candidate
		decoded, err := decodeSourceJSON(model.value)
		if err != nil {
			return out, err
		}
		name := sourceMap(decoded)["site_name"]
		if _, ok := name.(string); name != nil && !ok {
			return out, originalResourceProjectionError()
		}
		key, _ := json.Marshal([]any{candidate.Resource.SiteID, name})
		if _, present := bySite[string(key)]; !present {
			siteOrder = append(siteOrder, string(key))
		}
		bySite[string(key)] = append(bySite[string(key)], model)
	}
	result := input.input
	if len(siteOrder) <= 1 {
		if err = a.runOriginalTorrentFilterState(ctx, filter, out, groups, input); err != nil {
			return out, err
		}
		result = input.result.list
		if result == nil && !input.empty() && originalModuleResultTruthy(input.result.scalar) {
			return out, originalResourceProjectionError()
		}
	} else {
		retained := map[*originalTitleFilterModel]bool{}
		for index, key := range siteOrder {
			// All groups share the same MediaInfo object. The existing serial
			// process runner commits each valid mutation before the next call;
			// no detached model snapshots can overwrite another site's changes.
			state := &originalTitleFilterState{input: &originalTitleFilterList{rows: bySite[key]}, groups: append([]string{}, filter.names...), result: originalTitleFilterValue{scalar: json.RawMessage(`null`)}, next: input.next, media: media}
			var currentGroups []originalPluginModuleGroup
			if index == 0 {
				currentGroups = groups // reuse the first site's discovery frame
			}
			if err = a.runOriginalTorrentFilterState(ctx, filter, out, currentGroups, state); err != nil {
				return out, err
			}
			if state.result.list == nil {
				if !state.empty() && originalModuleResultTruthy(state.result.scalar) {
					return out, originalResourceProjectionError()
				}
				continue
			}
			for _, model := range state.result.list.rows {
				retained[model] = true
			}
		}
		result = &originalTitleFilterList{}
		for _, model := range input.input.rows {
			if retained[model] {
				result.rows = append(result.rows, model)
			}
		}
	}
	*media, err = originalFilterSourceMediaJSON(*media)
	if err != nil {
		return out, err
	}
	information, err := a.originalRecognitionProjection(filter.sourceMedia.Media, *media)
	if err != nil {
		return out, err
	}
	*filter.sourceMedia = information
	if filter.target != nil {
		filter.target.Media = information.Media
	}
	out.Candidates = []Candidate{}
	if result == nil {
		return out, nil
	}
	out.Candidates, err = a.originalTitleFilterCandidates(ctx, filter, result)
	if err != nil || filter.target == nil {
		return out, err
	}
	for i := range out.Candidates {
		row := &out.Candidates[i]
		choice := a.evaluateResourceContext(ctx, row.Resource, *filter.target, decision.NativePolicy(decisionPolicy(filter.policy)))
		row.Accepted, row.NeedsConfirmation, row.Reasons, row.Season, row.Episodes = choice.Accepted, choice.NeedsConfirmation, choice.Reasons, choice.Season, choice.Episodes
	}
	return out, nil
}
