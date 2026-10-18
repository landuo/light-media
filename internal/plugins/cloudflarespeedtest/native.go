package cloudflarespeedtest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HTTPAdapter uses only trusted, host-constructed clients. Direct must not use
// environment proxies; Proxied models RequestUtils(proxies=settings.PROXY).
// TLS verification is never disabled. Release mirror is a host setting, not
// input accepted from the worker or an HTTP invocation.
type HTTPAdapter struct {
	Direct, Proxied *http.Client
	Mirror          string
	HTTPSProxy      bool
}

func (h *HTTPAdapter) client(proxy bool) (*http.Client, error) {
	c := h.Direct
	if proxy && h.Proxied != nil {
		c = h.Proxied
	}
	if c == nil {
		return nil, errors.New("CloudflareSpeedTest HTTP client unconfigured")
	}
	copy := *c
	prior := copy.CheckRedirect
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("CloudflareSpeedTest redirect limit")
		}
		// Public GitHub asset redirects are expected; never forward a proxy's
		// user credentials or authorization headers to an unrelated endpoint.
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("CloudflareSpeedTest redirect refused")
		}
		host := strings.ToLower(req.URL.Hostname())
		if host != "api.github.com" && host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" {
			return errors.New("CloudflareSpeedTest redirect host refused")
		}
		if prior != nil {
			return prior(req, via)
		}
		return nil
	}
	return &copy, nil
}
func (h *HTTPAdapter) Latest(ctx context.Context, proxy bool) (ReleaseResponse, error) {
	c, err := h.client(proxy)
	if err != nil {
		return ReleaseResponse{}, err
	}
	if c.Timeout == 0 {
		c.Timeout = 20 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, "GET", ReleaseAPI, nil)
	if err != nil {
		return ReleaseResponse{}, err
	}
	res, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ReleaseResponse{}, ctx.Err()
		}
		return ReleaseResponse{}, nil
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return ReleaseResponse{}, nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil {
		if ctx.Err() != nil {
			return ReleaseResponse{}, ctx.Err()
		}
		return ReleaseResponse{}, nil
	}
	if len(data) > 2<<20 {
		return ReleaseResponse{}, sourceError("ValueError", "release JSON size")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ReleaseResponse{}, sourceError("JSONDecodeError", "release response")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ReleaseResponse{}, sourceError("JSONDecodeError", "release response")
	}
	m, ok := value.(map[string]any)
	if !ok {
		return ReleaseResponse{}, sourceError("TypeError", "release tag_name")
	}
	tag, ok := m["tag_name"]
	if !ok {
		return ReleaseResponse{}, sourceError("KeyError", "release tag_name")
	}
	return ReleaseResponse{Available: true, Tag: tag}, nil
}

