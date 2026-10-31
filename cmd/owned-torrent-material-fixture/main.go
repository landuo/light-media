// Owned standard-SDK verification only. This worker has no HTTP client,
// torrent cache, downloader, browser or filesystem material implementation.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"light-media/internal/plugins"
)

// The coordinator may build a capability-negative artifact from this same
// source with -ldflags '-X main.fixtureDenied=true'. No runtime input grants it.
var fixtureDenied = "false"

func fixtureManifest(denied bool) plugins.Manifest {
	id, entry := "OwnedTorrentMaterial", "owned-torrent-material-sdk-fixture"
	capabilities := []string{plugins.DownloadTorrentCapability}
	if denied {
		id, entry = "OwnedTorrentMaterialDenied", "owned-torrent-material-sdk-fixture-no-cap"
		capabilities = []string{}
	}
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: id, Name: "Owned torrent material SDK fixture", Version: "1.0",
		Runtime: "go-executable", Entry: entry, HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "get_page", "get_render_mode", "snapshot", "http", "close", "stop_service", "stop"},
		Capabilities: capabilities,
		APIs:         []plugins.HTTPAPI{{Key: "material", Path: "/material", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}},
	}
}

type fixtureInput struct {
	Requests        []plugins.DownloadTorrentRequest `json:"requests"`
	ContinueOnError bool                             `json:"continue_on_error"`
	ProbePrevious   bool                             `json:"probe_previous"`
}

// Fields are kind-specific. In particular, none, string "" and bytes [] do not
// collapse into the same JSON value. No prepared host digest is read here.
type materialObservation struct {
	Kind   string   `json:"kind"`
	Length *int     `json:"length,omitempty"`
	SHA256 *string  `json:"sha256,omitempty"`
	Text   *string  `json:"text,omitempty"`
	Folder string   `json:"folder"`
	Files  []string `json:"files"`
}

type hostCallCounts struct {
	Total   int `json:"total"`
	Prepare int `json:"prepare"`
	Read    int `json:"read"`
	Release int `json:"release"`
}

type materialAttempt struct {
	Index       int                  `json:"index"`
	OK          bool                 `json:"ok"`
	Result      *materialObservation `json:"result,omitempty"`
	Error       string               `json:"error,omitempty"`
	ErrorCode   string               `json:"error_code,omitempty"`
	SourceError string               `json:"source_error,omitempty"`
	HostCalls   hostCallCounts       `json:"host_calls"`
}

type fixtureSnapshot struct {
	PID              int               `json:"pid"`
	Invocation       int               `json:"invocation"`
	HadPrevious      bool              `json:"had_previous"`
	StaleDenied      *bool             `json:"stale_denied,omitempty"`
	Requested        int               `json:"requested"`
	Completed        int               `json:"completed"`
	Succeeded        int               `json:"succeeded"`
	Failed           int               `json:"failed"`
	OK               bool              `json:"ok"`
	Interrupted      string            `json:"interrupted,omitempty"`
	ReassembledBytes int64             `json:"reassembled_bytes"`
	Results          []materialAttempt `json:"results"`
}

type fixtureObject struct {
	mu         sync.Mutex
	invocation int
	previous   plugins.HostCaller
}

