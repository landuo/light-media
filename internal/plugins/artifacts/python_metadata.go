package artifacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"light-media/internal/plugins"
)

// Python package documents are display-only. Their IDs never pass through the
// executable artifact parser, installer or a filesystem path resolver.
func discoverPythonMetadata(body []byte, repository string) ([]Discovery, error) {
	// This generic fallback follows native repository parsing. Never turn a
	// malformed native envelope into successful Python display metadata. The
	// explicit package readers below have no reserved root keys in MoviePilot.
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || fields["schema_version"] != nil || fields["plugins"] != nil {
		return nil, errors.New("unsupported repository format")
	}
	return discoverPythonMetadataForVersion(body, repository, false)
}
func discoverPythonMetadataForVersion(body []byte, repository string, requireV2 bool) ([]Discovery, error) {
	prepared, constants, err := pythonJSONPrepare(body)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(prepared))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("invalid Python repository metadata")
	}
	type member struct {
		id           string
		rawID, value json.RawMessage
	}
	order := []string{}
	members := map[string]member{}
	for decoder.More() {
		start := decoder.InputOffset()
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		if _, ok := token.(string); !ok {
			return nil, errors.New("invalid Python metadata ID")
		}
		rawID := pythonDisplayTokenRaw(prepared, start, decoder.InputOffset())
		id, err := pythonDisplayVersionKey(rawID)
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, errors.New("invalid Python plugin display metadata")
		}
		if _, exists := members[id]; !exists {
			order = append(order, id)
		}
		members[id] = member{id, rawID, value}
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing Python metadata")
	}
	out := []Discovery{}
	for position, id := range order {
		entry := members[id]
		value := bytes.TrimSpace(entry.value)
		if len(value) == 0 || value[0] != '{' {
			continue
		}
		// Maps, unlike encoding/json struct decoding, retain Python's case-sensitive
		// field lookup and last duplicate value. Root names have no reserved meaning.
		var info map[string]json.RawMessage
		if json.Unmarshal(value, &info) != nil {
			return nil, errors.New("invalid Python plugin display metadata")
		}
		for key, raw := range info {
			info[key] = pythonJSONRestore(raw, constants)
		}
		if requireV2 && !bytes.Equal(bytes.TrimSpace(info["v2"]), []byte("true")) {
			continue
		}
		display := &PythonDisplay{ID: entry.rawID, Name: info["name"], Version: info["version"], Description: info["description"], Icon: info["icon"], Author: info["author"], History: info["history"], PublicKey: info["key"], Level: info["level"], SystemVersion: info["system_version"], Release: info["release"]}
		label, badLabel, err := normalizePythonDisplayLabel(info["labels"])
		if err != nil {
			return nil, err
		}
		display.Label, _ = json.Marshal(label)
		if label == "" {
			display.Label = json.RawMessage("null")
		}
		if err = display.initialize(); err != nil {
			return nil, err
		}
		if badLabel {
			display.wireErrors["plugin_label"] = true
		}
		var history map[string]string
		_ = json.Unmarshal(info["history"], &history)
		manifest := plugins.Manifest{PluginID: id, Name: pythonDisplayText(info["name"]), Version: pythonDisplayText(info["version"]), Description: pythonDisplayText(info["description"]), Icon: pythonDisplayText(info["icon"]), Author: pythonDisplayText(info["author"]), Runtime: "python"}
		_ = json.Unmarshal(info["level"], &manifest.AuthLevel)
		_ = json.Unmarshal(info["key"], &manifest.PluginPublicKey)
		// Auth-level/public-key are inert source display data, not native grants.
		// The excluded official-auth subsystem is not executed or emulated here.
		out = append(out, Discovery{SourceDisplay: display, AddTime: len(order) - position, RepositoryURL: repository, PluginID: id, CurrentVersion: manifest.Version, Current: Compatibility{Manifest: manifest}, History: history, HistoryJSON: info["history"], Label: label, Releases: []Compatibility{}, UnavailableReason: "原 MoviePilot Python 插件元数据，仅供查看；需重写并发布 Go 制品后才能安装"})
		annotateDiscoverySystemVersion(&out[len(out)-1], info["system_version"])
	}
	return out, nil
}

func normalizePythonDisplayLabel(raw json.RawMessage) (string, bool, error) {
	trim := func(v string) string {
		return strings.TrimFunc(v, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	}
	value := bytes.TrimSpace(raw)
	if len(value) == 0 {
		return "", false, nil
	}
	if value[0] == '"' {
		label, err := pythonDisplayVersionKey(value)
		label = trim(label)
		return label, !utf8.ValidString(label), err
	}
	if value[0] != '[' {
		return "", false, nil
	}
	prepared, constants, err := pythonJSONPrepare(value)
	if err != nil {
		return "", false, err
	}
	var values []json.RawMessage
	if err = json.Unmarshal(prepared, &values); err != nil {
		return "", false, err
	}
	labels := []string{}
	for _, item := range values {
		label, err := pythonDisplayVersionKey(pythonJSONRestore(item, constants))
		if err != nil {
			return "", false, err
		}
		if label = trim(label); label != "" {
			labels = append(labels, label)
		}
	}
	label := strings.Join(labels, " ")
	return label, !utf8.ValidString(label), nil
}
