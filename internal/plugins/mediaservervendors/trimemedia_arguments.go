package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"reflect"
	"strings"
)

// Compatibility name, not an ordinary JSON constructor gate.
type TrimeArgumentBoundary struct{ Field string }

func (e *TrimeArgumentBoundary) Error() string {
	return "TrimeMedia argument domain not yet implemented: " + e.Field
}

type trimeArguments struct {
	host, username, password, accessCode, playHost, syncLibraries json.RawMessage
	incomplete                                                    bool
}

func trimeParseArguments(c plugins.MediaServerConfig) (trimeArguments, error) {
	var out trimeArguments
	values, err := constructorFields(c.Config)
	if err != nil {
		return out, err
	}
	if !ejTruth(values["host"]) || !ejTruth(values["username"]) || !ejTruth(values["password"]) {
		out.incomplete = true
		return out, nil
	}
	out.host = constructorJSON(values["host"])
	out.username = constructorJSON(values["username"])
	out.password = constructorJSON(values["password"])
	out.accessCode = constructorJSON(values["access_code"])
	out.playHost = constructorJSON(values["play_host"])
	out.syncLibraries, err = constructorLibraries(c.SyncLibraries)
	return out, err
}
func trimeContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("TrimeMedia requires context")
	}
	return ctx.Err()
}
func trimeNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func trimeTruthy(v *string) bool   { return v != nil && *v != "" }
func trimeString(v string) *string { return &v }
func trimeStandardize(host string) string {
	if host == "" {
		return host
	}
	if !strings.HasSuffix(host, "/") {
		host += "/"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return host
}
func trimeMissing(attribute string) error {
	return &mediaserverruntime.SourceError{Class: "AttributeError", Cause: errors.New("'NoneType' object has no attribute '" + attribute + "'")}
}
