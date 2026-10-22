// Package plugins implements the versioned process boundary used by extensions.
// A separate process contains crashes; it is not an operating-system sandbox.
package plugins

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const ProtocolVersion = 1
const HostAPIVersion = 1

// ConfigDefaultsPure declares that missing-default writes have no independent
// business-owner state or frozen work to rebind. Only a matching compiled
// manifest can grant this contract; a stored manifest is not write authority.
const ConfigDefaultsPure = "pure/v1"

// ConfigDefaultsTransactional additionally requires a trusted host projection of
// business-owner state in the configuration transaction. This is not permission
// for worker-supplied hooks, arbitrary config replacement, or lifecycle actions.
const ConfigDefaultsTransactional = "transactional/v1"

// ConfigFormProjectionTransactional separately declares trusted host Form Save
// projection, including full replacements and equal-config source saves. A
// defaults declaration does not grant this contract. No worker RPC capability
// is implied, and a compiled adapter must validate its original business proof.
const ConfigFormProjectionTransactional = "transactional/v1"

// FormModelPolicy controls presentation only. It is independent of defaults or
// Form write permission and is authoritative only from the compiled catalog.
// A nil policy preserves the host overlay; a declaration specifies both modes.
const (
	FormModelHostOverlay = "host_overlay"
	FormModelSource      = "source"
)

type FormModelPolicy struct {
	Installed string `json:"installed"`
	Preview   string `json:"preview"`
}

var identityPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}(?:[-+][A-Za-z0-9.-]+)?$`)
var methodPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,95}$`)

// Manifest describes an executable package, not whether it has been tested or
// migrated. Credentials belong in the host's encrypted configuration store.
type Manifest struct {
	SchemaVersion        int              `json:"schema_version"`
	PluginID             string           `json:"plugin_id"`
	Name                 string           `json:"name"`
	Description          string           `json:"description,omitempty"`
	Icon                 string           `json:"icon,omitempty"`
	Author               string           `json:"author,omitempty"`
	AuthorURL            string           `json:"author_url,omitempty"`
	Version              string           `json:"version"`
	Runtime              string           `json:"runtime"`
	Entry                string           `json:"entry"`
	HostAPIMin           int              `json:"host_api_min"`
	HostAPIMax           int              `json:"host_api_max"`
	Methods              []string         `json:"methods"`
	Capabilities         []string         `json:"capabilities"`
	Events               []string         `json:"events,omitempty"`
	HasPage              bool             `json:"has_page,omitempty"`
	Dashboards           []string         `json:"dashboards,omitempty"`
	APIs                 []HTTPAPI        `json:"apis,omitempty"`
	ConfigDefaults       string           `json:"config_defaults,omitempty"`
	ConfigFormProjection string           `json:"config_form_projection,omitempty"`
	FormModelPolicy      *FormModelPolicy `json:"form_model_policy,omitempty"`
}

