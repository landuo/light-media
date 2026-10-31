// Owned protocol verification only. Material IO is exclusively the actual
// standard HostCaller/LocalTorrentBackup SDK. No OS material reads, downloader,
// network client, business transfer, source imports or file deletion live here.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"light-media/internal/plugins"
)

var fixtureDenied = "false"

func fixtureManifest(denied bool) plugins.Manifest {
	id, entry := "OwnedLocalTorrentBackup", "owned-local-torrent-backup-sdk-fixture"
	capabilities := []string{plugins.LocalTorrentBackupCapability}
	if denied {
		id, entry = "OwnedLocalTorrentBackupDenied", "owned-local-torrent-backup-sdk-fixture-no-cap"
		capabilities = []string{}
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: id, Name: "Owned local torrent backup SDK fixture", Version: "1.0",
		Runtime: "go-executable", Entry: entry, HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
		Capabilities: capabilities,
		APIs:         []plugins.HTTPAPI{{Key: "backup", Path: "/backup", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type fixtureInput struct {
	Requests        []plugins.LocalTorrentBackupRequest `json:"requests"`
	ContinueOnError bool                                `json:"continue_on_error"`
	ProbePrevious   bool                                `json:"probe_previous"`
}

type materialObservation struct {
	Skipped     bool    `json:"skipped"`
	Temporary   bool    `json:"temporary"`
	Length      *int    `json:"length,omitempty"`
	SHA256      *string `json:"sha256,omitempty"`
	SmallBase64 *string `json:"small_base64,omitempty"`
}

type hostCallCounts struct {
	Total   int `json:"total"`
	Prepare int `json:"prepare"`
	Read    int `json:"read"`
	Release int `json:"release"`
}

type materialAttempt struct {
	Index     int                  `json:"index"`
	OK        bool                 `json:"ok"`
	Result    *materialObservation `json:"result,omitempty"`
	Error     string               `json:"error,omitempty"`
	ErrorCode string               `json:"error_code,omitempty"`
	HostCalls hostCallCounts       `json:"host_calls"`
}

type fixtureSnapshot struct {
	PID                       int               `json:"pid"`
	InitCount                 int               `json:"init_count"`
	Invocation                int               `json:"invocation"`
	HadPrevious               bool              `json:"had_previous"`
	StaleDenied               *bool             `json:"stale_denied,omitempty"`
	PriorReleasedHandleDenied *bool             `json:"prior_released_handle_denied,omitempty"`
	Requested                 int               `json:"requested"`
	Completed                 int               `json:"completed"`
	Succeeded                 int               `json:"succeeded"`
	Failed                    int               `json:"failed"`
	OK                        bool              `json:"ok"`
	Interrupted               string            `json:"interrupted,omitempty"`
	ReassembledBytes          int64             `json:"reassembled_bytes"`
	Results                   []materialAttempt `json:"results"`
}

type fixtureObject struct {
	mu                    sync.Mutex
	invocation, initCount int
	previous              plugins.HostCaller
	lastSuccessful        *plugins.LocalTorrentBackupRequest
	lastReleasedHandle    string
}

func decodeFixtureInput(raw json.RawMessage) (fixtureInput, error) {
	var input fixtureInput
	if len(raw) == 0 || len(raw) > plugins.MaxPayloadBytes {
		return input, errors.New("invalid owned backup input size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		return input, err
	}
	if d.Decode(new(any)) != io.EOF {
		return input, errors.New("trailing owned backup input")
	}
	if len(input.Requests) < 1 || len(input.Requests) > 2 {
		return input, errors.New("owned backup fixture requires one or two requests")
	}
	return input, nil
}

func observeMaterial(value plugins.LocalTorrentBackupResult) (*materialObservation, error) {
	out := &materialObservation{Skipped: value.Skipped, Temporary: value.Temporary}
	if value.Skipped {
		if value.Content != nil || value.Temporary {
			return nil, errors.New("SDK mixed skip and material")
		}
		return out, nil
	}
	if value.Content == nil {
		return nil, errors.New("SDK successful bytes became nil")
	}
	// Hash the complete slice returned AFTER actual SDK page reads and release,
	// not the host prepare metadata or a guessed digest/size.
	length := len(value.Content)
	digest := sha256.Sum256(value.Content)
	sum := hex.EncodeToString(digest[:])
	out.Length, out.SHA256 = &length, &sum
	if length <= 4096 {
		encoded := base64.StdEncoding.EncodeToString(value.Content)
		out.SmallBase64 = &encoded
	}
	return out, nil
}

func runMaterial(ctx context.Context, host plugins.HostCaller, index int, input plugins.LocalTorrentBackupRequest) (materialAttempt, string) {
	attempt := materialAttempt{Index: index}
	if host == nil {
		attempt.Error = "owned backup fixture requires an active host caller"
		return attempt, ""
	}
	allocated, released := "", false
	counted := func(callCtx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != plugins.LocalTorrentBackupCapability {
			return nil, errors.New("unexpected owned capability")
		}
		attempt.HostCalls.Total++
		var operation struct {
			Op     string `json:"op"`
			Handle string `json:"handle"`
		}
		if json.Unmarshal(raw, &operation) != nil {
			return nil, errors.New("invalid SDK frame")
		}
		switch operation.Op {
		case "prepare":
			attempt.HostCalls.Prepare++
		case "read":
			attempt.HostCalls.Read++
		case "release":
			attempt.HostCalls.Release++
		}
		response, err := host(callCtx, method, raw)
		if err == nil && operation.Op == "prepare" {
			var value struct {
				Handle string `json:"handle"`
			}
			if json.Unmarshal(response, &value) == nil {
				allocated = value.Handle
			}
		}
		if err == nil && operation.Op == "release" && allocated != "" && operation.Handle == allocated {
			var receipt struct {
				Released bool `json:"released"`
			}
			released = json.Unmarshal(response, &receipt) == nil && receipt.Released
		}
		return response, err
	}
	value, err := (plugins.LocalTorrentBackup{Host: counted}).Prepare(ctx, input)
	if err == nil {
		attempt.Result, err = observeMaterial(value)
	}
	if err != nil {
		attempt.Error = err.Error()
		var protocol *plugins.Error
		if errors.As(err, &protocol) {
			attempt.ErrorCode = protocol.Code
		}
		attempt.Result = nil
		return attempt, ""
	}
	attempt.OK = true
	if !released {
		allocated = ""
	}
	return attempt, allocated
}

func (o *fixtureObject) snapshot(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (fixtureSnapshot, error) {
	input, err := decodeFixtureInput(raw)
	if err != nil {
		return fixtureSnapshot{}, err
	}
	o.mu.Lock()
	previous, lastSuccessful, lastReleased := o.previous, o.lastSuccessful, o.lastReleasedHandle
	o.previous = host
	o.invocation++
	sequence, initCount := o.invocation, o.initCount
	o.mu.Unlock()
	out := fixtureSnapshot{PID: os.Getpid(), InitCount: initCount, Invocation: sequence, HadPrevious: previous != nil,
		Requested: len(input.Requests), OK: true, Results: []materialAttempt{}}
	if input.ProbePrevious && previous != nil && lastSuccessful != nil {
		// The prior successful request is owned test input, not a synthetic
		// denied argument. If an expired callback were incorrectly accepted,
		// its actual operation would be observable (and this check would fail).
		_, err := (plugins.LocalTorrentBackup{Host: previous}).Prepare(ctx, *lastSuccessful)
		denied := err != nil
		out.StaleDenied = &denied
		if !denied {
			out.OK = false
		}
	}
	if input.ProbePrevious && lastReleased != "" {
		// Separate, precisely labelled assertion: this is a previously released
		// real handle, not proof about an intentionally leaked live transfer.
		probe, _ := json.Marshal(map[string]any{"op": "read", "handle": lastReleased, "offset": 0})
		_, err := host(ctx, plugins.LocalTorrentBackupCapability, probe)
		denied := err != nil
		out.PriorReleasedHandleDenied = &denied
		if !denied {
			out.OK = false
		}
	}
	for index, request := range input.Requests {
		attempt, handle := runMaterial(ctx, host, index, request)
		out.Results = append(out.Results, attempt)
		out.Completed++
		if attempt.OK {
			out.Succeeded++
			if attempt.Result.Length != nil {
				out.ReassembledBytes += int64(*attempt.Result.Length)
			}
			o.mu.Lock()
			selected := request
			o.lastSuccessful = &selected
			if handle != "" {
				o.lastReleasedHandle = handle
			}
			o.mu.Unlock()
		} else {
			out.Failed++
			out.OK = false
			if !input.ContinueOnError {
				break
			}
		}
		if ctx.Err() != nil {
			out.OK = false
			out.Interrupted = ctx.Err().Error()
			break
		}
	}
	if out.Completed != out.Requested {
		out.OK = false
	}
	return out, nil
}

func (o *fixtureObject) handle(manifest plugins.Manifest) plugins.Handler {
	return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		httpCall := method == "http"
		if httpCall {
			var request plugins.HTTPRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Key != "backup" || request.Method != "POST" || request.Path != "/backup" {
				return nil, errors.New("unsupported owned backup HTTP route")
			}
			method, raw = "snapshot", request.Body
		}
		switch method {
		case "init":
			o.mu.Lock()
			o.initCount++
			o.mu.Unlock()
			return json.RawMessage(`null`), nil
		case "close", "stop_service", "stop":
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
		case "snapshot":
			out, err := o.snapshot(ctx, raw, host)
			if err != nil {
				return nil, err
			}
			if httpCall {
				return plugins.JSONHTTPResponse(200, out)
			}
			return json.Marshal(out)
		default:
			return nil, errors.New("unsupported owned backup fixture method")
		}
	}
}

func main() {
	if fixtureDenied != "false" && fixtureDenied != "true" {
		os.Exit(80)
	}
	manifest := fixtureManifest(fixtureDenied == "true")
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if json.NewEncoder(os.Stdout).Encode(manifest) != nil {
			os.Exit(81)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(80)
	}
	object := &fixtureObject{}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, object.handle(manifest)) != nil {
		fmt.Fprintln(os.Stderr, "owned local backup SDK stopped with an error")
		os.Exit(82)
	}
}
