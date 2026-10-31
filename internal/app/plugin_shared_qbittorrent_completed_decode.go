package app

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"light-media/internal/sourcejson"
)

type sharedQBittorrentCompletedRequest struct {
	downloaderID string
	hashes       *string
	tags         []json.RawMessage
}

func decodeSharedQBittorrentCompleted(raw json.RawMessage) (out sharedQBittorrentCompletedRequest, err error) {
	invalid := func() (sharedQBittorrentCompletedRequest, error) {
		return out, bad("INVALID_PLUGIN_VALUE", "qB已完成任务查询须为唯一字段的有效Unicode有界对象；ids/tags须为null、字符串或数组")
	}
	if len(raw) == 0 || len(raw) > 128<<10 || !utf8.Valid(raw) || !json.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	start, e := d.Token()
	if e != nil || start != json.Delim('{') {
		return invalid()
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, e := d.Token()
		key, ok := token.(string)
		if e != nil || !ok || key != "downloader_id" && key != "ids" && key != "tags" {
			return invalid()
		}
		if _, exists := values[key]; exists {
			return invalid()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return invalid()
		}
		values[key] = value
	}
	end, e := d.Token()
	if e != nil || end != json.Delim('}') {
		return invalid()
	}
	if _, e = d.Token(); e != io.EOF {
		return invalid()
	}
	if json.Unmarshal(values["downloader_id"], &out.downloaderID) != nil || out.downloaderID == "" || len(out.downloaderID) > 64 {
		return invalid()
	}
	for _, c := range out.downloaderID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return invalid()
		}
	}
	ids := bytes.TrimSpace(values["ids"])
	if len(ids) != 0 && string(ids) != "null" {
		var text string
		if ids[0] == '"' {
			if json.Unmarshal(ids, &text) != nil {
				return invalid()
			}
		} else if ids[0] == '[' {
			var list []json.RawMessage
			if json.Unmarshal(ids, &list) != nil {
				return invalid()
			}
			stringsList := make([]string, len(list))
			for i, value := range list {
				stringsList[i], err = sourcejson.String(value)
				if err != nil {
					return invalid()
				}
			}
			text = strings.Join(stringsList, "|")
		} else {
			return invalid()
		}
		out.hashes = &text
	}
	tags := bytes.TrimSpace(values["tags"])
	if len(tags) == 0 || string(tags) == "null" {
		return out, nil
	}
	if tags[0] == '"' {
		var text string
		if json.Unmarshal(tags, &text) != nil {
			return invalid()
		}
		if text != "" {
			for _, tag := range strings.Split(text, ",") {
				value, _ := json.Marshal(tag)
				out.tags = append(out.tags, value)
			}
		}
	} else if tags[0] == '[' {
		if json.Unmarshal(tags, &out.tags) != nil {
			return invalid()
		}
	} else {
		return invalid()
	}
	return out, nil
}
