package sites

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
)

var sourceUserDataIntegerFields = []string{"upload", "download", "seeding", "leeching", "seeding_size", "leeching_size", "message_unread"}

// Keep exact source integers privately. The old bounded native observation
// view receives no fabricated count: callers must check representability.
func sourceUserDataIntegerView(raw []byte) ([]byte, map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if !decodeJSON(raw, &fields) || fields == nil {
		return raw, nil, nil
	}
	wide := map[string]json.RawMessage{}
	for _, key := range sourceUserDataIntegerFields {
		token, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(token), []byte("null")) {
			continue
		}
		var value int64
		if json.Unmarshal(token, &value) == nil {
			continue
		}
		integer, ok := new(big.Int).SetString(string(token), 10)
		if !ok || integer.IsInt64() {
			return nil, nil, sourceModuleError(nil)
		}
		wide[key] = append(json.RawMessage(nil), token...)
		fields[key] = json.RawMessage(`null`)
	}
	if len(wide) == 0 {
		return raw, nil, nil
	}
	compatible, err := json.Marshal(fields)
	return compatible, wide, err
}
func (d SourceUserData) SourceIntegerRepresentable(key string) bool { return d.rawIntegers[key] == nil }
func (d SourceUserData) SourceIntegerFloat(key string) (*float64, error) {
	raw := d.rawIntegers[key]
	if raw == nil {
		return nil, errors.New("source integer is already represented")
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return nil, errors.New("source account float cannot represent integer")
	}
	return &value, nil
}

// ValidateSourceAccountBinding mirrors the fixed SQLite ORM binding boundary.
// Call only if SiteOper would insert/update, never when err_msg preserves an
// existing row. The latter can still return arbitrary source integers.
func (d SourceUserData) ValidateSourceAccountBinding() error {
	for _, key := range []string{"upload", "download", "seeding", "leeching", "seeding_size", "leeching_size"} {
		if !d.SourceIntegerRepresentable(key) {
			if _, err := d.SourceIntegerFloat(key); err != nil {
				return err
			}
		}
	}
	if !d.SourceIntegerRepresentable("message_unread") {
		return errors.New("source account SQLite integer overflow")
	}
	if number, ok := d.UserID.(json.Number); ok {
		if _, err := strconv.ParseInt(string(number), 10, 64); err != nil {
			return errors.New("source account SQLite integer binding overflow")
		}
	}
	return nil
}
func (d SourceUserData) SourceMessageUnreadTruthy() bool {
	if raw := d.rawIntegers["message_unread"]; raw != nil {
		return !bytes.Equal(raw, []byte("0"))
	}
	return d.MessageUnread != 0
}
func (d SourceUserData) SourceMessageUnreadText() string {
	if raw := d.rawIntegers["message_unread"]; raw != nil {
		return string(raw)
	}
	return strconv.FormatInt(d.MessageUnread, 10)
}
