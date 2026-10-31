// Owned synthetic protocol verification only. All downloader operations go
// through the actual ordinary SDK HostCaller. There is no network client,
// filesystem material read, arbitrary RPC, real download, or delete-data option.
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
	"unicode/utf8"

	"light-media/internal/plugins"
)

const (
	fixtureQBID          = "owned-command-qb"
	fixtureTRID          = "owned-command-tr"
	fixtureInputLimit    = 128 << 10
	fixtureMaterialLimit = 16 << 20
)

func fixtureManifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedDownloaderCommands", Name: "Owned downloader commands SDK fixture", Version: "1.0",
		Runtime: "go-executable", Entry: "owned-downloader-command-sdk-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
		Capabilities: []string{
			plugins.QBittorrentAddCapability, plugins.QBittorrentStartCapability, plugins.QBittorrentRecheckCapability, plugins.QBittorrentDeleteKeepFilesCapability,
			plugins.TransmissionAddCapability, plugins.TransmissionStartCapability, plugins.TransmissionRecheckCapability, plugins.TransmissionDeleteKeepFilesCapability,
		},
		APIs: []plugins.HTTPAPI{{Key: "commands", Path: "/commands", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type fixtureOperation struct {
	Kind            string
	Op              string
	DownloaderID    string
	Args            map[string]any
	IDs             any
	SyntheticLength *int
}

type fixtureInput struct {
	Operations      []fixtureOperation
	ContinueOnError bool
	ProbePrevious   bool
}

// Exact object keys, including duplicate and case-alias rejection. Errors never
// repeat caller values, which may contain ordinary source cookie arguments.
func exactObject(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	invalid := errors.New("invalid owned command object")
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	open, err := d.Token()
	if err != nil || open != json.Delim('{') {
		return nil, invalid
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok {
			return nil, invalid
		}
		permitted := false
		for _, candidate := range allowed {
			permitted = permitted || name == candidate
		}
		if !permitted {
			return nil, invalid
		}
		if _, exists := out[name]; exists {
			return nil, invalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, invalid
		}
		out[name] = value
	}
	close, err := d.Token()
	if err != nil || close != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	return out, nil
}

func jsonValue(raw json.RawMessage) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid owned command JSON value")
	}
	return value, nil
}

func requiredString(fields map[string]json.RawMessage, name string) (string, error) {
	var value string
	raw, present := fields[name]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return "", errors.New("missing or invalid owned command selector")
	}
	return value, nil
}

func optionalBool(fields map[string]json.RawMessage, name string) (bool, error) {
	raw, present := fields[name]
	if !present {
		return false, nil
	}
	var value bool
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return false, errors.New("invalid owned command flag")
	}
	return value, nil
}

func decodeOperation(raw json.RawMessage) (fixtureOperation, error) {
	var out fixtureOperation
	fields, err := exactObject(raw, "kind", "op", "downloader_id", "args", "ids", "synthetic_content")
	if err != nil {
		return out, err
	}
	out.Kind, err = requiredString(fields, "kind")
	if err != nil {
		return out, err
	}
	out.Op, err = requiredString(fields, "op")
	if err != nil {
		return out, err
	}
	out.DownloaderID, err = requiredString(fields, "downloader_id")
	if err != nil {
		return out, err
	}
	if !(out.Kind == "qbittorrent" && out.DownloaderID == fixtureQBID || out.Kind == "transmission" && out.DownloaderID == fixtureTRID) {
		return out, errors.New("owned command fixture requires its exact owned kind and downloader ID")
	}
	switch out.Op {
	case "add":
		if _, exists := fields["ids"]; exists {
			return out, errors.New("owned add does not accept ids")
		}
		value, err := jsonValue(fields["args"])
		if err != nil {
			return out, err
		}
		var object bool
		out.Args, object = value.(map[string]any)
		if !object {
			return out, errors.New("owned add requires an ordinary JSON args object")
		}
		if generated, exists := fields["synthetic_content"]; exists {
			if _, conflict := out.Args["content"]; conflict {
				return out, errors.New("synthetic content cannot replace an explicit content argument")
			}
			params, err := exactObject(generated, "length")
			if err != nil {
				return out, err
			}
			rawLength, present := params["length"]
			var length int
			if !present || bytes.Equal(bytes.TrimSpace(rawLength), []byte("null")) || json.Unmarshal(rawLength, &length) != nil || length < 0 || length > fixtureMaterialLimit {
				return out, errors.New("synthetic content length must be between zero and 16 MiB")
			}
			out.SyntheticLength = &length
		}
	case "start", "recheck", "delete_keep_files":
		_, hasArgs := fields["args"]
		_, hasSynthetic := fields["synthetic_content"]
		if hasArgs || hasSynthetic {
			return out, errors.New("owned control accepts ids only")
		}
		out.IDs, err = jsonValue(fields["ids"])
		if err != nil {
			return out, err
		}
	default:
		return out, errors.New("unsupported owned command operation")
	}
	return out, nil
}

