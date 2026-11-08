package sites

import (
	"encoding/json"
	"errors"
)

// MarshalNativeSourceUserData returns the original current parser DTO. The
// native parser never sets the two storage-only fields; Go's string zero values
// represent the source model's nullable constructor defaults here. Callers must
// not use this normalization for worker-supplied DTOs with explicit empty text.
func MarshalNativeSourceUserData(data *SourceUserData) (json.RawMessage, error) {
	raw, err := json.Marshal(data)
	if err != nil || data == nil {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if data.Domain == "" {
		fields["domain"] = json.RawMessage(`null`)
	}
	if data.Day == "" {
		fields["updated_day"] = json.RawMessage(`null`)
	}
	return json.Marshal(fields)
}

// SourceAccountInsertClone applies only the fixed SQLAlchemy INSERT defaults.
// Explicit None becomes zero for these scalar columns on INSERT; UPDATE keeps
// SQL NULL. JSON columns evaluate None themselves and remain JSON null. The
// caller's current DTO and native observation are never modified.
func (data *SourceUserData) SourceAccountInsertClone() (*SourceUserData, error) {
	if data == nil {
		return nil, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"bonus", "upload", "download", "ratio", "seeding", "leeching", "seeding_size", "leeching_size", "message_unread"} {
		if string(fields[key]) == "null" {
			fields[key] = json.RawMessage(`0`)
		}
	}
	raw, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var out SourceUserData
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SourceUserRequestRejected recognizes only the existing native request-guard
// marker. Vendor errors with matching text cannot claim local authority facts.
func SourceUserRequestRejected(err error) bool {
	var rejected *sourceUserRevokedError
	return errors.As(err, &rejected)
}
