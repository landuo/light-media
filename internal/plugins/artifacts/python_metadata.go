package artifacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"light-media/internal/plugins"
)

// Original Python package.json is accepted only as display metadata. Neither
// acquisition nor inspection calls this parser; no URL/class/dependency in the
// source can turn such an entry into executable Go code.
func discoverPythonMetadata(body []byte, repository string) ([]Discovery, error) {
	return discoverPythonMetadataForVersion(body, repository, false)
}

func discoverPythonMetadataForVersion(body []byte, repository string, requireV2 bool) ([]Discovery, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || fields["schema_version"] != nil || fields["plugins"] != nil {
		return nil, errors.New("unsupported repository format")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("invalid Python repository metadata")
	}
	out := []Discovery{}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		id, ok := token.(string)
		if !ok || !idPattern.MatchString(id) || seen[strings.ToLower(id)] || len(out) >= 1024 {
			return nil, errors.New("invalid or duplicate Python metadata ID")
		}
		seen[strings.ToLower(id)] = true
		var info struct {
			SystemVersion json.RawMessage `json:"system_version"`
			Level         *int            `json:"level"`
			PublicKey     string          `json:"key"`
			V2            bool            `json:"v2"`
			Name          string          `json:"name"`
			Version       string          `json:"version"`
			Description   string          `json:"description"`
			Icon          string          `json:"icon"`
			Author        string          `json:"author"`
			AuthorURL     string          `json:"author_url"`
			Labels        json.RawMessage `json:"labels"`
			History       json.RawMessage `json:"history"`
		}
		if err = decoder.Decode(&info); err != nil {
			return nil, errors.New("invalid Python plugin display metadata")
		}
		if requireV2 && !info.V2 {
			continue
		}
		if info.Name == "" || info.Version == "" {
			return nil, errors.New("invalid Python plugin display metadata")
		}
		var history map[string]string
		if len(info.History) != 0 && json.Unmarshal(info.History, &history) != nil {
			return nil, errors.New("invalid Python plugin display history")
		}
		label, err := normalizeMetadataLabel(info.Labels)
		if err != nil {
			return nil, err
		}
		manifest := plugins.Manifest{PluginID: id, Name: info.Name, Version: info.Version, Description: info.Description, Icon: info.Icon, Author: info.Author, AuthorURL: info.AuthorURL, Runtime: "python"}
		manifest.AuthLevel, manifest.PluginPublicKey = info.Level, info.PublicKey
		out = append(out, Discovery{RepositoryURL: repository, PluginID: id, CurrentVersion: info.Version, Current: Compatibility{Manifest: manifest}, History: history, HistoryJSON: info.History, Label: label, Releases: []Compatibility{}, UnavailableReason: "原 MoviePilot Python 插件元数据，仅供查看；需重写并发布 Go 制品后才能安装"})
		annotateDiscoverySystemVersion(&out[len(out)-1], info.SystemVersion)
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing Python metadata")
	}
	for i := range out {
		out[i].AddTime = len(out) - i
	}
	return out, nil
}
