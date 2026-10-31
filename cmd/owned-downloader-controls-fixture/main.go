// Owned synthetic S13 four-command verification only. Actual ordinary SDK
// HostCaller, fixed owned downloader identities and finite synthetic values.
// No network client, file materials, environment, arbitrary RPC or real tasks.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"unicode/utf8"

	"light-media/internal/plugins"
)

const fixtureQBID = "owned-controls-qb"
const fixtureTRID = "owned-controls-tr"
const staleCategoryFrame = `{"op":"invoke","downloader_id":"owned-controls-qb","hash_string":"","category":null}`

func fixtureManifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedDownloaderControls", Name: "Owned four downloader commands SDK fixture", Version: "1.0",
		Runtime: "go-executable", Entry: "owned-downloader-controls-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
		Capabilities: []string{plugins.QBittorrentSetTagsCapability, plugins.QBittorrentSetCategoryCapability, plugins.QBittorrentStopCapability, plugins.TransmissionStopCapability},
		APIs:         []plugins.HTTPAPI{{Key: "controls", Path: "/controls", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type fixtureOperation struct {
	Kind, Op, DownloaderID          string
	IDs, Tags, HashString, Category any
}
type fixtureInput struct {
	Operations                     []fixtureOperation
	ContinueOnError, ProbePrevious bool
}

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

// This is an owned fixture domain, not a new product/source selector restriction.
func ownedValue(raw json.RawMessage, allowed ...string) (any, error) {
	value, err := jsonValue(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("invalid owned fixed value")
	}
	for _, candidate := range allowed {
		if string(canonical) == candidate {
			return value, nil
		}
	}
	return nil, errors.New("outside the finite owned fixture values")
}

func decodeOperation(raw json.RawMessage) (fixtureOperation, error) {
	var out fixtureOperation
	fields, err := exactObject(raw, "kind", "op", "downloader_id", "ids", "tags", "hash_string", "category")
	if err != nil {
		return out, err
	}
	if out.Kind, err = requiredString(fields, "kind"); err != nil {
		return out, err
	}
	if out.Op, err = requiredString(fields, "op"); err != nil {
		return out, err
	}
	if out.DownloaderID, err = requiredString(fields, "downloader_id"); err != nil {
		return out, err
	}
	if !(out.Kind == "qbittorrent" && out.DownloaderID == fixtureQBID || out.Kind == "transmission" && out.DownloaderID == fixtureTRID) {
		return out, errors.New("requires exact owned downloader kind and identity")
	}
	if out.Kind == "transmission" && out.Op != "stop" {
		return out, errors.New("unselected Transmission operation")
	}
	switch out.Op {
	case "set_tags", "stop":
		count := 4
		if out.Op == "set_tags" {
			count = 5
		}
		if len(fields) != count || fields["hash_string"] != nil || fields["category"] != nil || out.Op == "stop" && fields["tags"] != nil {
			return out, errors.New("unexpected owned control argument")
		}
		if out.Kind == "qbittorrent" {
			out.IDs, err = ownedValue(fields["ids"], `null`, `""`, `"owned-a"`, `"owned-fail"`, `"owned-a|owned-a"`, `[]`, `["owned-a"]`, `["owned-a","owned-a"]`)
		} else {
			out.IDs, err = ownedValue(fields["ids"], `null`, `[]`, `7`, `[7]`, `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `[0,"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]`)
		}
		if err != nil {
			return out, err
		}
		if out.Op == "set_tags" {
			out.Tags, err = ownedValue(fields["tags"], `null`, `[]`, `"owned-label"`, `["owned-label","owned-label"]`, `["owned-fail"]`)
			if err != nil {
				return out, err
			}
		}
	case "set_category":
		if len(fields) != 5 || fields["ids"] != nil || fields["tags"] != nil {
			return out, errors.New("unexpected owned category argument")
		}
		out.HashString, err = ownedValue(fields["hash_string"], `null`, `""`, `"owned-a"`, `"owned-fail"`)
		if err != nil {
			return out, err
		}
		out.Category, err = ownedValue(fields["category"], `null`, `""`, `"owned-category"`, `false`)
		if err != nil {
			return out, err
		}
	default:
		return out, errors.New("unsupported owned four-command operation")
	}
	return out, nil
}

func decodeFixtureInput(raw json.RawMessage) (fixtureInput, error) {
	var out fixtureInput
	if len(raw) == 0 || len(raw) > 16<<10 {
		return out, errors.New("invalid owned four-command input size")
	}
	fields, err := exactObject(raw, "operations", "continue_on_error", "probe_previous")
	if err != nil {
		return out, err
	}
	var operations []json.RawMessage
	if json.Unmarshal(fields["operations"], &operations) != nil || len(operations) < 1 || len(operations) > 8 {
		return out, errors.New("requires one through eight owned operations")
	}
	if out.ContinueOnError, err = optionalBool(fields, "continue_on_error"); err != nil {
		return out, err
	}
	if out.ProbePrevious, err = optionalBool(fields, "probe_previous"); err != nil {
		return out, err
	}
	for _, rawOp := range operations {
		op, err := decodeOperation(rawOp)
		if err != nil {
			return out, err
		}
		out.Operations = append(out.Operations, op)
	}
	return out, nil
}

type hostCallCounts struct {
	Total           int `json:"total"`
	Succeeded       int `json:"succeeded"`
	Failed          int `json:"failed"`
	InputJSONBytes  int `json:"input_json_bytes"`
	OutputJSONBytes int `json:"output_json_bytes"`
}
type controlAttempt struct {
	Index        int             `json:"index"`
	Kind         string          `json:"kind"`
	Op           string          `json:"op"`
	DownloaderID string          `json:"downloader_id"`
	OK           bool            `json:"ok"`
	Result       json.RawMessage `json:"result,omitempty"`
	ResultKind   string          `json:"result_kind,omitempty"`
	SourceTruthy *bool           `json:"source_truthy,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	HostCalls    hostCallCounts  `json:"host_calls"`
}
type previousProbe struct {
	Label     string         `json:"label"`
	Available bool           `json:"available"`
	Denied    *bool          `json:"denied,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	HostCalls hostCallCounts `json:"host_calls"`
}
type fixtureSnapshot struct {
	PID           int              `json:"pid"`
	InitCount     int              `json:"init_count"`
	Invocation    int              `json:"invocation"`
	Requested     int              `json:"requested"`
	Completed     int              `json:"completed"`
	Succeeded     int              `json:"succeeded"`
	Failed        int              `json:"failed"`
	OK            bool             `json:"ok"`
	Interrupted   string           `json:"interrupted,omitempty"`
	PreviousProbe *previousProbe   `json:"previous_probe,omitempty"`
	Results       []controlAttempt `json:"results"`
}
type fixtureObject struct {
	mu                    sync.Mutex
	invocation, initCount int
	previous              plugins.HostCaller
}

func countedCall(ctx context.Context, host plugins.HostCaller, method string, raw json.RawMessage, counts *hostCallCounts) (json.RawMessage, error) {
	if host == nil {
		return nil, errors.New("owned controls require current host caller")
	}
	counts.Total++
	counts.InputJSONBytes += len(raw)
	response, err := host(ctx, method, raw)
	counts.OutputJSONBytes += len(response)
	if err == nil {
		counts.Succeeded++
	} else {
		counts.Failed++
	}
	return response, err
}

func dispatchControl(ctx context.Context, host plugins.HostCaller, index int, input fixtureOperation) controlAttempt {
	out := controlAttempt{Index: index, Kind: input.Kind, Op: input.Op, DownloaderID: input.DownloaderID}
	capability := "downloader." + input.Kind + "." + input.Op
	counted := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != capability {
			return nil, errors.New("unexpected fixed SDK capability")
		}
		return countedCall(ctx, host, method, raw, &out.HostCalls)
	}
	q := plugins.SharedQBittorrentCommands{Host: counted, DownloaderID: input.DownloaderID}
	tr := plugins.SharedTransmissionCommands{Host: counted, DownloaderID: input.DownloaderID}
	var err error
	var result bool
	switch input.Op {
	case "set_tags":
		err = q.SetTorrentsTag(ctx, input.IDs, input.Tags)
		if err == nil {
			out.Result = json.RawMessage(`null`)
			out.ResultKind = "source_none_not_mutation_success"
			sourceTruthy := false
			out.SourceTruthy = &sourceTruthy
		}
	case "set_category":
		result, err = q.SetTorrentCategory(ctx, input.HashString, input.Category)
	case "stop":
		if input.Kind == "qbittorrent" {
			result, err = q.StopTorrents(ctx, input.IDs)
		} else {
			result, err = tr.StopTorrents(ctx, input.IDs)
		}
	default:
		err = errors.New("unselected owned SDK operation")
	}
	if err == nil && input.Op != "set_tags" {
		out.Result, err = json.Marshal(result)
		out.ResultKind = "source_bool"
		out.SourceTruthy = &result
	}
	if err != nil {
		out.ErrorCode = safeErrorCode(err)
		out.Result, out.SourceTruthy, out.ResultKind = nil, nil, ""
		return out
	}
	// OK is a completed typed SDK return, not a successful mutation. In
	// particular source None/False are returned separately, never coerced true.
	out.OK = true
	return out
}

func (o *fixtureObject) snapshot(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (fixtureSnapshot, error) {
	input, err := decodeFixtureInput(raw)
	if err != nil {
		return fixtureSnapshot{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	previous := o.previous
	o.previous = host
	o.invocation++
	out := fixtureSnapshot{PID: os.Getpid(), InitCount: o.initCount, Invocation: o.invocation, Requested: len(input.Operations), OK: true, Results: []controlAttempt{}}
	if input.ProbePrevious {
		probe := &previousProbe{Label: "previous_invocation_category_empty_hash_caller_denied", Available: previous != nil}
		out.PreviousProbe = probe
		if probe.Available {
			// One fixed, source-no-op empty-hash category on the OLD identity;
			// never replay a captured tag/stop mutation or a live upload token.
			response, probeErr := countedCall(ctx, previous, plugins.QBittorrentSetCategoryCapability, json.RawMessage(staleCategoryFrame), &probe.HostCalls)
			probe.ErrorCode = safeErrorCode(probeErr)
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
		attempt := dispatchControl(ctx, host, index, operation)
		out.Results = append(out.Results, attempt)
		out.Completed++
		if attempt.OK {
			out.Succeeded++
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
			if json.Unmarshal(raw, &request) != nil || request.Key != "controls" || request.Method != "POST" || request.Path != "/controls" {
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
		fmt.Fprintln(os.Stderr, "owned four downloader commands SDK stopped with an error")
		os.Exit(82)
	}
}
