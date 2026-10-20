package subscriptionstats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func asyncSourceString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", ErrDependency
	}
	var ascii strings.Builder
	if err := appendSourceScalar(&ascii, raw); err != nil {
		return "", ErrDependency
	}
	checked, err := asyncJSON([]byte(ascii.String()))
	if err != nil {
		return "", err
	}
	var text string
	if json.Unmarshal(checked, &text) != nil {
		return "", ErrDependency
	}
	return text, nil
}

// Select only the string the source actually consumes. A discarded HTTP proxy
// must not override a valid HTTPS proxy. Isolated-surrogate proxy strings have
// no Go Unicode representation; reject this native dependency before routing,
// rather than silently send to Go JSON's replacement-character destination.
func asyncSourceProxy(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	selectString := func(v json.RawMessage) (string, error) {
		v = bytes.TrimSpace(v)
		if len(v) == 0 || v[0] != '"' {
			return "", nil
		}
		text, err := asyncSourceString(v)
		if err != nil {
			return "", ErrDependency
		}
		return asyncProxyTrim(text), nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		return selectString(raw)
	}
	if len(raw) == 0 || raw[0] != '{' {
		return "", nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return "", ErrDependency
	}
	for _, key := range []string{"https", "http"} {
		proxy, err := selectString(fields[key])
		if err != nil {
			return "", err
		}
		if proxy != "" {
			return proxy, nil
		}
	}
	return "", nil
}

// SourceSettings decodes the trusted source snapshot in its actual lazy
// exception order: share, explicit proxy, URL, then header strings. Raw proxy
// surrogates are an explicit native dependency, never replacement URL routing.
// Native Go snapshots with known Unicode strings can use SubRegFields directly.
func SourceSettings(raw json.RawMessage) (SettingsSnapshot, Result, error) {
	var setting SettingsSnapshot
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return setting, Result{}, ErrDependency
	}
	if json.Unmarshal(fields["share"], &setting.Share) != nil || setting.Share == nil {
		return setting, Result{}, ErrDependency
	}
	if !*setting.Share {
		return setting, Result{}, nil
	}
	if json.Unmarshal(fields["known"], &setting.Known) != nil || !setting.Known {
		return setting, Result{}, ErrDependency
	}
	proxy, err := asyncSourceProxy(fields["proxies"])
	if err != nil {
		return setting, Result{}, err
	}
	if proxy != "" {
		setting.Proxies = map[string]string{"https": proxy}
	}
	if _, err = asyncProxyURL(proxy); err != nil {
		result, e := asyncErrorResult(err)
		return setting, result, e
	}
	setting.ServerHost, err = asyncSourceString(fields["server_host"])
	if err != nil {
		if errors.Is(err, ErrAsyncUnicode) {
			return setting, Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, nil
		}
		return setting, Result{}, err
	}
	if _, err = asyncTarget(strings.TrimRight(setting.ServerHost, "/") + "/subscribe/add"); err != nil && !errors.Is(err, errAsyncProtocol) {
		result, e := asyncErrorResult(err)
		return setting, result, e
	}
	for _, key := range []string{"user_agent", "user_uid"} {
		value, ok := fields[key]
		if !ok {
			return setting, Result{}, ErrDependency
		}
		if key == "user_uid" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		text, e := asyncSourceString(value)
		if e != nil {
			if errors.Is(e, ErrAsyncUnicode) {
				return setting, Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, nil
			}
			return setting, Result{}, e
		}
		if key == "user_agent" {
			setting.UserAgent = text
		} else {
			setting.UserUID = &text
		}
	}
	if _, err = asyncHeaders(setting); err != nil {
		return setting, Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, nil
	}
	return setting, Result{}, nil
}

func (c *AsyncClient) SubRegSourceSettings(ctx context.Context, settings json.RawMessage, payload []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	setting, result, err := SourceSettings(settings)
	if err != nil || result.SourceError != "" {
		return result, err
	}
	return c.SubReg(ctx, setting, payload)
}
func (c *AsyncClient) SubRegSourceFields(ctx context.Context, settings, fields json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	setting, result, err := SourceSettings(settings)
	if err != nil || result.SourceError != "" {
		return result, err
	}
	return c.SubRegFields(ctx, setting, fields)
}