func (m Manifest) Validate() error {
	if m.FormModelPolicy != nil {
		for _, mode := range []string{m.FormModelPolicy.Installed, m.FormModelPolicy.Preview} {
			if mode != FormModelHostOverlay && mode != FormModelSource {
				return failure("invalid_manifest")
			}
		}
	}
	if m.ConfigFormProjection != "" && m.ConfigFormProjection != ConfigFormProjectionTransactional {
		return failure("invalid_manifest")
	}
	if m.ConfigDefaults != "" && m.ConfigDefaults != ConfigDefaultsPure && m.ConfigDefaults != ConfigDefaultsTransactional {
		return failure("invalid_manifest")
	}
	if m.SchemaVersion != 1 || !identityPattern.MatchString(m.PluginID) || !versionPattern.MatchString(m.Version) ||
		m.Name == "" || len(m.Name) > 256 || m.Runtime != "go-executable" || m.HostAPIMin < 1 || m.HostAPIMax < m.HostAPIMin ||
		m.Entry == "" || filepath.IsAbs(m.Entry) || strings.Contains(m.Entry, "\\") || filepath.ToSlash(filepath.Clean(m.Entry)) != m.Entry ||
		m.Entry == "." || m.Entry == ".." || strings.HasPrefix(m.Entry, "../") || strings.ContainsAny(m.Entry, "\x00\r\n") {
		return failure("invalid_manifest")
	}
	if m.HostAPIMin > HostAPIVersion || m.HostAPIMax < HostAPIVersion {
		return failure("unsupported_api")
	}
	if len(m.Methods) == 0 || len(m.Methods) > 64 || len(m.Capabilities) > 64 || len(m.Events) > 64 || len(m.Dashboards) > 64 {
		return failure("invalid_manifest")
	}
	if len(m.APIs) > 128 {
		return failure("invalid_manifest")
	}
	apiKeys := map[string]bool{}
	paths := map[string]bool{}
	for _, api := range m.APIs {
		if api.Validate() != nil || apiKeys[api.Key] {
			return failure("invalid_manifest")
		}
		apiKeys[api.Key] = true
		for _, method := range api.Methods {
			key := method + " " + api.Path
			if paths[key] {
				return failure("invalid_manifest")
			}
			paths[key] = true
		}
	}
	for _, values := range [][]string{m.Methods, m.Capabilities, m.Events, m.Dashboards} {
		seen := map[string]bool{}
		for _, value := range values {
			if !methodPattern.MatchString(value) || seen[value] {
				return failure("invalid_manifest")
			}
			seen[value] = true
		}
	}
	for _, method := range m.Methods {
		if strings.HasPrefix(method, "transport.") {
			return failure("invalid_manifest")
		}
	}
	if !slices.Contains(m.Methods, "stop") {
		return failure("invalid_manifest")
	}
	if (m.HasPage && !slices.Contains(m.Methods, "page")) || (len(m.Dashboards) != 0 && !slices.Contains(m.Methods, "dashboard")) {
		return failure("invalid_manifest")
	}
	return nil
}

type Identity struct {
	PluginID   string `json:"plugin_id"`
	InstanceID string `json:"instance_id"`
	Version    string `json:"plugin_version"`
}

type ReplyError struct {
	Code string `json:"code"`
}

