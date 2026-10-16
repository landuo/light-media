// Package plugins implements the versioned process boundary used by extensions.
// A separate process contains crashes; it is not an operating-system sandbox.
package plugins

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const ProtocolVersion = 1
const HostAPIVersion = 1

var identityPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}(?:[-+][A-Za-z0-9.-]+)?$`)
var methodPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,95}$`)

// Manifest describes an executable package, not whether it has been tested or
// migrated. Credentials belong in the host's encrypted configuration store.
type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	PluginID      string   `json:"plugin_id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Icon          string   `json:"icon,omitempty"`
	Author        string   `json:"author,omitempty"`
	AuthorURL     string   `json:"author_url,omitempty"`
	Version       string   `json:"version"`
	Runtime       string   `json:"runtime"`
	Entry         string   `json:"entry"`
	HostAPIMin    int      `json:"host_api_min"`
	HostAPIMax    int      `json:"host_api_max"`
	Methods       []string `json:"methods"`
	Capabilities  []string `json:"capabilities"`
	Events        []string `json:"events,omitempty"`
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 || !identityPattern.MatchString(m.PluginID) || !versionPattern.MatchString(m.Version) ||
		m.Name == "" || len(m.Name) > 256 || m.Runtime != "go-executable" || m.HostAPIMin < 1 || m.HostAPIMax < m.HostAPIMin ||
		m.Entry == "" || filepath.IsAbs(m.Entry) || strings.Contains(m.Entry, "\\") || filepath.ToSlash(filepath.Clean(m.Entry)) != m.Entry ||
		m.Entry == "." || m.Entry == ".." || strings.HasPrefix(m.Entry, "../") || strings.ContainsAny(m.Entry, "\x00\r\n") {
		return failure("invalid_manifest")
	}
	if m.HostAPIMin > HostAPIVersion || m.HostAPIMax < HostAPIVersion {
		return failure("unsupported_api")
	}
	if len(m.Methods) == 0 || len(m.Methods) > 64 || len(m.Capabilities) > 64 || len(m.Events) > 64 {
		return failure("invalid_manifest")
	}
	for _, values := range [][]string{m.Methods, m.Capabilities, m.Events} {
		seen := map[string]bool{}
		for _, value := range values {
			if !methodPattern.MatchString(value) || seen[value] {
				return failure("invalid_manifest")
			}
			seen[value] = true
		}
	}
	if !slices.Contains(m.Methods, "stop") {
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
	Protocol     int      `json:"protocol"`
	PluginID     string   `json:"plugin_id"`
	Version      string   `json:"version"`
	HostAPI      int      `json:"host_api"`
	Methods      []string `json:"methods"`
	Capabilities []string `json:"capabilities"`
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
	MaxFrameBytes  int
	MaxOutputBytes int
	MaxStderrBytes int
	MaxHostCalls   int
	Timeout        time.Duration
}

type RunResult struct {
	Result      json.RawMessage
	HostCalls   int
	StderrBytes int64
}

// Run starts one short-lived worker, handshakes before sending the invocation,
// serves bounded host calls, then requests cleanup before accepting success.
func (runner Runner) Run(ctx context.Context, command Command, manifest Manifest, instanceID, method string, payload json.RawMessage, host HostHandler) (out RunResult, err error) {
	if err := manifest.Validate(); err != nil {
		return out, err
	}
	if ctx == nil || !identityPattern.MatchString(instanceID) ||
		!filepath.IsAbs(command.Path) || (command.Dir != "" && !filepath.IsAbs(command.Dir)) ||
		!methodPattern.MatchString(method) || method == "handshake" || method == "stop" || !slices.Contains(manifest.Methods, method) ||
		(len(payload) > 0 && !json.Valid(payload)) {
		return out, failure("invalid_invocation")
	}
	if runner.MaxFrameBytes == 0 {
		runner.MaxFrameBytes = 1 << 20
	}
	if runner.MaxOutputBytes == 0 {
		runner.MaxOutputBytes = 8 << 20
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
	if runner.MaxFrameBytes < 256 || runner.MaxFrameBytes > 4<<20 || runner.MaxOutputBytes < runner.MaxFrameBytes || runner.MaxOutputBytes > 32<<20 || runner.MaxStderrBytes < 256 || runner.MaxStderrBytes > 8<<20 || runner.MaxHostCalls < 1 || runner.MaxHostCalls > 256 || runner.Timeout < time.Millisecond || runner.Timeout > 30*time.Minute {
		return out, failure("invalid_limits")
	}
	ctx, cancel := context.WithTimeout(ctx, runner.Timeout)
	defer cancel()
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
	if err = cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return out, failure("worker_start_failed")
	}
	waited := false
	defer func() {
		stdin.Close()
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		out.StderrBytes = stderr.bytes
		if stderr.exceeded {
			err = failure("stderr_limit")
		} else if ctx.Err() != nil {
			err = failure("execution_canceled")
		}
	}()
	identity := Identity{PluginID: manifest.PluginID, InstanceID: instanceID, Version: manifest.Version}
	session := wireSession{reader: bufio.NewReaderSize(stdout, 4096), writer: stdin, identity: identity, maxFrame: runner.MaxFrameBytes, maxOutput: runner.MaxOutputBytes, maxHostCalls: runner.MaxHostCalls, host: host, ctx: ctx}
	hello, _ := json.Marshal(map[string]int{"protocol": ProtocolVersion, "host_api": HostAPIVersion})
	raw, err := session.exchange("handshake", hello)
	if err != nil {
		return out, err
	}
	var agreed Handshake
	if json.Unmarshal(raw, &agreed) != nil || agreed.Protocol != ProtocolVersion || agreed.HostAPI != HostAPIVersion || agreed.PluginID != manifest.PluginID || agreed.Version != manifest.Version || !sameSet(agreed.Methods, manifest.Methods) || !sameSet(agreed.Capabilities, manifest.Capabilities) {
		return out, failure("handshake_mismatch")
	}
	result, err := session.exchange(method, payload)
	if err != nil {
		return out, err
	}
	if _, err = session.exchange("stop", nil); err != nil {
		return out, err
	}
	stdin.Close()
	err = cmd.Wait()
	waited = true
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
	host                                                      HostHandler
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
			if method == "handshake" || !methodPattern.MatchString(frame.Method) || frame.Result != nil || frame.Error != nil {
				return nil, failure("invalid_host_call")
			}
			s.hostCalls++
			if s.hostCalls > s.maxHostCalls {
				return nil, failure("host_call_limit")
			}
			reply := Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "host_result", Identity: s.identity}
			if s.host == nil {
				reply.Error = &ReplyError{Code: "HOST_DENIED"}
			} else {
				reply.Result, err = s.host(s.ctx, s.identity, frame.Method, frame.Payload)
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
			return nil, failure("plugin_operation_failed")
		}
		if !json.Valid(frame.Result) {
			return nil, failure("invalid_reply")
		}
		return frame.Result, nil
	}
}

func (s *wireSession) write(frame Frame) error {
	raw, err := json.Marshal(frame)
	if err != nil || len(raw)+1 > s.maxFrame {
		return failure("frame_limit")
	}
	raw = append(raw, '\n')
	n, err := s.writer.Write(raw)
	if err != nil || n != len(raw) {
		return failure("worker_io_failed")
	}
	return nil
}

func (s *wireSession) read() (Frame, error) {
	var frame Frame
	var raw []byte
	for {
		part, err := s.reader.ReadSlice('\n')
		s.outputBytes += len(part)
		if len(raw)+len(part) > s.maxFrame || s.outputBytes > s.maxOutput {
			return frame, failure("output_limit")
		}
		raw = append(raw, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return frame, failure("worker_io_failed")
		}
		break
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&frame) != nil {
		return frame, failure("invalid_frame")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return frame, failure("invalid_frame")
	}
	return frame, nil
}
