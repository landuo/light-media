// Owned, finite SiteOper.success/fail protocol fixture. It has no HTTP client,
// database, filesystem or clock access; all statistics writes use HostCaller.
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

const (
	fixtureDomain = "owned-site-access.invalid"
	invalidFrame  = `{"domain":"owned-site-access.invalid","seconds":"not_an_integer"}`
	previousFrame = `{"domain":"owned-site-access.invalid","seconds":null}`
)

func fixtureManifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "OwnedSiteAccess", Name: "Owned site access SDK fixture", Version: "1.0",
		Runtime: "go-executable", Entry: "owned-site-access-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
		Capabilities: []string{plugins.SiteAccessSuccessCapability, plugins.SiteAccessFailCapability},
		APIs:         []plugins.HTTPAPI{{Key: "access", Path: "/access", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type fixtureOperation struct {
	Op      string
	Domain  string
	Seconds *int64
}

type fixtureInput struct {
	Operations      []fixtureOperation
	ContinueOnError bool
	ProbePrevious   bool
}

// Reject unknown, duplicate and case-aliased keys rather than silently making
// the finite fixture an arbitrary capability or payload relay.
func exactObject(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	invalid := errors.New("invalid owned site access object")
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, invalid
		}
		permitted := false
		for _, candidate := range allowed {
			permitted = permitted || name == candidate
		}
		if _, exists := fields[name]; exists || !permitted {
			return nil, invalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, invalid
		}
		fields[name] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	return fields, nil
}

func requiredString(fields map[string]json.RawMessage, name string) (string, error) {
	var value *string
	raw, present := fields[name]
	if !present || json.Unmarshal(raw, &value) != nil || value == nil {
		return "", errors.New("missing or invalid owned site access selector")
	}
	return *value, nil
}

func optionalBool(fields map[string]json.RawMessage, name string) (bool, error) {
	raw, present := fields[name]
	if !present {
		return false, nil
	}
	var value *bool
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false, errors.New("invalid owned site access flag")
	}
	return *value, nil
}

func decodeOperation(raw json.RawMessage) (fixtureOperation, error) {
	var out fixtureOperation
	fields, err := exactObject(raw, "op", "domain", "seconds")
	if err != nil {
		return out, err
	}
	out.Op, err = requiredString(fields, "op")
	if err != nil {
		return out, err
	}
	if out.Op == "invalid_wire" {
		if len(fields) != 1 {
			return out, errors.New("invalid wire case takes no caller payload")
		}
		out.Domain = fixtureDomain
		return out, nil
	}
	out.Domain, err = requiredString(fields, "domain")
	if err != nil {
		return out, err
	}
	switch out.Domain {
	case fixtureDomain, "owned-site-access-second.invalid", " Owned-Site-Access.invalid ", "":
	default:
		return out, errors.New("owned site access domain is outside fixed fixture cases")
	}
	switch out.Op {
	case "success":
		raw, present := fields["seconds"]
		if !present || json.Unmarshal(raw, &out.Seconds) != nil {
			return out, errors.New("owned success requires explicit seconds or null")
		}
		if out.Seconds != nil {
			switch *out.Seconds {
			case -3, -1, 0, 1, 2, 5, 7, 10, 20, 99:
			default:
				return out, errors.New("owned seconds is outside fixed fixture cases")
			}
		}
	case "fail":
		if len(fields) != 2 {
			return out, errors.New("owned fail does not accept seconds")
		}
	default:
		return out, errors.New("unsupported owned site access operation")
	}
	return out, nil
}