func decodeFixtureInput(raw json.RawMessage) (fixtureInput, error) {
	var out fixtureInput
	if len(raw) == 0 || len(raw) > fixtureInputLimit {
		return out, errors.New("invalid owned command input size")
	}
	fields, err := exactObject(raw, "operations", "continue_on_error", "probe_previous")
	if err != nil {
		return out, err
	}
	var operations []json.RawMessage
	if json.Unmarshal(fields["operations"], &operations) != nil || len(operations) < 1 || len(operations) > 8 {
		return out, errors.New("owned command fixture requires one through eight operations")
	}
	out.ContinueOnError, err = optionalBool(fields, "continue_on_error")
	if err != nil {
		return out, err
	}
	out.ProbePrevious, err = optionalBool(fields, "probe_previous")
	if err != nil {
		return out, err
	}
	for _, rawOperation := range operations {
		operation, err := decodeOperation(rawOperation)
		if err != nil {
			return out, err
		}
		out.Operations = append(out.Operations, operation)
	}
	return out, nil
}

type materialObservation struct {
	Kind   string `json:"kind"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

type hostCallCounts struct {
	Total              int   `json:"total"`
	Begin              int   `json:"begin"`
	Append             int   `json:"append"`
	Submit             int   `json:"submit"`
	Abort              int   `json:"abort"`
	Invoke             int   `json:"invoke"`
	Other              int   `json:"other"`
	Succeeded          int   `json:"succeeded"`
	Failed             int   `json:"failed"`
	InputJSONBytes     int64 `json:"input_json_bytes"`
	OutputJSONBytes    int64 `json:"output_json_bytes"`
	AppendPayloadBytes int64 `json:"append_payload_bytes"`
	MaxInputJSONBytes  int   `json:"max_input_json_bytes"`
}

type commandAttempt struct {
	Index        int                  `json:"index"`
	Kind         string               `json:"kind"`
	Op           string               `json:"op"`
	DownloaderID string               `json:"downloader_id"`
	OK           bool                 `json:"ok"`
	Result       json.RawMessage      `json:"result,omitempty"`
	ResultKind   string               `json:"result_kind,omitempty"`
	SourceTruthy *bool                `json:"source_truthy,omitempty"`
	Generated    *materialObservation `json:"generated,omitempty"`
	ErrorCode    string               `json:"error_code,omitempty"`
	HostCalls    hostCallCounts       `json:"host_calls"`
}

// Captured payloads remain private, are never serialized into a report, and are
// retained only for the next invocation's explicit one-call expiration probe.
type successfulFrame struct {
	Method    string
	Payload   json.RawMessage
	Operation string
}

type priorSuccess struct {
	Input fixtureOperation
	Frame successfulFrame
}

type previousProbe struct {
	Label                 string `json:"label"`
	Available             bool   `json:"available"`
	SourceInvocation      int    `json:"source_invocation,omitempty"`
	Kind                  string `json:"kind,omitempty"`
	Operation             string `json:"operation,omitempty"`
	PrivateFrameOperation string `json:"private_frame_operation,omitempty"`
	CallbackAttempts      int    `json:"callback_attempts"`
	InputJSONBytes        int    `json:"input_json_bytes"`
	OutputJSONBytes       int    `json:"output_json_bytes"`
	Denied                *bool  `json:"denied,omitempty"`
	ErrorCode             string `json:"error_code,omitempty"`
}

type fixtureSnapshot struct {
	PID           int              `json:"pid"`
	InitCount     int              `json:"init_count"`
	Invocation    int              `json:"invocation"`
	HadPrevious   bool             `json:"had_previous"`
	PreviousProbe *previousProbe   `json:"previous_probe,omitempty"`
	Requested     int              `json:"requested"`
	Completed     int              `json:"completed"`
	Succeeded     int              `json:"succeeded"`
	Failed        int              `json:"failed"`
	OK            bool             `json:"ok"`
	Interrupted   string           `json:"interrupted,omitempty"`
	Results       []commandAttempt `json:"results"`
}

type fixtureObject struct {
	mu                    sync.Mutex
	invocation, initCount int
	previous              plugins.HostCaller
	lastSuccessful        *priorSuccess
}

func safeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded"
	}
	var protocol *plugins.Error
	if errors.As(err, &protocol) && len(protocol.Code) > 0 && len(protocol.Code) <= 96 {
		valid := true
		for _, c := range protocol.Code {
			valid = valid && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_')
		}
		if valid {
			return protocol.Code
		}
	}
	// Do not echo Error(): source/vendor errors may contain input or credentials.
	return "owned_command_operation_failed"
}

func observeHostCall(counts *hostCallCounts, raw json.RawMessage) string {
	counts.Total++
	counts.InputJSONBytes += int64(len(raw))
	counts.MaxInputJSONBytes = max(counts.MaxInputJSONBytes, len(raw))
	var frame struct {
		Op   string `json:"op"`
		Data string `json:"data"`
	}
	if json.Unmarshal(raw, &frame) != nil {
		counts.Other++
		return "unrecognized"
	}
	switch frame.Op {
	case "upload_begin":
		counts.Begin++
	case "upload_append":
		counts.Append++
		if content, err := base64.StdEncoding.Strict().DecodeString(frame.Data); err == nil {
			counts.AppendPayloadBytes += int64(len(content))
		}
	case "upload_submit":
		counts.Submit++
	case "upload_abort":
		counts.Abort++
	case "invoke":
		counts.Invoke++
	default:
		counts.Other++
	}
	return frame.Op
}

func generatedArgs(input fixtureOperation) (map[string]any, *materialObservation) {
	args := make(map[string]any, len(input.Args)+1)
	for key, value := range input.Args {
		args[key] = value
	}
	if input.SyntheticLength == nil {
		return args, nil
	}
	// make(..., 0) is deliberately non-nil: typed empty bytes are not None.
	content := make([]byte, *input.SyntheticLength)
	for index := range content {
		content[index] = byte(index % 251)
	}
	digest := sha256.Sum256(content)
	args["content"] = content
	return args, &materialObservation{Kind: "deterministic_bytes_i_mod_251", Length: len(content), SHA256: hex.EncodeToString(digest[:])}
}

type commandControls interface {
	StartTorrents(context.Context, any) (bool, error)
	RecheckTorrents(context.Context, any) (bool, error)
	DeleteTorrentsKeepFiles(context.Context, any) (bool, error)
}

func dispatchCommand(ctx context.Context, host plugins.HostCaller, index int, input fixtureOperation) (commandAttempt, *priorSuccess) {
	attempt := commandAttempt{Index: index, Kind: input.Kind, Op: input.Op, DownloaderID: input.DownloaderID}
	if host == nil {
		attempt.ErrorCode = "owned_command_missing_host"
		return attempt, nil
	}
	capability := "downloader." + input.Kind + "." + input.Op
	var first *successfulFrame
	counted := func(callCtx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != capability {
			return nil, errors.New("unexpected owned command capability")
		}
		op := observeHostCall(&attempt.HostCalls, raw)
		response, err := host(callCtx, method, raw)
		attempt.HostCalls.OutputJSONBytes += int64(len(response))
		if err == nil {
			attempt.HostCalls.Succeeded++
			if first == nil {
				first = &successfulFrame{Method: method, Payload: bytes.Clone(raw), Operation: op}
			}
		} else {
			attempt.HostCalls.Failed++
		}
		return response, err
	}
	qb := plugins.SharedQBittorrentCommands{Host: counted, DownloaderID: input.DownloaderID}
	tr := plugins.SharedTransmissionCommands{Host: counted, DownloaderID: input.DownloaderID}
	var err error
	knownSuccessful := false
	if input.Op == "add" {
		args, generated := generatedArgs(input)
		attempt.Generated = generated
		if input.Kind == "qbittorrent" {
			var result plugins.QBittorrentAddResult
			result, err = qb.AddTorrent(ctx, plugins.DownloaderCommandArgs(args))
			if err == nil {
				attempt.Result, err = json.Marshal(struct {
					Success bool     `json:"success"`
					IDs     []string `json:"ids"`
				}{result.Success, result.IDs})
				truthy := result.SourceTruthy()
				attempt.SourceTruthy = &truthy
				attempt.ResultKind = "qbittorrent_source_tuple"
				knownSuccessful = result.Success
			}
		} else {
			var result plugins.TransmissionAddResult
			result, err = tr.AddTorrent(ctx, plugins.DownloaderCommandArgs(args))
			if err == nil {
				attempt.Result = bytes.Clone(result.Value)
				attempt.ResultKind = "transmission_native_json_not_python_torrent"
				// Retain only an actually returned native object, not source None
				// or an arbitrary false/number/string sentinel, for the probe.
				value := bytes.TrimSpace(result.Value)
				knownSuccessful = len(value) > 0 && value[0] == '{'
			}
		}
	} else {
		var controls commandControls = qb
		if input.Kind == "transmission" {
			controls = tr
		}
		var result bool
		switch input.Op {
		case "start":
			result, err = controls.StartTorrents(ctx, input.IDs)
		case "recheck":
			result, err = controls.RecheckTorrents(ctx, input.IDs)
		case "delete_keep_files":
			result, err = controls.DeleteTorrentsKeepFiles(ctx, input.IDs)
		default:
			err = errors.New("unsupported owned command operation")
		}
		if err == nil {
			attempt.Result, err = json.Marshal(result)
			attempt.ResultKind = "source_bool"
			knownSuccessful = result
		}
	}
	if err != nil {
		attempt.ErrorCode = safeErrorCode(err)
		attempt.Result, attempt.SourceTruthy, attempt.ResultKind = nil, nil, ""
		return attempt, nil
	}
	// OK means a completed SDK return, NOT that a false/None source result
	// represents a successful downloader mutation. Report the result separately.
	attempt.OK = true
	if knownSuccessful && first != nil {
		return attempt, &priorSuccess{Input: input, Frame: *first}
	}
	return attempt, nil
}

func (o *fixtureObject) snapshot(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (fixtureSnapshot, error) {
	input, err := decodeFixtureInput(raw)
	if err != nil {
		return fixtureSnapshot{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	previous, lastSuccessful := o.previous, o.lastSuccessful
	o.previous, o.lastSuccessful = host, nil
	o.invocation++
	out := fixtureSnapshot{PID: os.Getpid(), InitCount: o.initCount, Invocation: o.invocation, HadPrevious: previous != nil, Requested: len(input.Operations), OK: true, Results: []commandAttempt{}}
	if input.ProbePrevious {
		probe := &previousProbe{Label: "previous_invocation_host_caller_denied", Available: previous != nil && lastSuccessful != nil}
		out.PreviousProbe = probe
		if probe.Available {
			probe.SourceInvocation, probe.Kind, probe.Operation = o.invocation-1, lastSuccessful.Input.Kind, lastSuccessful.Input.Op
			probe.PrivateFrameOperation = lastSuccessful.Frame.Operation
			probe.CallbackAttempts = 1
			probe.InputJSONBytes = len(lastSuccessful.Frame.Payload)
			// Exactly one captured, previously successful frame on the OLD caller.
			// For byte adds this is upload_begin, NOT submit or live-token proof.
			// No retry even if an expiration bug accepts the owned synthetic call.
			response, probeErr := previous(ctx, lastSuccessful.Frame.Method, lastSuccessful.Frame.Payload)
			probe.OutputJSONBytes, probe.ErrorCode = len(response), safeErrorCode(probeErr)
			denied := probeErr != nil && len(response) == 0 && (probe.ErrorCode == "invalid_host_call" || probe.ErrorCode == "PLUGIN_CALL_CLOSED")
			probe.Denied = &denied
			if !denied {
				out.OK = false
			}
		}
	}
	for index, operation := range input.Operations {
		if err := ctx.Err(); err != nil {
			out.Interrupted, out.OK = safeErrorCode(err), false
			break
		}
		attempt, successful := dispatchCommand(ctx, host, index, operation)
		out.Results = append(out.Results, attempt)
		out.Completed++
		if attempt.OK {
			out.Succeeded++
			if successful != nil {
				o.lastSuccessful = successful
			}
		} else {
			out.Failed++
			out.OK = false
			if !input.ContinueOnError {
				break
			}
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
			if json.Unmarshal(raw, &request) != nil || request.Key != "commands" || request.Method != "POST" || request.Path != "/commands" {
				return nil, errors.New("unsupported owned command HTTP route")
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
			return nil, errors.New("unsupported owned command fixture method")
		}
	}
}

func main() {
	manifest := fixtureManifest()
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
		fmt.Fprintln(os.Stderr, "owned downloader commands SDK stopped with an error")
		os.Exit(82)
	}
}