// Frame is a single bounded JSON line. All calls, including reverse host calls,
// are tied to the same installed plugin instance and pinned implementation.
type Frame struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"request_id"`
	Type     string `json:"type"`
	Identity
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ReplyError     `json:"error,omitempty"`
}

type Handshake struct {
	Protocol     int                     `json:"protocol"`
	PluginID     string                  `json:"plugin_id"`
	Version      string                  `json:"version"`
	HostAPI      int                     `json:"host_api"`
	Methods      []string                `json:"methods"`
	Capabilities []string                `json:"capabilities"`
	Transport    *transportAdvertisement `json:"transport,omitempty"`
}

// Error deliberately contains only a local code. Worker output, command paths,
// stderr and wrapped transport errors never become public error messages.
type Error struct{ Code string }

func (e *Error) Error() string  { return "plugin: " + e.Code }
func failure(code string) error { return &Error{Code: code} }

// HostHandler must enforce the installed instance's actual scope on every
// operation. A capability named by a worker does not grant it authority.
type HostHandler func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error)

type Command struct {
	Path string
	Args []string
	Dir  string
}

type Runner struct {
	// ObservePhase is nil by default; it does not alter execution policy.
	ObservePhase     func(RunObservation)
	MaxInputBytes    int
	MaxMessageBytes  int
	MaxChunkBytes    int
	MaxMessageChunks int
	MaxSessionChunks int
	MaxFrameBytes    int
	MaxOutputBytes   int
	MaxStderrBytes   int
	MaxHostCalls     int
	Timeout          time.Duration
	// ExcludeHostTime makes Timeout a cumulative worker/transport budget.
	// Only a trusted native host should opt in: host callbacks retain the
	// caller's cancellation but do not consume this budget.
	ExcludeHostTime bool
}

type RunResult struct {
	SentWireBytes int
	ReadWireBytes int
	SentChunks    int
	ReadChunks    int
	Result        json.RawMessage
	HostCalls     int
	StderrBytes   int64
}

// Run starts one short-lived worker, handshakes before sending the invocation,
// serves bounded host calls, then requests cleanup before accepting success.
func (runner Runner) Run(ctx context.Context, command Command, manifest Manifest, instanceID, method string, payload json.RawMessage, host HostHandler) (out RunResult, err error) {
	runner.observe(PhaseRunEntered, ctx)
	// This closure runs after cleanup and result counters, but before the caller.
	// Its context is updated below to the actual Runner context.
	var observationContext context.Context
	defer func() {
		if observationContext == nil {
			runner.observe(PhaseRunReturned, ctx)
		}
	}()
	if err := manifest.Validate(); err != nil {
		return out, err
	}
	if ctx == nil || !identityPattern.MatchString(instanceID) ||
		!filepath.IsAbs(command.Path) || (command.Dir != "" && !filepath.IsAbs(command.Dir)) ||
		!methodPattern.MatchString(method) || method == "handshake" || method == "stop" || strings.HasPrefix(method, "transport.") || !slices.Contains(manifest.Methods, method) ||
		(len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload)) {
		return out, failure("invalid_invocation")
	}
	if runner.MaxFrameBytes == 0 {
		runner.MaxFrameBytes = 1 << 20
	}
	explicitOutput := runner.MaxOutputBytes != 0
	if runner.MaxOutputBytes == 0 {
		runner.MaxOutputBytes = defaultWireBytes
	}
	if runner.MaxInputBytes == 0 {
		runner.MaxInputBytes = defaultWireBytes
	}
	if runner.MaxMessageBytes == 0 {
		runner.MaxMessageBytes = defaultLogicalBytes
	}
	if runner.MaxChunkBytes == 0 {
		runner.MaxChunkBytes = defaultChunkBytes
	}
	if runner.MaxMessageChunks == 0 {
		runner.MaxMessageChunks = 16384
	}
	if runner.MaxSessionChunks == 0 {
		runner.MaxSessionChunks = 32768
	}
	if runner.MaxHostCalls == 0 {
		runner.MaxHostCalls = 64
	}
	if runner.MaxStderrBytes == 0 {
		runner.MaxStderrBytes = 1 << 20
	}
	if runner.Timeout == 0 {
		runner.Timeout = 2 * time.Minute
	}
	if runner.MaxFrameBytes < 256 || runner.MaxFrameBytes > 4<<20 || runner.MaxOutputBytes < runner.MaxFrameBytes || runner.MaxOutputBytes > defaultWireBytes || runner.MaxInputBytes < runner.MaxFrameBytes || runner.MaxInputBytes > defaultWireBytes || runner.MaxMessageBytes < runner.MaxFrameBytes || runner.MaxMessageBytes > defaultLogicalBytes || runner.MaxChunkBytes < 1 || runner.MaxChunkBytes > defaultChunkBytes || runner.MaxMessageChunks < 1 || runner.MaxMessageChunks > 32768 || runner.MaxSessionChunks < 1 || runner.MaxSessionChunks > 65536 || runner.MaxStderrBytes < 256 || runner.MaxStderrBytes > 8<<20 || runner.MaxHostCalls < 1 || runner.MaxHostCalls > 256 || runner.Timeout < time.Millisecond || runner.Timeout > 30*time.Minute {
		return out, failure("invalid_limits")
	}
	ctx, cancel, budget := runnerContext(ctx, runner.Timeout, runner.ExcludeHostTime)
	defer cancel()
	observationContext = ctx
	defer func() { runner.observe(PhaseRunReturned, observationContext) }()
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir, cmd.WaitDelay = command.Dir, time.Second
	// Do not inherit provider credentials or management sessions from the host.
	cmd.Env = []string{"GOMEMLIMIT=64MiB"}
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ", "SYSTEMROOT"} {
		if value, present := os.LookupEnv(key); present {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	stderr := &byteCounter{limit: int64(runner.MaxStderrBytes), cancel: cancel}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return out, failure("worker_start_failed")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return out, failure("worker_start_failed")
	}
	runner.observe(PhaseStartBegin, ctx)
	err = cmd.Start()
	runner.observe(PhaseStartEnd, ctx)
	if err != nil {
		stdin.Close()
		stdout.Close()
		return out, failure("worker_start_failed")
	}
	waited := false
	defer func() {
		stdin.Close()
		if !waited {
			runner.observe(PhaseCleanupWaitBegin, ctx)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			runner.observe(PhaseCleanupWaitEnd, ctx)
		}
		out.StderrBytes = stderr.bytes
		if stderr.exceeded {
			err = failure("stderr_limit")
		} else if ctx.Err() != nil {
			err = failure("execution_canceled")
		}
		// Cancellation can win after Wait succeeded but before this final check.
		// Error results retain transport evidence, never a successful payload.
		if err != nil {
			out.Result = nil
		}
	}()
	identity := Identity{PluginID: manifest.PluginID, InstanceID: instanceID, Version: manifest.Version}
	session := wireSession{maxInput: runner.MaxInputBytes, role: "host", closeIO: func() { stdin.Close(); stdout.Close() }, reader: bufio.NewReaderSize(stdout, 4096), writer: stdin, identity: identity, maxFrame: runner.MaxFrameBytes, maxOutput: runner.MaxOutputBytes, maxHostCalls: runner.MaxHostCalls, host: host, ctx: ctx, hostBudget: budget}
	stopIO := context.AfterFunc(ctx, session.abort)
	defer stopIO()
	defer func() {
		out.HostCalls = session.hostCalls
		out.SentWireBytes = session.inputBytes
		out.ReadWireBytes = session.outputBytes
		out.SentChunks = session.sentChunks
		out.ReadChunks = session.readChunks
	}()
	hello, _ := json.Marshal(map[string]int{"protocol": ProtocolVersion, "host_api": HostAPIVersion})
	runner.observe(PhaseHandshakeBegin, ctx)
	raw, err := session.exchange("handshake", hello)
	runner.observe(PhaseHandshakeEnd, ctx)
	if err != nil {
		return out, err
	}
	var agreed Handshake
	if json.Unmarshal(raw, &agreed) != nil || agreed.Protocol != ProtocolVersion || agreed.HostAPI != HostAPIVersion || agreed.PluginID != manifest.PluginID || agreed.Version != manifest.Version || !sameSet(agreed.Methods, manifest.Methods) || !sameSet(agreed.Capabilities, manifest.Capabilities) {
		return out, failure("handshake_mismatch")
	}
	if agreed.Transport != nil {
		if agreed.Transport.Version != 1 {
			return out, failure("transport_mismatch")
		}
		limits := transportLimits{Version: 1, FrameBytes: min(runner.MaxFrameBytes, 1<<20), LogicalBytes: runner.MaxMessageBytes, HostSendBytes: runner.MaxInputBytes, HostReadBytes: runner.MaxOutputBytes, ChunkBytes: runner.MaxChunkBytes, MessageChunks: runner.MaxMessageChunks, SessionChunks: runner.MaxSessionChunks, Transfers: runner.MaxHostCalls + 4}
		proposal, _ := json.Marshal(limits)
		runner.observe(PhaseNegotiateBegin, ctx)
		answer, negotiateErr := session.exchange("transport.negotiate", proposal)
		runner.observe(PhaseNegotiateEnd, ctx)
		var accepted transportLimits
		if negotiateErr != nil {
			return out, negotiateErr
		}
		if strictJSON(answer, &accepted) != nil || accepted != limits || limits.valid() != nil {
			return out, failure("transport_mismatch")
		}
		session.enable(limits)
	} else if !explicitOutput {
		session.maxOutput = 8 << 20
	}
	runner.observe(PhaseInvokeBegin, ctx)
	result, err := session.exchange(method, payload)
	runner.observe(PhaseInvokeEnd, ctx)
	if err != nil {
		return out, err
	}
	runner.observe(PhaseStopBegin, ctx)
	_, err = session.exchange("stop", nil)
	runner.observe(PhaseStopEnd, ctx)
	if err != nil {
		return out, err
	}
	stdin.Close()
	runner.observe(PhaseWaitBegin, ctx)
	err = cmd.Wait()
	waited = true
	runner.observe(PhaseWaitEnd, ctx)
	if err != nil {
		return out, failure("worker_failed")
	}
	out.Result, out.HostCalls = result, session.hostCalls
	return out, nil
}

type byteCounter struct {
	bytes, limit int64
	exceeded     bool
	cancel       context.CancelFunc
}

func (w *byteCounter) Write(p []byte) (int, error) {
	w.bytes += int64(len(p))
	if w.bytes > w.limit && !w.exceeded {
		w.exceeded = true
		w.cancel()
	}
	return len(p), nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range a {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	for _, value := range b {
		if !seen[value] {
			return false
		}
	}
	return true
}

type wireSession struct {
	reader                                                    *bufio.Reader
	writer                                                    io.Writer
	identity                                                  Identity
	ctx                                                       context.Context
	maxFrame, maxOutput, maxHostCalls, outputBytes, hostCalls int
	maxInput, inputBytes                                      int
	role                                                      string
	enabled                                                   bool
	limits                                                    transportLimits
	assembly                                                  *chunkAssembly
	seenTransfers                                             map[string]bool
	sentChunks, readChunks                                    int
	closeIO                                                   func()
	abortOnce                                                 sync.Once
	aborted                                                   atomic.Bool
	host                                                      HostHandler
	hostBudget                                                *runnerBudget
}

func (s *wireSession) exchange(method string, payload json.RawMessage) (json.RawMessage, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, failure("request_failed")
	}
	id := hex.EncodeToString(nonce)
	if err := s.write(Frame{Protocol: ProtocolVersion, ID: id, Type: "call", Identity: s.identity, Method: method, Payload: payload}); err != nil {
		return nil, err
	}
	for {
		frame, err := s.read()
		if err != nil {
			return nil, err
		}
		if frame.Protocol != ProtocolVersion || frame.Identity != s.identity || frame.ID == "" || len(frame.ID) > 128 {
			return nil, failure("identity_mismatch")
		}
		if frame.Type == "host_call" {
			// The initial handshake cannot request configuration or other host data.
			if method == "handshake" || method == "transport.negotiate" || !methodPattern.MatchString(frame.Method) || strings.HasPrefix(frame.Method, "transport.") || frame.Result != nil || frame.Error != nil {
				return nil, failure("invalid_host_call")
			}
			if s.hostCalls >= s.maxHostCalls {
				return nil, failure("host_call_limit")
			}
			s.hostCalls++
			reply := Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "host_result", Identity: s.identity}
			if s.host == nil {
				reply.Error = &ReplyError{Code: "HOST_DENIED"}
			} else {
				reply.Result, err = s.callHost(frame.Method, frame.Payload)
				if err != nil {
					reply.Result, reply.Error = nil, &ReplyError{Code: "HOST_DENIED"}
				}
				if reply.Error == nil && len(reply.Result) == 0 {
					reply.Result = json.RawMessage("null")
				}
			}
			if err := s.write(reply); err != nil {
				return nil, err
			}
			continue
		}
		if frame.Type != "result" || frame.ID != id || frame.Method != "" || frame.Payload != nil || (frame.Error != nil && frame.Result != nil) {
			return nil, failure("invalid_reply")
		}
		if frame.Error != nil {
			return nil, workerReplyFailure(frame.Error.Code)
		}
		if !json.Valid(frame.Result) {
			return nil, failure("invalid_reply")
		}
		return frame.Result, nil
	}
}
