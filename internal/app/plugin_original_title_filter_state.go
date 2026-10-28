package app

import (
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	"light-media/internal/rsscontext"
)

// Only the title caller's TorrentInfo objects and their list have identities in
// this per-call state. This is not a Python object store or a generic graph.
type originalTitleFilterModel struct {
	ref       int
	value     json.RawMessage
	candidate *Candidate
}

type originalTitleFilterList struct{ rows []*originalTitleFilterModel }
type originalTitleFilterValue struct {
	list   *originalTitleFilterList
	scalar json.RawMessage
}
type originalTitleFilterState struct {
	input  *originalTitleFilterList
	groups []string
	result originalTitleFilterValue
	next   int
	media  *json.RawMessage // Same source MediaInfo shared by the media caller's site filters.
}

func newOriginalTitleFilterState(groups []string, rows []json.RawMessage) (*originalTitleFilterState, error) {
	state := &originalTitleFilterState{input: &originalTitleFilterList{}, groups: append([]string{}, groups...), result: originalTitleFilterValue{scalar: json.RawMessage(`null`)}}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			return nil, originalResourceProjectionError()
		}
		model, err := state.newModel(row)
		if err != nil {
			return nil, err
		}
		state.input.rows = append(state.input.rows, model)
	}
	return state, nil
}

func (state *originalTitleFilterState) newModel(raw json.RawMessage) (*originalTitleFilterModel, error) {
	if !json.Valid(raw) {
		return nil, originalResourceProjectionError()
	}
	model := &originalTitleFilterModel{ref: state.next, value: append(json.RawMessage(nil), raw...)}
	state.next++
	return model, nil
}

