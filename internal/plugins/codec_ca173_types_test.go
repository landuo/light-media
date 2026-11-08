package plugins

import (
	"encoding/json"
	"regexp"
)

type CodecCAIdentity struct {
	PluginID   string `json:"plugin_id"`
	InstanceID string `json:"instance_id"`
	Version    string `json:"plugin_version"`
}

type CodecCAReplyError struct {
	Code string `json:"code"`
}

type CodecCAFrame struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"request_id"`
	Type     string `json:"type"`
	CodecCAIdentity
	Method  string             `json:"method,omitempty"`
	Payload json.RawMessage    `json:"payload,omitempty"`
	Result  json.RawMessage    `json:"result,omitempty"`
	Error   *CodecCAReplyError `json:"error,omitempty"`
}

type CodecCAError struct{ Code string }

func (e *CodecCAError) Error() string { return "plugin: " + e.Code }

func codecCAFailure(code string) error { return &CodecCAError{Code: code} }

var codecCAIdentityPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

var codecCAMethodPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,95}$`)
