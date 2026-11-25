package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
)

const titleTorrentFilterContract = "light-media.title-torrent-filter/v1"

// TitleFilterTorrent is one ordinary TorrentInfo object. Pointer identity is
// explicit: reusing a pointer shares that model, Clone creates an equal copy.
// No reflection, arbitrary object graph or cross-operation object store exists.
type TitleFilterTorrent struct{ Fields map[string]json.RawMessage }

func (m *TitleFilterTorrent) Clone() *TitleFilterTorrent {
	if m == nil {
		return nil
	}
	out := &TitleFilterTorrent{Fields: map[string]json.RawMessage{}}
	for key, value := range m.Fields {
		out.Fields[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

// Assigning these lists represents in-place modification of the source input
// lists. MediaInfo is nil for title search; a media caller shares its concrete
// input model. Mutate Fields on that pointer; assigning a different pointer
// does not replace the caller's model, matching a Python local reassignment.
type TitleTorrentFilterInput struct {
	RuleGroups  []string
	TorrentList []*TitleFilterTorrent
	MediaInfo   *ModuleMediaInfo
}

type TitleTorrentFilterReturn struct {
	// InputList means the actual input list, not an equal/copied list. Rows
	// instead means a separate list; its model pointers may still share inputs.
	InputList bool
	Rows      []*TitleFilterTorrent
	Value     json.RawMessage
}

type TitleFilterModelSnapshot struct {
	Ref   int             `json:"ref"`
	Value json.RawMessage `json:"value"`
}

type TitleTorrentFilterSnapshot struct {
	Contract    string                     `json:"title_filter_contract"`
	Models      []TitleFilterModelSnapshot `json:"models"`
	InputRows   []int                      `json:"input_rows"`
	RuleGroups  []string                   `json:"rule_groups"`
	ResultKind  string                     `json:"result_kind"`
	ResultRows  []int                      `json:"result_rows"`
	Value       json.RawMessage            `json:"value"`
	Failed      bool                       `json:"handler_failed"`
	RateLimited bool                       `json:"rate_limited"`
	MediaInfo   json.RawMessage            `json:"mediainfo,omitempty"`
}

// HandleTitleTorrentFilter uses the same commit-before-failure boundary as
// HandleMutableEvent, with a bounded source-specific DTO: TorrentInfo models,
// their input list and rule names. Explicit input references distinguish aliases
// from equal copies. Raw source kwargs remain unmodified in the request.
func HandleTitleTorrentFilter(raw json.RawMessage, handle func(*TitleTorrentFilterInput) (TitleTorrentFilterReturn, error)) (json.RawMessage, error) {
	var wire struct {
		Args   []json.RawMessage `json:"args"`
		Kwargs struct {
			RuleGroups  []string          `json:"rule_groups"`
			TorrentList []json.RawMessage `json:"torrent_list"`
			MediaInfo   json.RawMessage   `json:"mediainfo"`
		} `json:"kwargs"`
		Refs []int `json:"title_torrent_refs"`
	}
	if json.Unmarshal(raw, &wire) != nil || handle == nil || wire.Args == nil || len(wire.Args) != 0 || wire.Refs == nil || len(wire.Refs) != len(wire.Kwargs.TorrentList) {
		return nil, errors.New("invalid title filter input")
	}
	call := &TitleTorrentFilterInput{RuleGroups: wire.Kwargs.RuleGroups, TorrentList: []*TitleFilterTorrent{}}
	var inputMedia *ModuleMediaInfo
	if !bytes.Equal(bytes.TrimSpace(wire.Kwargs.MediaInfo), []byte("null")) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(wire.Kwargs.MediaInfo, &fields) != nil || fields == nil {
			return nil, errors.New("invalid torrent filter MediaInfo")
		}
		inputMedia = &ModuleMediaInfo{Fields: fields}
		call.MediaInfo = inputMedia
	}
	byRef := map[int]*TitleFilterTorrent{}
	refs := map[*TitleFilterTorrent]int{}
	objects := []*TitleFilterTorrent{}
	for i, ref := range wire.Refs {
		if ref < 0 {
			return nil, errors.New("invalid title filter input reference")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(wire.Kwargs.TorrentList[i], &fields) != nil || fields == nil {
			return nil, errors.New("invalid title filter input model")
		}
		model := byRef[ref]
		if model == nil {
			model = &TitleFilterTorrent{Fields: fields}
			byRef[ref], refs[model] = model, ref
			objects = append(objects, model)
		} else {
			previous, _ := json.Marshal(model.Fields)
			current, _ := json.Marshal(fields)
			if !bytes.Equal(previous, current) {
				return nil, errors.New("inconsistent title filter input reference")
			}
		}
		call.TorrentList = append(call.TorrentList, model)
	}
	var returned TitleTorrentFilterReturn
	var callbackErr error
	failed := false
	func() {
		defer func() {
			if recover() != nil {
				failed = true
			}
		}()
		returned, callbackErr = handle(call)
		failed = callbackErr != nil
	}()
	if failed {
		returned = TitleTorrentFilterReturn{} // source ignores a failed callback's return
	}
	if call.RuleGroups == nil {
		call.RuleGroups = []string{}
	}
	snapshot := TitleTorrentFilterSnapshot{Contract: titleTorrentFilterContract, Models: []TitleFilterModelSnapshot{}, InputRows: []int{}, RuleGroups: call.RuleGroups, ResultKind: "value", ResultRows: []int{}, Value: json.RawMessage(`null`), Failed: failed, RateLimited: errors.Is(callbackErr, ErrModuleRateLimited)}
	if inputMedia != nil {
		if inputMedia.Fields == nil {
			return nil, errors.New("invalid torrent filter MediaInfo mutation")
		}
		var err error
		snapshot.MediaInfo, err = json.Marshal(inputMedia.Fields)
		if err != nil {
			return nil, err
		}
	}
	next := -1
	add := func(model *TitleFilterTorrent) (int, error) {
		if model == nil || model.Fields == nil {
			return 0, errors.New("invalid title filter output model")
		}
		if ref, exists := refs[model]; exists {
			return ref, nil
		}
		refs[model] = next
		objects = append(objects, model)
		next--
		return refs[model], nil
	}
	for _, model := range call.TorrentList {
		ref, err := add(model)
		if err != nil {
			return nil, err
		}
		snapshot.InputRows = append(snapshot.InputRows, ref)
	}
	if returned.InputList {
		if returned.Rows != nil || len(returned.Value) != 0 {
			return nil, errors.New("ambiguous title filter return")
		}
		snapshot.ResultKind = "input_list"
	} else if returned.Rows != nil {
		if len(returned.Value) != 0 {
			return nil, errors.New("ambiguous title filter return")
		}
		snapshot.ResultKind = "list"
		for _, model := range returned.Rows {
			ref, err := add(model)
			if err != nil {
				return nil, err
			}
			snapshot.ResultRows = append(snapshot.ResultRows, ref)
		}
	} else if len(returned.Value) != 0 {
		if !json.Valid(returned.Value) {
			return nil, errors.New("invalid title filter return")
		}
		snapshot.Value = append(json.RawMessage(nil), returned.Value...)
	}
	for _, model := range objects {
		value, err := json.Marshal(model.Fields)
		if err != nil {
			return nil, err
		}
		snapshot.Models = append(snapshot.Models, TitleFilterModelSnapshot{Ref: refs[model], Value: value})
	}
	return json.Marshal(snapshot)
}

// DecodeTitleTorrentFilterSnapshot recognizes only the explicit helper format.
// Scope/identity checks against the actual input references belong to the host.
func DecodeTitleTorrentFilterSnapshot(raw json.RawMessage) (snapshot TitleTorrentFilterSnapshot, present bool, err error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || fields["title_filter_contract"] == nil {
		return snapshot, false, nil
	}
	present = true
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || snapshot.Contract != titleTorrentFilterContract || snapshot.Models == nil || snapshot.InputRows == nil || snapshot.RuleGroups == nil || snapshot.ResultRows == nil || !json.Valid(snapshot.Value) {
		return snapshot, true, errors.New("invalid title filter snapshot")
	}
	for _, name := range []string{"handler_failed", "rate_limited"} {
		flag := bytes.TrimSpace(fields[name])
		if !bytes.Equal(flag, []byte("true")) && !bytes.Equal(flag, []byte("false")) {
			return snapshot, true, errors.New("invalid title filter failure flag")
		}
	}
	if snapshot.ResultKind != "value" && snapshot.ResultKind != "list" && snapshot.ResultKind != "input_list" || snapshot.RateLimited && !snapshot.Failed {
		return snapshot, true, errors.New("invalid title filter result kind")
	}
	return snapshot, true, nil
}
