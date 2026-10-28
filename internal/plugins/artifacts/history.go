package artifacts

import (
	"encoding/json"
	"maps"
)

// The pinned frontend iterates the author's history object directly. A Go map
// alone sorts its keys during JSON encoding, silently reversing many histories.
// Keep the original object only while it still describes the validated map.
func AuthorHistoryJSON(history map[string]string, original json.RawMessage) json.RawMessage {
	var decoded map[string]string
	if len(original) != 0 && json.Unmarshal(original, &decoded) == nil && maps.Equal(decoded, history) {
		return original
	}
	return nil
}

func (i *Index) UnmarshalJSON(raw []byte) error {
	type plain Index
	var decoded plain
	if err := decodeStrictIndex(raw, &decoded); err != nil {
		return err
	}
	var fields struct{ History json.RawMessage }
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*i = Index(decoded)
	i.HistoryJSON = append(json.RawMessage(nil), fields.History...)
	return nil
}

func (i Index) MarshalJSON() ([]byte, error) {
	type plain Index
	if history := AuthorHistoryJSON(i.History, i.HistoryJSON); history != nil {
		return json.Marshal(struct {
			plain
			History json.RawMessage `json:"history"`
		}{plain(i), history})
	}
	return json.Marshal(plain(i))
}