func releaseURL(req InstallRequest) bool {
	u, err := url.Parse(req.URL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if req.Version == "" || strings.ContainsAny(req.Version, "/\\?#\r\n\x00") || req.Version == "." || req.Version == ".." {
		return false
	}
	want := ReleasePrefix + "/" + req.Version + "/" + req.Archive
	if req.URL != want {
		return false
	}
	switch req.Archive {
	case "cfst_windows_amd64.zip", "cfst_darwin_amd64.zip", "cfst_darwin_arm64.zip", "cfst_linux_amd64.tar.gz", "cfst_linux_arm64.tar.gz":
		return true
	}
	return false
}
func (h *HTTPAdapter) download(ctx context.Context, req InstallRequest) ([]byte, error) {
	if !releaseURL(req) {
		return nil, ErrArtifactUnverified
	}
	target := req.URL
	proxy := h.HTTPSProxy || req.Platform.OS == "windows"
	if req.Platform.OS != "windows" && !h.HTTPSProxy {
		mirror := h.Mirror
		if mirror == "" {
			mirror = "https://ghfast.top"
		}
		u, e := url.Parse(mirror)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("CloudflareSpeedTest mirror invalid")
		}
		target = strings.TrimRight(mirror, "/") + "/" + req.URL
	}
	c, err := h.client(proxy)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	defer res.Body.Close()
	// Windows source only accepts 200; wget accepts a successful response.
	if res.StatusCode != 200 {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 128<<20+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	if len(data) > 128<<20 {
		return nil, errors.New("CloudflareSpeedTest archive size exceeded")
	}
	return data, nil
}

type CLI interface {
	Run(context.Context, RunSpec) (RunResult, error)
}

// NativeRuntime never executes a downloaded or old-data binary implicitly.
// A host must supply both archive and executable digests from its trusted
// catalog; absent approval is an explicit unverified error, not fake success.
type NativeRuntime struct {
	Files  *OwnedFiles
	HTTP   *HTTPAdapter
	CLI    CLI
	Target Platform
	Pins   Pins
	Guard  func(context.Context) error
}

func (r *NativeRuntime) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.Guard == nil {
		return errors.New("CloudflareSpeedTest native owner guard unconfigured")
	}
	return r.Guard(ctx)
}
func (r *NativeRuntime) Platform() Platform { return r.Target }
func (r *NativeRuntime) Exists(ctx context.Context, f FileRef) (bool, error) {
	if err := r.guard(ctx); err != nil {
		return false, err
	}
	if r.Files == nil {
		return false, ErrStorageScope
	}
	return r.Files.Exists(ctx, f, r.Target)
}
func (r *NativeRuntime) Ensure(ctx context.Context) error {
	if err := r.guard(ctx); err != nil {
		return err
	}
	if r.Files == nil {
		return ErrStorageScope
	}
	return r.Files.Ensure(ctx)
}
func (r *NativeRuntime) Reset(ctx context.Context) error {
	if err := r.guard(ctx); err != nil {
		return err
	}
	if r.Files == nil {
		return ErrStorageScope
	}
	return r.Files.Reset(ctx)
}
func (r *NativeRuntime) Latest(ctx context.Context, proxy bool) (ReleaseResponse, error) {
	if err := r.guard(ctx); err != nil {
		return ReleaseResponse{}, err
	}
	if r.HTTP == nil {
		return ReleaseResponse{}, errors.New("CloudflareSpeedTest HTTP unconfigured")
	}
	return r.HTTP.Latest(ctx, proxy)
}
func (r *NativeRuntime) Download(ctx context.Context, req InstallRequest) error {
	if err := r.guard(ctx); err != nil {
		return err
	}
	if r.HTTP == nil || r.Files == nil {
		return errors.New("CloudflareSpeedTest native installation unconfigured")
	}
	if _, ok := r.Pins.Archives[req.URL]; !ok {
		return ErrArtifactUnverified
	}
	data, err := r.HTTP.download(ctx, req)
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	if err = r.Pins.archive(req, data); err != nil {
		return err
	}
	if err = r.guard(ctx); err != nil {
		return err
	}
	return r.Files.write(ctx, req.Archive, data, 0600)
}
func (r *NativeRuntime) Extract(ctx context.Context, req InstallRequest) error {
	if err := r.guard(ctx); err != nil {
		return err
	}
	if r.Files == nil || !releaseURL(req) {
		return ErrArtifactUnverified
	}
	data, err := r.Files.read(ctx, req.Archive, 128<<20)
	if err != nil {
		return err
	}
	if err = r.Pins.archive(req, data); err != nil {
		return err
	}
	if err = r.guard(ctx); err != nil {
		return err
	}
	err = extractArchive(ctx, r.Files, req, data)
	var extraction *ExtractionError
	if req.Platform.OS != "windows" && errors.As(err, &extraction) {
		// Source Unix tar/ditto is os.system: a completed format failure is
		// only a nonzero exit status, which it ignores before removing the
		// archive and checking the old/new executable. Windows ZipFile raises.
		return nil
	}
	return err
}
func (r *NativeRuntime) Remove(ctx context.Context, name string) error {
	if err := r.guard(ctx); err != nil {
		return err
	}
	if r.Files == nil {
		return ErrStorageScope
	}
	return r.Files.Remove(ctx, name)
}
func (r *NativeRuntime) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if err := r.guard(ctx); err != nil {
		return RunResult{}, err
	}
	if r.CLI == nil {
		return RunResult{}, errors.New("CloudflareSpeedTest CLI unconfigured")
	}
	return r.CLI.Run(ctx, spec)
}
func (r *NativeRuntime) ReadResult(ctx context.Context) ([]byte, error) {
	if err := r.guard(ctx); err != nil {
		return nil, err
	}
	if r.Files == nil {
		return nil, ErrStorageScope
	}
	return r.Files.ReadResult(ctx)
}