func (model *originalTitleFilterModel) sourceJSON() (json.RawMessage, error) {
	value, err := decodeSourceJSON(model.value)
	if err != nil {
		return nil, err
	}
	if sourceMap(value) == nil {
		// Ordinary list returns can contain an invalid consumer value. Keep it
		// until after real core execution instead of swallowing it as a handler
		// failure and silently returning only the native rows.
		return append(json.RawMessage(nil), model.value...), nil
	}
	// Existing source model code recomputes its two properties. An assignment
	// to a serialized property cannot replace the source descriptor's getter.
	torrent, err := rsscontext.NewTorrentInfo(nil)
	if err != nil {
		return nil, err
	}
	torrent.FromDict(sourceMap(value))
	fields, err := torrent.ToDict()
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func (rows *originalTitleFilterList) sourceJSON() (json.RawMessage, error) {
	values := []json.RawMessage{}
	snapshots := map[*originalTitleFilterModel]json.RawMessage{}
	for _, row := range rows.rows {
		value, exists := snapshots[row]
		if !exists {
			var err error
			value, err = row.sourceJSON()
			if err != nil {
				return nil, err
			}
			snapshots[row] = value
		}
		values = append(values, value)
	}
	return json.Marshal(values)
}

func (state *originalTitleFilterState) resultJSON() (json.RawMessage, error) {
	if state.result.list != nil {
		return state.result.list.sourceJSON()
	}
	return append(json.RawMessage(nil), state.result.scalar...), nil
}

func (state *originalTitleFilterState) empty() bool {
	return state.result.list == nil && originalModuleResultEmpty(state.result.scalar)
}

func (state *originalTitleFilterState) callJSON() (json.RawMessage, error) {
	rows, err := state.input.sourceJSON()
	if err != nil {
		return nil, err
	}
	refs := []int{}
	for _, model := range state.input.rows {
		refs = append(refs, model.ref)
	}
	var media json.RawMessage = json.RawMessage(`null`)
	if state.media != nil {
		media, err = originalFilterSourceMediaJSON(*state.media)
		if err != nil {
			return nil, err
		}
		*state.media = media
	}
	return json.Marshal(map[string]any{"args": []any{}, "kwargs": map[string]any{"rule_groups": state.groups, "torrent_list": rows, "mediainfo": media}, "title_torrent_refs": refs})
}

func (state *originalTitleFilterState) decodeValue(raw json.RawMessage) (originalTitleFilterValue, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil && values != nil {
		list := &originalTitleFilterList{}
		for _, value := range values {
			model, err := state.newModel(value)
			if err != nil {
				return originalTitleFilterValue{}, err
			}
			list.rows = append(list.rows, model)
		}
		return originalTitleFilterValue{list: list}, nil
	}
	return originalTitleFilterValue{scalar: append(json.RawMessage(nil), raw...)}, nil
}

// A malformed snapshot changes nothing. Valid in-place modifications commit
// before the ordinary error path, including rate limits and callback failures.
func (state *originalTitleFilterState) decodeReturn(raw json.RawMessage) (originalTitleFilterValue, error) {
	snapshot, present, err := plugins.DecodeTitleTorrentFilterSnapshot(raw)
	if err != nil || !present {
		if err != nil {
			return originalTitleFilterValue{}, err
		}
		return state.decodeValue(raw)
	}
	known := map[int]*originalTitleFilterModel{}
	for _, row := range state.input.rows {
		known[row.ref] = row
	}
	resolved := map[int]*originalTitleFilterModel{}
	updates := map[*originalTitleFilterModel]json.RawMessage{}
	newModels := []*originalTitleFilterModel{}
	for _, row := range snapshot.Models {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row.Value, &fields) != nil || fields == nil || resolved[row.Ref] != nil {
			return originalTitleFilterValue{}, errors.New("invalid title filter model snapshot")
		}
		var model *originalTitleFilterModel
		if row.Ref >= 0 {
			model = known[row.Ref]
			if model == nil {
				return originalTitleFilterValue{}, errors.New("unknown title filter input reference")
			}
		} else {
			model = &originalTitleFilterModel{ref: state.next + len(newModels)}
			newModels = append(newModels, model)
		}
		resolved[row.Ref], updates[model] = model, append(json.RawMessage(nil), row.Value...)
	}
	for ref := range known {
		if resolved[ref] == nil {
			return originalTitleFilterValue{}, errors.New("missing title filter model snapshot")
		}
	}
	resolve := func(refs []int) ([]*originalTitleFilterModel, error) {
		models := []*originalTitleFilterModel{}
		for _, ref := range refs {
			model := resolved[ref]
			if model == nil {
				return nil, errors.New("unknown title filter output reference")
			}
			models = append(models, model)
		}
		return models, nil
	}
	inputs, err := resolve(snapshot.InputRows)
	if err != nil {
		return originalTitleFilterValue{}, err
	}
	returned, err := resolve(snapshot.ResultRows)
	if err != nil {
		return originalTitleFilterValue{}, err
	}
	if snapshot.ResultKind != "list" && len(returned) != 0 || snapshot.ResultKind != "value" && string(snapshot.Value) != "null" {
		return originalTitleFilterValue{}, errors.New("ambiguous title filter snapshot")
	}
	if state.media != nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal(snapshot.MediaInfo, &fields) != nil || fields == nil {
			return originalTitleFilterValue{}, errors.New("missing torrent filter MediaInfo snapshot")
		}
	} else if len(snapshot.MediaInfo) != 0 && string(snapshot.MediaInfo) != "null" {
		return originalTitleFilterValue{}, errors.New("unexpected title filter MediaInfo mutation")
	}
	for model, value := range updates {
		model.value = value
	}
	state.next += len(newModels)
	state.input.rows, state.groups = inputs, append([]string{}, snapshot.RuleGroups...)
	if state.media != nil {
		*state.media = append(json.RawMessage(nil), snapshot.MediaInfo...)
	}
	if snapshot.Failed {
		if snapshot.RateLimited {
			return originalTitleFilterValue{}, plugins.ErrModuleRateLimited
		}
		return originalTitleFilterValue{}, errors.New("title filter handler failed")
	}
	switch snapshot.ResultKind {
	case "input_list":
		return originalTitleFilterValue{list: state.input}, nil
	case "list":
		return originalTitleFilterValue{list: &originalTitleFilterList{rows: returned}}, nil
	default:
		return state.decodeValue(snapshot.Value)
	}
}

func (state *originalTitleFilterState) merge(value originalTitleFilterValue) {
	if state.empty() {
		state.result = value
	} else if state.result.list != nil && value.list != nil {
		state.result.list.rows = append(state.result.list.rows, value.list.rows...)
	}
}
