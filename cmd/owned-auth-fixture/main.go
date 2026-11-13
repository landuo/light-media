// An independently built SDK worker for synthetic authentication tests. It
// checks an owned HMAC proof; it never contacts a real identity provider.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"light-media/internal/plugins"
)

var fixtureCachedConfig string // -ldflags=-X main.fixtureCachedConfig=true

var fixtureFrontend string // -ldflags=-X main.fixtureFrontend=true

func manifest() plugins.Manifest {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedAuth", Name: "Owned authentication", Version: "1.0", Runtime: "go-executable", Entry: "owned-auth-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_auth_providers", "http", "close", "stop_service", "stop"},
		Capabilities: append(plugins.BaseMethods(), plugins.AuthTicketCreateCapability),
		APIs:         []plugins.HTTPAPI{{Key: "authenticate", Path: "/authenticate", Methods: []string{"POST"}, Auth: "anonymous", Body: "json", BodyRequired: true}, {Key: "stale", Path: "/stale", Methods: []string{"POST"}, Auth: "anonymous", Body: "json", BodyRequired: true}}}
	if fixtureFrontend == "true" {
		m.Frontend = &plugins.Frontend{Contract: plugins.FrontendFederationContract, Root: "ui", Entry: "remoteEntry.js", Exposes: []string{"AuthPage"}, Shared: []string{"vue", "vuetify"}, APIContract: plugins.FrontendAPIContract}
	}
	return m
}

type object struct {
	mu       sync.Mutex
	retained plugins.HostCaller
	config   json.RawMessage
}

func (o *object) handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	var config json.RawMessage
	var err error
	if fixtureCachedConfig == "true" && method != "init" {
		o.mu.Lock()
		config = append(json.RawMessage(nil), o.config...)
		o.mu.Unlock()
	} else {
		config, err = base.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if fixtureCachedConfig == "true" {
			o.mu.Lock()
			o.config = append(json.RawMessage(nil), config...)
			o.mu.Unlock()
		}
	}
	var options struct {
		Enabled    bool            `json:"enabled"`
		Providers  json.RawMessage `json:"providers"`
		FailState  bool            `json:"fail_state"`
		FailGetter bool            `json:"fail_getter"`
		FailRender bool            `json:"fail_render"`
	}
	if len(config) > 0 && string(config) != "null" {
		if err = json.Unmarshal(config, &options); err != nil {
			return nil, err
		}
	}
	switch method {
	case "init", "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	case "get_state":
		if options.FailState {
			return nil, errors.New("owned state failure")
		}
		return json.Marshal(options.Enabled)
	case "get_auth_providers":
		if options.FailGetter {
			return nil, errors.New("owned getter failure")
		}
		if len(options.Providers) > 0 {
			return options.Providers, nil
		}
		return json.RawMessage(`[{"name":"Owned authentication"}]`), nil
	case "get_render_mode":
		if options.FailRender {
			return nil, errors.New("owned render failure")
		}
		if fixtureFrontend == "true" {
			return json.RawMessage(`["vue","ui"]`), nil
		}
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "http":
		var request plugins.HTTPRequest
		if err = json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		var input struct {
			Nonce      string `json:"nonce"`
			Proof      string `json:"proof"`
			UserID     int64  `json:"user_id"`
			ProviderID string `json:"provider_id"`
		}
		if err = json.Unmarshal(request.Body, &input); err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, []byte("owned-auth-proof-key"))
		_, _ = mac.Write([]byte(input.Nonce))
		proof, decodeErr := hex.DecodeString(input.Proof)
		status := 401
		body := map[string]any{"authenticated": false}
		if input.Nonce != "" && decodeErr == nil && hmac.Equal(proof, mac.Sum(nil)) {
			caller := host
			if request.Key == "stale" {
				o.mu.Lock()
				caller = o.retained
				o.mu.Unlock()
			}
			if input.ProviderID == "" {
				input.ProviderID = "plugin:OwnedAuth"
			}
			if input.UserID == 0 {
				input.UserID = 1
			}
			ticket, createErr := plugins.CreateAuthTicket(ctx, caller, input.UserID, input.ProviderID, json.RawMessage(`{"proof":"owned-hmac"}`))
			if createErr == nil {
				status = 200
				body = map[string]any{"ticket": ticket, "pid": os.Getpid()}
			} else {
				status = 403
				body["denied"] = true
			}
			if request.Key == "authenticate" {
				o.mu.Lock()
				o.retained = host
				o.mu.Unlock()
			}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: status, Body: encoded, Headers: map[string]string{"Content-Type": "application/json"}})
	}
	return nil, errors.New("owned method unsupported")
}

func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, (&object{}).handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