// NativeCLI executes only a newly staged copy of the exact approved bytes. It
// inherits no environment secrets, does not use shell=True, never taskkills an
// unrelated process by name and never retries after Start. The host still
// needs OS/container isolation for filesystem/network permissions of the CLI.
type NativeCLI struct {
	Files *OwnedFiles
	Pins  Pins
	Guard func(context.Context) error
}

// CLIError is diagnostic, not a host authority proof. The host must never use
// a worker-supplied public field to bypass its journal or authorize a retry.
type CLIError struct {
	Dispatched bool
	Operation  string
}

func (e *CLIError) Error() string {
	if e.Dispatched {
		return "CloudflareSpeedTest CLI result unknown: " + e.Operation
	}
	return "CloudflareSpeedTest CLI not dispatched: " + e.Operation
}

func (c *NativeCLI) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if c == nil || c.Guard == nil || c.Files == nil {
		return RunResult{}, &CLIError{Operation: "owner guard"}
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	if err := c.Guard(ctx); err != nil {
		return RunResult{}, err
	}
	if spec.Result != "result_hosts.txt" {
		return RunResult{}, ErrStorageScope
	}
	switch spec.Binary {
	case "cfst", "CloudflareST", "cfst.exe", "CloudflareST.exe":
	default:
		return RunResult{}, ErrStorageScope
	}
	for _, arg := range spec.Args {
		if strings.ContainsRune(arg, 0) {
			return RunResult{}, &CLIError{Operation: "argument validation"}
		}
	}
	data, err := c.Files.read(ctx, spec.Binary, 128<<20)
	if err != nil {
		return RunResult{}, &CLIError{Operation: "executable read"}
	}
	if err = c.Pins.binary(spec.Binary, data); err != nil {
		return RunResult{}, err
	}
	// Copy eliminates a hash-to-exec race against the mutable archive output.
	stage, err := os.MkdirTemp(c.Files.root, ".run-")
	if err != nil {
		return RunResult{}, &CLIError{Operation: "executable stage"}
	}
	defer os.RemoveAll(stage)
	program := filepath.Join(stage, spec.Binary)
	if err = os.WriteFile(program, data, 0700); err != nil {
		return RunResult{}, &CLIError{Operation: "executable stage"}
	}
	check, err := os.ReadFile(program)
	if err != nil || sha256.Sum256(check) != sha256.Sum256(data) {
		return RunResult{}, ErrArtifactUnverified
	}
	if err = c.Files.check(ctx); err != nil {
		return RunResult{}, err
	}
	if err = c.Guard(ctx); err != nil {
		return RunResult{}, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, program, spec.Args...)
	cmd.Dir = c.Files.root
	cmd.Env = []string{}
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return RunResult{}, &CLIError{Operation: "start"}
	}
	// No second dispatch occurs even if a wait, lifecycle check or read fails.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if spec.WindowsWait > 0 {
		timer := time.NewTimer(spec.WindowsWait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return RunResult{}, &CLIError{Dispatched: true, Operation: "cancelled"}
		case <-timer.C:
		}
		select {
		case err = <-done:
		default:
			cancel()
			<-done
			return RunResult{OwnTimeout: true, ExitCode: -1}, nil
		}
	} else {
		err = <-done
	}
	if ctx.Err() != nil {
		return RunResult{}, &CLIError{Dispatched: true, Operation: "cancelled"}
	}
	if err == nil {
		return RunResult{Completed: true}, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ProcessState != nil {
		return RunResult{Completed: true, ExitCode: exit.ExitCode()}, nil
	}
	return RunResult{}, &CLIError{Dispatched: true, Operation: "wait"}
}
