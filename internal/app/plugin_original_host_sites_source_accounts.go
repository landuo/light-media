package app

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

var sourceSiteAccountColumns = []string{"id", "domain", "name", "username", "userid", "user_level", "join_at", "bonus", "upload", "download", "ratio", "seeding", "leeching", "seeding_size", "leeching_size", "seeding_info", "message_unread", "message_unread_contents", "err_msg", "updated_day", "updated_time"}

func originalSourceAccountFields(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if token := bytes.TrimSpace(fields["userid"]); len(token) > 0 && token[0] != '"' && !bytes.Equal(token, []byte("null")) {
		// SQLite's TEXT affinity converts a successfully bound integer ID.
		fields["userid"], _ = json.Marshal(string(token))
	}
	for _, key := range []string{"bonus", "upload", "download", "ratio", "seeding", "leeching", "seeding_size", "leeching_size"} {
		token := bytes.TrimSpace(fields[key])
		if len(token) == 0 || token[0] == '"' || bytes.Equal(token, []byte("null")) {
			continue
		}
		value, err := strconv.ParseFloat(string(token), 64)
		if err != nil {
			return nil, invalidSiteAccountSnapshot()
		}
		number := strconv.FormatFloat(value, 'g', -1, 64)
		if !strings.ContainsAny(number, ".eE") {
			number += ".0"
		}
		fields[key] = json.RawMessage(number)
	}
	var out bytes.Buffer
	out.WriteByte('{')
	comma := false
	for _, key := range sourceSiteAccountColumns {
		value, ok := fields[key]
		if !ok {
			continue
		}
		if comma {
			out.WriteByte(',')
		}
		comma = true
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
