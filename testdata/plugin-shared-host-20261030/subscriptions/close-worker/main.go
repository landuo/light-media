// Owned shared subscription verification worker; never a built-in business adapter.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"light-media/internal/plugins"
)

var fixtureCapabilities = "subscriptions.get,subscriptions.list,subscriptions.list_by_tmdbid,subscriptions.update"
var fixtureAuth = "bear"

var auditFixtureVersion = "1.0"

func manifest() plugins.Manifest {
	caps := []string{}
	if fixtureCapabilities != "" {
		caps = append(strings.Split(fixtureCapabilities, ","), plugins.BaseGetInstanceMetadata)
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedSharedHost", Name: "Owned shared subscriptions", Version: auditFixtureVersion, Runtime: "go-executable", Entry: "owned-shared-host-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "resource", "snapshot", "http", "close", "stop_service", "stop"}, Capabilities: caps, HasPage: true, APIs: []plugins.HTTPAPI{{Key: "shared", Path: "/shared", Methods: []string{"POST"}, Auth: fixtureAuth, Body: "json", BodyRequired: true}, {Key: "exercise", Path: "/exercise", Methods: []string{"POST"}, Auth: fixtureAuth, Body: "json", BodyRequired: true}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: fixtureAuth}}}
}

type object struct {
	mu        sync.Mutex
	config    map[string]any
	initCount int
	previous  plugins.HostCaller
	last      any
}