func decodeFixtureInput(raw json.RawMessage) (fixtureInput, error) {
	var out fixtureInput
	if len(raw) == 0 || len(raw) > 16<<10 {
		return out, errors.New("invalid owned site access input size")
	}
	fields, err := exactObject(raw, "operations", "continue_on_error", "probe_previous")
	if err != nil {
		return out, err
	}
	var operations []json.RawMessage
	if json.Unmarshal(fields["operations"], &operations) != nil || len(operations) < 1 || len(operations) > 8 {
		return out, errors.New("owned site access requires one through eight operations")
	}
	if out.ContinueOnError, err = optionalBool(fields, "continue_on_error"); err != nil {
		return out, err
	}
	if out.ProbePrevious, err = optionalBool(fields, "probe_previous"); err != nil {
		return out, err
	}
	invalidCount := 0
	for _, rawOperation := range operations {
		operation, err := decodeOperation(rawOperation)
		if err != nil {
			return out, err
		}
		if operation.Op == "invalid_wire" {
			invalidCount++
			if invalidCount > 1 {
				return out, errors.New("only one invalid wire frame is allowed per invocation")
			}
		}
		out.Operations = append(out.Operations, operation)
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

type accessAttempt struct {
	Index      int            `json:"index"`
	Op         string         `json:"op"`
	Domain     string         `json:"domain"`
	OK         bool           `json:"ok"`
	SourceNone bool           `json:"source_none"`
	ErrorCode  string         `json:"error_code,omitempty"`
	HostCalls  hostCallCounts `json:"host_calls"`
}

type previousProbe struct {
	Label     string         `json:"label"`
	Available bool           `json:"available"`
	Denied    *bool          `json:"denied,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	HostCalls hostCallCounts `json:"host_calls"`
}

type fixtureSnapshot struct {
	PID           int             `json:"pid"`
	InitCount     int             `json:"init_count"`
	Invocation    int             `json:"invocation"`
	Requested     int             `json:"requested"`
	Completed     int             `json:"completed"`
	Succeeded     int             `json:"succeeded"`
	Failed        int             `json:"failed"`
	OK            bool            `json:"ok"`
	Interrupted   string          `json:"interrupted,omitempty"`
	PreviousProbe *previousProbe  `json:"previous_probe,omitempty"`
	Results       []accessAttempt `json:"results"`
}

type fixtureObject struct {
	mu                    sync.Mutex
	invocation, initCount int
	previous              plugins.HostCaller
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
	return "owned_site_access_failed"
}

func countedCall(ctx context.Context, host plugins.HostCaller, method string, raw json.RawMessage, counts *hostCallCounts) (json.RawMessage, error) {
	if host == nil {
		return nil, errors.New("owned site access requires a host caller")
	}
	counts.Total++
	counts.InputJSONBytes += len(raw)
	response, err := host(ctx, method, raw)
	counts.OutputJSONBytes += len(response)
	if err != nil {
		counts.Failed++
	} else {
		counts.Succeeded++
	}
	return response, err
}

func dispatchAccess(ctx context.Context, host plugins.HostCaller, index int, input fixtureOperation) accessAttempt {
	out := accessAttempt{Index: index, Op: input.Op, Domain: input.Domain}
	counted := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != plugins.SiteAccessSuccessCapability && method != plugins.SiteAccessFailCapability {
			return nil, errors.New("unexpected owned site access capability")
		}
		return countedCall(ctx, host, method, raw, &out.HostCalls)
	}
	sdk := plugins.SharedSiteAccess{Host: counted}
	var err error
	switch input.Op {
	case "success":
		err = sdk.Success(ctx, input.Domain, input.Seconds)
	case "fail":
		err = sdk.Fail(ctx, input.Domain)
	case "invalid_wire":
		// Deliberate host decoder boundary, NOT a source SQLite failure or a
		// typed SDK call. No caller-controlled raw frame and no retry.
		_, err = counted(ctx, plugins.SiteAccessSuccessCapability, json.RawMessage(invalidFrame))
	default:
		err = errors.New("unsupported owned site access operation")
	}
	out.OK = err == nil
	out.SourceNone = out.OK && input.Op != "invalid_wire"
	out.ErrorCode = safeErrorCode(err)
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
	out := fixtureSnapshot{PID: os.Getpid(), InitCount: o.initCount, Invocation: o.invocation, Requested: len(input.Operations), OK: true, Results: []accessAttempt{}}
	if input.ProbePrevious {
		probe := &previousProbe{Label: "previous_invocation_fixed_frame", Available: previous != nil}
		out.PreviousProbe = probe
		if previous != nil {
			// One OLD HostCaller attempt with a fixed owned frame. Even if a
			// lifetime bug accepts it, do not replay any current operation.
			response, probeErr := countedCall(ctx, previous, plugins.SiteAccessSuccessCapability, json.RawMessage(previousFrame), &probe.HostCalls)
			probe.ErrorCode = safeErrorCode(probeErr)
			denied := probeErr != nil && len(response) == 0 && (probe.ErrorCode == "invalid_host_call" || probe.ErrorCode == "PLUGIN_CALL_CLOSED")
			probe.Denied = &denied
			out.OK = denied
		}
	}
	for index, operation := range input.Operations {
		if err := ctx.Err(); err != nil {
			out.Interrupted, out.OK = safeErrorCode(err), false
			break
		}
		attempt := dispatchAccess(ctx, host, index, operation)
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
			if json.Unmarshal(raw, &request) != nil || request.Key != "access" || request.Method != "POST" || request.Path != "/access" {
				return nil, errors.New("unsupported owned site access HTTP route")
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
			return nil, errors.New("unsupported owned site access fixture method")
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
		fmt.Fprintln(os.Stderr, "owned site access SDK stopped with an error")
		os.Exit(82)
	}
}
