// Owned protocol verification only. No real site, OCR engine, browser or
// AutoSignIn business logic is included in this independently built worker.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"os"
)

func main() {
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedOCR", Name: "Owned OCR wrapper", Description: "Synthetic protocol verification only", Version: "1.0", Runtime: "go-executable", Entry: "owned-ocr-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "query", "http", "stop_service", "stop"}, Capabilities: []string{plugins.SharedOCRGetCaptchaText}, APIs: []plugins.HTTPAPI{{Key: "query", Path: "/query", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}}}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if json.NewEncoder(os.Stdout).Encode(manifest) != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(80)
	}
	var previous plugins.HostCaller
	handler := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		httpCall := method == "http"
		if httpCall {
			var request plugins.HTTPRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Key != "query" || request.Method != "POST" || request.Path != "/query" {
				return nil, errors.New("unsupported owned OCR HTTP route")
			}
			method, raw = "query", request.Body
		}
		switch method {
		case "init", "stop_service", "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command", "get_page":
			return json.RawMessage(`[]`), nil
		case "get_api":
			return json.Marshal(manifest.APIs)
		case "get_form":
			return json.RawMessage(`[[],{}]`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "query":
			var input plugins.OCRRequest
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			staleDenied := true
			if previous != nil {
				_, err := (plugins.SharedOCR{Host: previous}).GetCaptchaText(ctx, plugins.OCRRequest{})
				staleDenied = err != nil
			}
			previous = host
			value, err := (plugins.SharedOCR{Host: host}).GetCaptchaText(ctx, input)
			errorClass := ""
			if err != nil {
				var source *plugins.OCRSourceError
				if !errors.As(err, &source) {
					return nil, err
				}
				errorClass = source.Class
			}
			var resultValue *plugins.OCRValue
			if errorClass == "" {
				resultValue = &value
			}
			result, err := json.Marshal(struct {
				Value       *plugins.OCRValue `json:"value,omitempty"`
				ErrorClass  string            `json:"error_class,omitempty"`
				PID         int               `json:"pid"`
				StaleDenied bool              `json:"stale_denied"`
			}{resultValue, errorClass, os.Getpid(), staleDenied})
			if err != nil || !httpCall {
				return result, err
			}
			return plugins.JSONHTTPResponse(200, json.RawMessage(result))
		}
		return nil, errors.New("unsupported owned OCR method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handler) != nil {
		fmt.Fprintln(os.Stderr, "owned OCR SDK stopped with an error")
		os.Exit(82)
	}
}
