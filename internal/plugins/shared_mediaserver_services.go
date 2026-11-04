package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

const (
	MediaServerGetServicesCapability = "mediaserver.services.get"
	MediaServerGetServiceCapability  = "mediaserver.service.get"
	MediaServerIsInactiveCapability  = "mediaserver.instance.is_inactive"
)

// MediaServerInstance retains identity, not an invocation callback. Supply the
// current invocation's HostCaller on every operation. Copying this value never
// transfers authority to another plugin session. No provider/name is a token.
type MediaServerInstance struct {
	Reference string `json:"reference"`
}

type MediaServerServiceInfo struct {
	Name            json.RawMessage     `json:"name"`
	Type            json.RawMessage     `json:"type"`
	Config          *MediaServerConfig  `json:"config"`
	ConfigFieldsSet []string            `json:"config_fields_set"`
	Instance        MediaServerInstance `json:"instance"`
}

type MediaServerServicesRequest struct {
	Type  json.RawMessage   `json:"type"`
	Names []json.RawMessage `json:"names"`
}
type MediaServerServiceRequest struct {
	Name json.RawMessage `json:"name"`
	Type json.RawMessage `json:"type"`
}
type MediaServerInstanceRequest struct {
	Reference string `json:"reference"`
}

// The envelope transports an exact source exception class without raw error,
// config, URL, password or vendor diagnostic text. Platform errors stay errors.
type MediaServerServicesResponse struct {
	Result           json.RawMessage `json:"result"`
	SourceErrorClass string          `json:"source_error_class,omitempty"`
}
type MediaServerServiceSourceError struct{ Class string }

func (e *MediaServerServiceSourceError) Error() string {
	return "media server service source exception: " + e.Class
}

type SharedMediaServerServices struct{ Host HostCaller }

func mediaServerServiceCall(ctx context.Context, host HostCaller, method string, input any, output any) error {
	if ctx == nil || host == nil {
		return errors.New("media service operation requires the current host caller")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = host(ctx, method, raw)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return errors.New("invalid media service response size")
	}
	var response MediaServerServicesResponse
	if err = mediaServerServiceDecode(raw, &response); err != nil {
		return err
	}
	if response.SourceErrorClass != "" {
		if !bytes.Equal(response.Result, []byte("null")) {
			return errors.New("invalid media service source error")
		}
		return &MediaServerServiceSourceError{Class: response.SourceErrorClass}
	}
	if len(response.Result) == 0 {
		return errors.New("missing media service result")
	}
	return mediaServerServiceDecode(response.Result, output)
}
func mediaServerServiceDecode(raw json.RawMessage, output any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return errors.New("invalid media service response")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid media service response")
	}
	return nil
}
func mediaServerServiceInfoValid(info MediaServerServiceInfo) bool {
	if len(info.Instance.Reference) != 67 || info.Instance.Reference[:3] != "ms_" {
		return false
	}
	for _, ch := range info.Instance.Reference[3:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	for _, raw := range []json.RawMessage{info.Name, info.Type} {
		if bytes.Equal(raw, []byte("null")) {
			continue
		}
		if _, err := DecodeSystemConfigJSONString(raw); err != nil {
			return false
		}
	}
	return info.Config != nil
}

// GetServices returns source dictionary order, not a Go map that loses nullable
// keys or conflates null with "". Each item is one real retained object and a
// new query projection; object identity does not cache its config snapshot.
func (s SharedMediaServerServices) GetServices(ctx context.Context, input MediaServerServicesRequest) ([]MediaServerServiceInfo, error) {
	var out []MediaServerServiceInfo
	if err := mediaServerServiceCall(ctx, s.Host, MediaServerGetServicesCapability, input, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("invalid media services result")
	}
	for _, info := range out {
		if !mediaServerServiceInfoValid(info) {
			return nil, errors.New("invalid media service identity")
		}
	}
	return out, nil
}
func (s SharedMediaServerServices) GetService(ctx context.Context, input MediaServerServiceRequest) (*MediaServerServiceInfo, error) {
	var out *MediaServerServiceInfo
	if err := mediaServerServiceCall(ctx, s.Host, MediaServerGetServiceCapability, input, &out); err != nil {
		return nil, err
	}
	if out != nil && !mediaServerServiceInfoValid(*out) {
		return nil, errors.New("invalid media service identity")
	}
	return out, nil
}
func (instance MediaServerInstance) IsInactive(ctx context.Context, current HostCaller) (bool, error) {
	var out *bool
	err := mediaServerServiceCall(ctx, current, MediaServerIsInactiveCapability, MediaServerInstanceRequest{Reference: instance.Reference}, &out)
	if err != nil {
		return false, err
	}
	if out == nil {
		return false, errors.New("invalid media instance state result")
	}
	return *out, nil
}
