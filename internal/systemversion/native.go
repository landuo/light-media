package systemversion

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// NativeReleasesURL is the approved light-media project, not a source MoviePilot
// version reference or an arbitrary URL supplied by a plugin/user message.
const NativeReleasesURL = "https://api.github.com/repos/landuo/light-media/releases"

var ErrNativeRelease = errors.New("invalid light-media release metadata")

// NativeVersionKey admits only optional lowercase v and dot-separated ASCII
// decimal components. No prerelease/channel/v2 family guess. Leading zeroes are
// numeric, so equality and ordering use the same canonical decimal tuple.
func NativeVersionKey(tag string) (string, bool) {
	if strings.HasPrefix(tag, "v") {
		tag = tag[1:]
	}
	if tag == "" {
		return "", false
	}
	parts := strings.Split(tag, ".")
	for i, part := range parts {
		if part == "" {
			return "", false
		}
		for _, c := range []byte(part) {
			if c < '0' || c > '9' {
				return "", false
			}
		}
		part = strings.TrimLeft(part, "0")
		if part == "" {
			part = "0"
		}
		parts[i] = part
	}
	return strings.Join(parts, "."), true
}
func nativeVersionCompare(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) && i < len(right); i++ {
		if len(left[i]) < len(right[i]) {
			return -1
		}
		if len(left[i]) > len(right[i]) {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

// SelectNativeRelease retains the original numeric-tuple/stable-last ordering
// but uses the approved native numeric grammar, without changing SelectRelease.
// Non-numeric tags are not native release candidates. Empty valid releases is
// unknown (nil), never a synthesized local/latest version.
func SelectNativeRelease(raw []byte) (*string, error) {
	if !utf8.Valid(raw) {
		return nil, ErrNativeRelease
	}
	var rows []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&rows) != nil || decoder.Decode(new(any)) != io.EOF || bytes.Equal(bytes.TrimSpace(raw), []byte(`null`)) {
		return nil, ErrNativeRelease
	}
	var selected *string
	selectedKey := ""
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			return nil, ErrNativeRelease
		}
		var tag string
		if fields["tag_name"] == nil || json.Unmarshal(fields["tag_name"], &tag) != nil || bytes.Equal(bytes.TrimSpace(fields["tag_name"]), []byte(`null`)) {
			return nil, ErrNativeRelease
		}
		key, ok := NativeVersionKey(tag)
		if !ok {
			continue
		}
		if selected == nil || nativeVersionCompare(key, selectedKey) >= 0 {
			value := tag
			selected = &value
			selectedKey = key
		}
	}
	return selected, nil
}

func NativeMessage(remote *string, backend, frontend string) string {
	line := func(side, local string) string {
		if remote != nil {
			remoteKey, remoteOK := NativeVersionKey(*remote)
			localKey, localOK := NativeVersionKey(local)
			if remoteOK && localOK && remoteKey == localKey {
				return "当前光影" + side + "版本：" + local + "，已是最新版本"
			}
			return "当前光影" + side + "版本：" + local + "，远程版本：" + *remote
		}
		return "当前光影" + side + "版本：" + local + "，远程版本：未知"
	}
	return line("后端", backend) + "\n" + line("前端", frontend)
}
