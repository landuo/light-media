package app

import (
	"encoding/json"
	"io"
	"light-media/internal/plugins"
	"net/http"
)

type pluginCard struct {
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	RepositoryURL     string `json:"repo_url,omitempty"`
	RepositoryVersion string `json:"repository_version,omitempty"`
	HasUpdate         bool   `json:"has_update,omitempty"`
	DisplayVersion    string `json:"display_version,omitempty"`
	SourceInstanceID  string `json:"source_instance_id,omitempty"`
	plugins.Manifest
	InstanceID string `json:"instance_id"`
	Installed  bool   `json:"installed"`
	State      bool   `json:"state"`
}

func decodePluginBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
	}
	return nil
}
func decodePluginJSONBody(body io.Reader, value any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
	}
	return nil
}