func (o *object) snapshot(ctx context.Context, host plugins.HostCaller) (json.RawMessage, error) {
	o.mu.Lock()
	previous := o.previous
	o.previous = host
	count := o.initCount
	o.mu.Unlock()
	denied := true
	if previous != nil {
		_, e := previous(ctx, plugins.SharedSubscriptionsList, json.RawMessage(`{}`))
		denied = e != nil
	}
	return json.Marshal(map[string]any{"pid": os.Getpid(), "init_count": count, "stale_denied": denied, "had_previous": previous != nil})
}
func typed(ctx context.Context, host plugins.HostCaller, method string, raw json.RawMessage) (json.RawMessage, error) {
	s := plugins.SharedSubscriptions{Host: host}
	var out any
	var e error
	switch method {
	case plugins.SharedSubscriptionsGet:
		var in plugins.SharedSubscriptionGetV1
		if e = json.Unmarshal(raw, &in); e == nil {
			out, e = s.Get(ctx, in)
		}
	case plugins.SharedSubscriptionsList:
		var in plugins.SharedSubscriptionListV1
		if e = json.Unmarshal(raw, &in); e == nil {
			out, e = s.List(ctx, in)
		}
	case plugins.SharedSubscriptionsListByTMDBID:
		var in plugins.SharedSubscriptionTMDBV1
		if e = json.Unmarshal(raw, &in); e == nil {
			out, e = s.ListByTMDBID(ctx, in)
		}
	case plugins.SharedSubscriptionsUpdate:
		var in plugins.SharedSubscriptionUpdateV1
		if e = json.Unmarshal(raw, &in); e == nil {
			out, e = s.Update(ctx, in)
		}
	default:
		return nil, errors.New("unsupported fixture operation")
	}
	if e != nil {
		return nil, e
	}
	return json.Marshal(out)
}
func (o *object) resource(ctx context.Context, host plugins.HostCaller, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if e := json.Unmarshal(raw, &in); e != nil {
		return nil, e
	}
	return typed(ctx, host, in.Method, in.Payload)
}
func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		var config map[string]any
		if e := json.Unmarshal(raw, &config); e != nil {
			return nil, e
		}
		if config == nil {
			config = map[string]any{}
		}
		o.mu.Lock()
		o.config = config
		o.initCount++
		o.mu.Unlock()
		return json.RawMessage(`null`), nil
	case "close":
        o.mu.Lock()
        sid, _ := o.config["close_subscription_id"].(string)
        o.mu.Unlock()
        if sid != "" && auditFixtureVersion == "1.0" {
            value, err := (plugins.SharedSubscriptions{Host: host}).Update(ctx, plugins.SharedSubscriptionUpdateV1{ID:sid,Patch:json.RawMessage(`{"keyword":"old-version-close"}`)})
            marker := map[string]any{"version":auditFixtureVersion,"pid":os.Getpid(),"dao_ok":err==nil,"row_returned":value!=nil}
            if err != nil { marker["dao_error"] = err.Error() }
            if saveErr := (plugins.Base{Host:host}).SaveData(ctx,"owned-close-dao",marker); saveErr!=nil { return nil, saveErr }
            return json.RawMessage(`null`), err
        }
        return json.RawMessage(`null`), nil
    case "stop", "stop_service":
        return json.RawMessage(`null`), nil
	case "get_state":
		return json.RawMessage(`false`), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "snapshot":
		return o.snapshot(ctx, host)
	case "resource":
		return o.resource(ctx, host, raw)
	case "get_form":
		nodes := []any{}
		for _, v := range [][2]string{{"subscription_id", "Synthetic subscription ID"}, {"state_filter", "State list (comma separated)"}, {"tmdbid", "TMDB query JSON"}, {"patch", "Update patch JSON"}, {"expected_revision", "Optional expected revision"}} {
			component := "VTextField"
			if v[0] == "patch" {
				component = "VTextarea"
			}
			nodes = append(nodes, map[string]any{"component": component, "props": map[string]any{"model": v[0], "label": v[1]}})
		}
		return json.Marshal([]any{nodes, map[string]any{"subscription_id": "owned-s05-browser", "state_filter": "N,R", "tmdbid": "7001", "patch": `{"keyword":"owned browser update","note":[1,2],"state":"R"}`, "expected_revision": ""}})
	case "get_page":
		metadata, metadataErr := (plugins.Base{Host: host}).GetInstanceMetadata(ctx)
		if metadataErr != nil {
			return nil, metadataErr
		}
		o.mu.Lock()
		sid, _ := o.config["subscription_id"].(string)
		last := o.last
		count := o.initCount
		o.mu.Unlock()
		if sid == "" {
			sid = "owned-s05-browser"
		}
		row, e := (plugins.SharedSubscriptions{Host: host}).Get(ctx, plugins.SharedSubscriptionGetV1{ID: sid})
		value := map[string]any{"pid": os.Getpid(), "init_count": count, "current": row, "last_operation": last}
		if e != nil {
			value["read_error"] = e.Error()
		}
		text, _ := json.MarshalIndent(value, "", "  ")
		buttons := []any{}
		for _, op := range []string{"get", "list", "state", "tmdb", "update"} {
			buttons = append(buttons, map[string]any{"component": "VBtn", "props": map[string]any{"color": "primary", "variant": "outlined", "class": "ma-1"}, "text": op, "events": map[string]any{"click": map[string]any{"api": "plugin/" + metadata.InstanceID + "/exercise", "method": "POST", "params": map[string]any{"operation": op}}}})
		}
		return json.Marshal([]any{map[string]any{"component": "div", "content": buttons}, map[string]any{"component": "pre", "text": string(text)}})
	case "http":
		var request plugins.HTTPRequest
		if e := json.Unmarshal(raw, &request); e != nil {
			return nil, e
		}
		if request.Key == "snapshot" {
			result, e := o.snapshot(ctx, host)
			if e != nil {
				return nil, e
			}
			return plugins.JSONHTTPResponse(200, json.RawMessage(result))
		}
		payload := request.Body
		if request.Key == "exercise" {
			var action struct{ Operation string }
			if e := json.Unmarshal(payload, &action); e != nil {
				return nil, e
			}
			o.mu.Lock()
			config := map[string]any{}
			for k, v := range o.config {
				config[k] = v
			}
			o.mu.Unlock()
			get := func(k, def string) string {
				v, _ := config[k].(string)
				if v == "" {
					return def
				}
				return v
			}
			in := map[string]any{}
			method := ""
			switch action.Operation {
			case "get":
				method = plugins.SharedSubscriptionsGet
				in["id"] = get("subscription_id", "owned-s05-browser")
			case "list":
				method = plugins.SharedSubscriptionsList
			case "state":
				method = plugins.SharedSubscriptionsList
				in["state"] = get("state_filter", "N,R")
			case "tmdb":
				method = plugins.SharedSubscriptionsListByTMDBID
				in["tmdbid"] = json.RawMessage(get("tmdbid", "7001"))
			case "update":
				method = plugins.SharedSubscriptionsUpdate
				in["id"] = get("subscription_id", "owned-s05-browser")
				in["patch"] = json.RawMessage(get("patch", `{"keyword":"owned browser update","note":[1,2],"state":"R"}`))
				if v := get("expected_revision", ""); v != "" {
					n, e := strconv.Atoi(v)
					if e != nil {
						return plugins.JSONHTTPResponse(422, map[string]any{"success": false, "message": "invalid revision"})
					}
					in["expected_revision"] = n
				}
			default:
				return nil, errors.New("unsupported owned exercise")
			}
			var e error
			payload, e = json.Marshal(map[string]any{"method": method, "payload": in})
			if e != nil {
				return nil, e
			}
		}
		if request.Key != "shared" && request.Key != "exercise" {
			return nil, errors.New("unsupported owned API")
		}
		result, e := o.resource(ctx, host, payload)
		status := 200
		response := map[string]any{"success": e == nil, "result": json.RawMessage(result), "pid": os.Getpid()}
		if e != nil {
			status = 422
			response["message"] = e.Error()
			response["result"] = nil
		}
		o.mu.Lock()
		o.last = response
		o.mu.Unlock()
		return plugins.JSONHTTPResponse(status, response)
	}
	return nil, errors.New("unsupported owned method")
}
func main() {
	m := manifest()
	if e := m.Validate(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	o := &object{}
	if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, o.handle); e != nil {
		fmt.Fprintln(os.Stderr, "owned shared worker failed")
		os.Exit(1)
	}
}