func decodeFixtureInput(raw json.RawMessage) (fixtureInput, error) {
	var input fixtureInput
	if len(raw) == 0 || len(raw) > plugins.MaxPayloadBytes {
		return input, errors.New("invalid owned material fixture input size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		return input, err
	}
	if d.Decode(new(any)) != io.EOF {
		return input, errors.New("trailing owned material fixture input")
	}
	// Two sequential maximum-size calls exercise full 32 MiB reconstruction.
	// Actual Runner wire/call evidence, not this count, establishes budget success.
	if len(input.Requests) < 1 || len(input.Requests) > 2 {
		return input, errors.New("owned material fixture requires one or two requests")
	}
	return input, nil
}

func observeMaterial(value plugins.TorrentMaterialResult) (*materialObservation, error) {
	if value.Files == nil {
		return nil, errors.New("SDK material files must be an explicit list")
	}
	observation := &materialObservation{Kind: "none", Folder: value.Folder, Files: append([]string{}, value.Files...)}
	if value.Content == nil {
		return observation, nil
	}
	observation.Kind = value.Content.Kind
	switch value.Content.Kind {
	case "string":
		text := value.Content.String
		observation.Text = &text
	case "bytes":
		if value.Content.Bytes == nil {
			return nil, errors.New("SDK byte result lost its non-nil byte value")
		}
		// DownloadTorrent has returned the COMPLETE byte slice to this worker.
		// Hash every actual byte here, after all reads and release have completed;
		// never substitute prepare.total/sha256 or a digest-only HostCaller result.
		length := len(value.Content.Bytes)
		digest := sha256.Sum256(value.Content.Bytes)
		encoded := hex.EncodeToString(digest[:])
		observation.Length, observation.SHA256 = &length, &encoded
	default:
		return nil, errors.New("SDK returned an unknown material content kind")
	}
	return observation, nil
}

func runMaterial(ctx context.Context, host plugins.HostCaller, index int, input plugins.DownloadTorrentRequest) materialAttempt {
	attempt := materialAttempt{Index: index}
	if host == nil {
		attempt.Error = "owned material fixture requires an active host caller"
		return attempt
	}
	// Only count actual SDK callback attempts. These are not physical wire-byte
	// measurements; those remain the normal Runner's independently recorded data.
	counted := func(callCtx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != plugins.DownloadTorrentCapability {
			return nil, errors.New("owned material fixture attempted another capability")
		}
		attempt.HostCalls.Total++
		var operation struct {
			Op string `json:"op"`
		}
		if json.Unmarshal(raw, &operation) == nil {
			switch operation.Op {
			case "prepare":
				attempt.HostCalls.Prepare++
			case "read":
				attempt.HostCalls.Read++
			case "release":
				attempt.HostCalls.Release++
			}
		}
		return host(callCtx, method, raw)
	}
	// Each SDK call owns prepare/read/release itself. A later request is never
	// started while this fixture deliberately retains an earlier transfer token.
	value, err := (plugins.SharedDownloadChain{Host: counted}).DownloadTorrent(ctx, input)
	if err == nil {
		attempt.Result, err = observeMaterial(value)
	}
	if err != nil {
		// Explicit operation failure is not source None and not protocol/process
		// success. Owned tests assert ok/error; nothing is written to worker logs.
		attempt.Error = err.Error()
		var protocol *plugins.Error
		if errors.As(err, &protocol) {
			attempt.ErrorCode = protocol.Code
		}
		var source *plugins.DownloadTorrentSourceError
		if errors.As(err, &source) {
			attempt.SourceError = source.Class
		}
		attempt.Result = nil
		return attempt
	}
	attempt.OK = true
	return attempt
}

func (o *fixtureObject) snapshot(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (fixtureSnapshot, error) {
	input, err := decodeFixtureInput(raw)
	if err != nil {
		return fixtureSnapshot{}, err
	}
	o.mu.Lock()
	previous := o.previous
	o.previous = host
	o.invocation++
	sequence := o.invocation
	o.mu.Unlock()
	out := fixtureSnapshot{PID: os.Getpid(), Invocation: sequence, HadPrevious: previous != nil, Requested: len(input.Requests), OK: true, Results: []materialAttempt{}}
	if input.ProbePrevious && previous != nil {
		// An empty enclosure is a side-effect-free source input even if a broken
		// host accepts the stale callback. It cannot cause fixture network IO.
		_, err := (plugins.SharedDownloadChain{Host: previous}).DownloadTorrent(ctx, plugins.DownloadTorrentRequest{Torrent: json.RawMessage(`{"enclosure":null}`)})
		denied := err != nil
		out.StaleDenied = &denied
		if !denied {
			out.OK = false
		}
	}
	for index, request := range input.Requests {
		attempt := runMaterial(ctx, host, index, request)
		out.Results = append(out.Results, attempt)
		out.Completed++
		if attempt.OK {
			out.Succeeded++
			if attempt.Result.Kind == "bytes" {
				out.ReassembledBytes += int64(*attempt.Result.Length)
			}
		} else {
			out.Failed++
			out.OK = false
			if !input.ContinueOnError {
				break
			}
		}
		if ctx.Err() != nil {
			// Cancellation cannot be turned into permission to perform the next IO.
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
			if request.Key != "material" || request.Method != "POST" || request.Path != "/material" {
				return nil, errors.New("unsupported owned material HTTP route")
			}
			method, raw = "snapshot", request.Body
		}
		switch method {
		case "init", "close", "stop_service", "stop":
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
			return nil, errors.New("unsupported owned material fixture method")
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
		fmt.Fprintln(os.Stderr, "owned material SDK stopped with an error")
		os.Exit(82)
	}
}
