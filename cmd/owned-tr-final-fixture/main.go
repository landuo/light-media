// Owned synthetic S13 final TR commands fixture. Actual ordinary SDK only.
// No network/DB/client, arbitrary RPC, environment policy or worker budget change.
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

const fixtureTRID = "owned-tr-final-tr"

func fixtureManifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedTRFinal", Name: "Owned TR final commands SDK fixture", Version: "1.0",
		Runtime: "go-executable", Entry: "owned-tr-final-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
		Capabilities: []string{plugins.TransmissionSetTagsCapability, plugins.TransmissionChangeCapability},
		APIs:         []plugins.HTTPAPI{{Key: "tr_final", Path: "/tr-final", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type fixtureOperation struct {
	Kind, Op, DownloaderID                                               string
	IDs, Tags, OrgTags                                                   any
	HashString, UploadLimit, DownloadLimit, RatioLimit, SeedingTimeLimit any
}
type fixtureInput struct {
	Operations      []fixtureOperation
	ContinueOnError bool
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
	fields, err := exactObject(raw, "kind", "op", "downloader_id", "ids", "tags", "org_tags", "hash_string", "upload_limit", "download_limit", "ratio_limit", "seeding_time_limit")
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
	if out.Kind != "transmission" || out.DownloaderID != fixtureTRID {
		return out, errors.New("requires exact owned Transmission identity")
	}
	switch out.Op {
	case "set_tags":
		if len(fields) != 6 || fields["hash_string"] != nil || fields["upload_limit"] != nil || fields["download_limit"] != nil || fields["ratio_limit"] != nil || fields["seeding_time_limit"] != nil {
			return out, errors.New("unexpected owned tag fields")
		}
		if out.IDs, err = ownedValue(fields["ids"], `7`); err != nil {
			return out, err
		}
		if out.Tags, err = ownedValue(fields["tags"], `["owned-new","owned-old","owned-new"]`); err != nil {
			return out, err
		}
		out.OrgTags, err = ownedValue(fields["org_tags"], `["owned-old","owned-retained"]`, `null`)
	case "change":
		if len(fields) != 8 || fields["ids"] != nil || fields["tags"] != nil || fields["org_tags"] != nil {
			return out, errors.New("unexpected owned limit fields")
		}
		if out.HashString, err = ownedValue(fields["hash_string"], `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`); err != nil {
			return out, err
		}
		if out.UploadLimit, err = ownedValue(fields["upload_limit"], `"0"`, `"owned-invalid"`, `null`, `"1_000"`, `" +١٢٣ "`, `"1__0"`); err != nil {
			return out, err
		}
		if out.DownloadLimit, err = ownedValue(fields["download_limit"], `-4`, `null`, `"2_048"`, `"４_२　"`); err != nil {
			return out, err
		}
		if out.RatioLimit, err = ownedValue(fields["ratio_limit"], `2.675`, `null`, `"2_6.7_5"`, `" +१_२.५_०e-१ "`, `"1_2.5_0e+0_1"`); err != nil {
			return out, err
		}
		out.SeedingTimeLimit, err = ownedValue(fields["seeding_time_limit"], `0`, `null`, `"1_0"`, `"　-٠　"`)
	default:
		return out, errors.New("unsupported owned Transmission operation")
	}
	return out, err
}

func decodeFixtureInput(raw json.RawMessage) (fixtureInput, error) {
	var out fixtureInput
	if len(raw) == 0 || len(raw) > 16<<10 {
		return out, errors.New("invalid owned TR input size")
	}
	fields, err := exactObject(raw, "operations", "continue_on_error")
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
	SourceError  string          `json:"source_error,omitempty"`
	HostCalls    hostCallCounts  `json:"host_calls"`
}
type fixtureSnapshot struct {
	PID         int              `json:"pid"`
	InitCount   int              `json:"init_count"`
	Invocation  int              `json:"invocation"`
	Requested   int              `json:"requested"`
	Completed   int              `json:"completed"`
	Succeeded   int              `json:"succeeded"`
	Failed      int              `json:"failed"`
	OK          bool             `json:"ok"`
	Interrupted string           `json:"interrupted,omitempty"`
	Results     []controlAttempt `json:"results"`
}
type fixtureObject struct {
	mu                    sync.Mutex
	invocation, initCount int
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
	capability := ""
	switch input.Op {
	case "set_tags":
		capability = plugins.TransmissionSetTagsCapability
	case "change":
		capability = plugins.TransmissionChangeCapability
	default:
		out.ErrorCode = "unselected_owned_operation"
		return out
	}
	counted := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != capability {
			return nil, errors.New("unexpected fixed SDK capability")
		}
		return countedCall(ctx, host, method, raw, &out.HostCalls)
	}
	tr := plugins.SharedTransmissionCommands{Host: counted, DownloaderID: input.DownloaderID}
	var result bool
	var err error
	if input.Op == "set_tags" {
		result, err = tr.SetTorrentTag(ctx, input.IDs, input.Tags, input.OrgTags)
	} else {
		result, err = tr.ChangeTorrent(ctx, input.HashString, input.UploadLimit, input.DownloadLimit, input.RatioLimit, input.SeedingTimeLimit)
	}
	if err == nil {
		out.Result, err = json.Marshal(result)
		out.ResultKind = "source_bool"
		out.SourceTruthy = &result
	}
	if err != nil {
		out.ErrorCode = safeErrorCode(err)
		if source, ok := err.(*plugins.TRChangeSourceError); ok && source != nil && source.Class == "ValueError" {
			out.ErrorCode, out.SourceError = "source_exception", source.Class
		}
		out.Result, out.SourceTruthy, out.ResultKind = nil, nil, ""
		return out
	}
	// OK is a completed typed SDK return, including source False. A successful
	// callback carrying ValueError remains a failed typed attempt, not HTTP fail.
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
	o.invocation++
	out := fixtureSnapshot{PID: os.Getpid(), InitCount: o.initCount, Invocation: o.invocation, Requested: len(input.Operations), OK: true, Results: []controlAttempt{}}
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
			if json.Unmarshal(raw, &request) != nil || request.Key != "tr_final" || request.Method != "POST" || request.Path != "/tr-final" {
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
	if manifest.Validate() != nil {
		os.Exit(1)
	}
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
		fmt.Fprintln(os.Stderr, "owned TR final commands SDK stopped with an error")
		os.Exit(82)
	}
}
