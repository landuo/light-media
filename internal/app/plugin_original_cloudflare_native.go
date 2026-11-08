package app

import (
	"context"
	"database/sql"
	"debug/elf"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"

	"golang.org/x/sys/unix"
)

const originalCloudflareProofKind = "plugin_cloudflare_engine_proof"

var originalCloudflareRevision = regexp.MustCompile(`^[a-f0-9]{40}$`)

type originalCloudflareEngineProof struct {
	SchemaVersion int
	Instance      string
	Generation    int64
	PluginVersion string
	Tag           string
	AssetID       int64
	Archive       string
	ArchiveSHA256 string
	Binary        cf.ExecutableInspection
}

type originalCloudflareNative struct {
	app         *App
	scope       originalBaseCallScope
	runtime     *cf.NativeRuntime
	network     originalCloudflareNetwork
	root        string
	rootInfo    os.FileInfo
	owner       string
	archiveName string
	assets      map[string]originalCloudflareAsset
	proof       *originalCloudflareEngineProof
	ran         bool
	completed   bool
}

func (n *originalCloudflareNative) Close() {
	if n != nil {
		n.network.close()
		n.network.owned = nil
	}
}

func (a *App) newOriginalCloudflareNative(ctx context.Context, scope originalBaseCallScope) (*originalCloudflareNative, error) {
	if err := originalCloudflareValidate(ctx, scope); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return nil, cf.ErrStorageScope
	}
	machine := "aarch64"
	if runtime.GOARCH == "amd64" {
		machine = "x86_64"
	}
	base, err := a.originalBaseDataPath(ctx, scope.installation.InstanceID)
	if err != nil {
		return nil, err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	baseFD, err := originalBaseOpenDirectory(-1, base)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(baseFD)
	name := ".cloudflare-engine-" + strconv.FormatInt(scope.installation.Generation, 10)
	fd, err := originalBaseEnsureDirectory(baseFD, name, true)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), name)
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(base, name)
	owner := a.pluginStore.noticeID("cloudflare-engine-owner", scope.installation.InstanceID, scope.installation.Generation)
	if marker, err := os.Lstat(filepath.Join(root, ".cloudflare-owner")); err == nil {
		if !marker.Mode().IsRegular() || marker.Size() != int64(len(owner)) {
			return nil, cf.ErrStorageScope
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files, err := cf.NewOwnedFiles(root, owner)
	if err != nil {
		return nil, err
	}
	n := &originalCloudflareNative{
		app: a, scope: scope, root: root, rootInfo: info, owner: owner, assets: make(map[string]originalCloudflareAsset),
		archiveName: "cfst_linux_" + runtime.GOARCH + ".tar.gz",
	}
	pins := cf.Pins{Archives: make(map[string][32]byte), Binaries: make(map[string][][32]byte)}
	n.runtime = &cf.NativeRuntime{Files: files, Target: cf.Platform{OS: "linux", Machine: machine}, Pins: pins, Guard: n.guard}
	n.runtime.CLI = &cf.NativeCLI{Files: files, Pins: pins, Guard: n.guard}
	if err = n.guard(ctx); err != nil {
		return nil, err
	}
	if err = n.restoreProof(ctx); err != nil {
		return nil, err
	}
	n.network, err = a.originalCloudflareNetwork()
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (n *originalCloudflareNative) guard(ctx context.Context) error {
	if err := originalCloudflareValidate(ctx, n.scope); err != nil {
		return err
	}
	info, err := os.Lstat(n.root)
	if err != nil || !info.IsDir() || !os.SameFile(n.rootInfo, info) {
		return cf.ErrStorageScope
	}
	return nil
}

func (n *originalCloudflareNative) proofKey() string {
	return n.app.pluginStore.noticeID("cloudflare-engine-proof", n.scope.installation.InstanceID, n.scope.installation.Generation)
}

func (n *originalCloudflareNative) validProof(p originalCloudflareEngineProof) bool {
	in := n.scope.installation
	return p.SchemaVersion == 1 && p.Instance == in.InstanceID && p.Generation == in.Generation && p.PluginVersion == in.Version &&
		originalCloudflareValidTag(p.Tag) && p.AssetID > 0 && p.Archive == n.archiveName &&
		pluginStorageDigest.MatchString(p.ArchiveSHA256) && pluginStorageDigest.MatchString(p.Binary.SHA256) &&
		p.Binary.Module == "github.com/XIU2/CloudflareSpeedTest" && strings.HasPrefix(p.Binary.GoVersion, "go1.") &&
		originalCloudflareRevision.MatchString(p.Binary.VCSRevision) && p.Binary.VCSModified == "false" && p.Binary.CGOEnabled == "0" &&
		p.Binary.GOOS == runtime.GOOS && p.Binary.GOARCH == runtime.GOARCH
}

func (n *originalCloudflareNative) restoreProof(ctx context.Context) error {
	var sealed []byte
	if err := n.app.Store.Get(ctx, originalCloudflareProofKind, n.proofKey(), &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var proof originalCloudflareEngineProof
	if n.app.Store.Unseal(sealed, &proof) != nil || !n.validProof(proof) {
		return cf.ErrArtifactUnverified
	}
	return n.setProof(proof)
}

func (n *originalCloudflareNative) setProof(proof originalCloudflareEngineProof) error {
	pin, err := originalCloudflareDigest(proof.Binary.SHA256)
	if err != nil {
		return err
	}
	n.proof = &proof
	n.runtime.Pins.Binaries["cfst"] = [][32]byte{pin}
	return nil
}

func (n *originalCloudflareNative) exists(ctx context.Context, ref cf.FileRef) (bool, error) {
	switch ref.Name {
	case "cfst", "cfst.exe", "CloudflareST", "CloudflareST.exe", n.archiveName:
	default:
		return false, cf.ErrStorageScope
	}
	if ref.Backslash || ref.Name == "cfst.exe" || strings.HasPrefix(ref.Name, "CloudflareST") {
		return false, nil
	}
	exists, err := n.runtime.Exists(ctx, ref)
	if err != nil || !exists || ref.Name == n.archiveName {
		return exists, err
	}
	if n.proof == nil {
		return false, nil
	}
	installed, err := cf.InspectOwnedExecutable(ctx, n.runtime.Files, "cfst")
	return err == nil && installed == n.proof.Binary, nil
}

func (n *originalCloudflareNative) reset(ctx context.Context) error {
	if err := n.runtime.Reset(ctx); err != nil {
		return err
	}
	if err := n.app.Store.Delete(ctx, originalCloudflareProofKind, n.proofKey()); err != nil {
		return err
	}
	n.proof = nil
	delete(n.runtime.Pins.Binaries, "cfst")
	return nil
}

// The caller uses only the exact platform archive name or the fixed candidate
// lists. Open relative to the identified directory without following links.
func (n *originalCloudflareNative) openOwned(name string) (*os.File, error) {
	fd, err := originalBaseOpenDirectory(-1, n.root)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), n.root)
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !os.SameFile(info, n.rootInfo) {
		return nil, cf.ErrStorageScope
	}
	fileFD, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fileFD), name)
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, cf.ErrStorageScope
	}
	return file, nil
}

func (n *originalCloudflareNative) extract(ctx context.Context, req cf.InstallRequest) error {
	asset, err := n.asset(ctx, req)
	if err != nil {
		return err
	}
	if err := n.guard(ctx); err != nil {
		return err
	}
	archive, err := n.openOwned(n.archiveName)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(archive, asset.Size+1))
	closeErr := archive.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) != asset.Size {
		return cf.ErrArtifactUnverified
	}

	// Inspect staged official bytes before replacing the previous approved
	// generation's engine. An invalid update cannot displace that binary.
	stagePath, err := os.MkdirTemp(n.root, ".extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagePath)
	stage, err := cf.NewOwnedFiles(stagePath, n.owner)
	if err != nil {
		return err
	}
	if err := cf.ExtractApprovedArchive(ctx, stage, req, data, n.runtime.Pins); err != nil {
		return err
	}
	inspection, err := cf.InspectOwnedExecutable(ctx, stage, "cfst")
	if err != nil {
		return cf.ErrArtifactUnverified
	}
	engine, err := elf.Open(filepath.Join(stagePath, "cfst"))
	if err != nil {
		return cf.ErrArtifactUnverified
	}
	machine := engine.Machine
	engine.Close()
	if runtime.GOARCH == "amd64" && machine != elf.EM_X86_64 || runtime.GOARCH == "arm64" && machine != elf.EM_AARCH64 {
		return cf.ErrArtifactUnverified
	}

	in := n.scope.installation
	proof := originalCloudflareEngineProof{SchemaVersion: 1, Instance: in.InstanceID, Generation: in.Generation, PluginVersion: in.Version,
		Tag: req.Version, AssetID: asset.ID, Archive: req.Archive, ArchiveSHA256: strings.TrimPrefix(asset.Digest, "sha256:"), Binary: inspection}
	if !n.validProof(proof) {
		return cf.ErrArtifactUnverified
	}
	sealed, err := n.app.Store.Seal(proof)
	if err != nil {
		return err
	}
	if err := n.guard(ctx); err != nil {
		return err
	}
	if err := cf.ExtractApprovedArchive(ctx, n.runtime.Files, req, data, n.runtime.Pins); err != nil {
		return err
	}
	installed, err := cf.InspectOwnedExecutable(ctx, n.runtime.Files, "cfst")
	if err != nil || installed != proof.Binary {
		return cf.ErrArtifactUnverified
	}
	if err := n.guard(ctx); err != nil {
		return err
	}
	if err := n.app.Store.Put(ctx, originalCloudflareProofKind, n.proofKey(), sealed); err != nil {
		return err
	}
	return n.setProof(proof)
}

// Preserve the native command's options while preventing a worker or a
// positional flag terminator from changing the two owned file destinations.
func originalCloudflareNativeArgs(args []string) ([]string, bool) {
	if len(args) == 0 || len(args) > 128 {
		return nil, false
	}
	var input []string
	var output bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, assigned := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if !strings.HasPrefix(arg, "-") || len(arg) > 16<<10 {
			return nil, false
		}
		switch name {
		case "httping", "dd", "allip", "debug", "v", "h", "help":
			continue
		case "n", "t", "dn", "dt", "tp", "url", "httping-code", "cfcolo", "tl", "tll", "tlr", "sl", "p", "ip", "f", "o":
		default:
			return nil, false
		}
		if !assigned {
			i++
			if i >= len(args) || len(args[i]) > 16<<10 {
				return nil, false
			}
			value = args[i]
		}
		switch name {
		case "f":
			if value != "ip.txt" && value != "ipv6.txt" {
				return nil, false
			}
			input = append(input, value)
		case "o":
			if value != "result_hosts.txt" {
				return nil, false
			}
			output = true
		}
	}
	if len(input) == 0 {
		input = append(input, "ip.txt")
	}
	return input, output
}

func (n *originalCloudflareNative) run(ctx context.Context, spec cf.RunSpec) (cf.RunResult, error) {
	if n.ran || n.proof == nil || spec.Binary != "cfst" || spec.Result != "result_hosts.txt" || spec.WindowsWait != 0 {
		return cf.RunResult{}, cf.ErrArtifactUnverified
	}
	inputs, ok := originalCloudflareNativeArgs(spec.Args)
	if !ok {
		return cf.RunResult{}, cf.ErrStorageScope
	}
	for _, name := range inputs {
		input, err := n.openOwned(name)
		if err != nil {
			return cf.RunResult{}, err
		}
		input.Close()
	}
	// A completed child with no output cannot accidentally promote an IP
	// left over from an earlier run.
	if err := n.runtime.Remove(ctx, "result_hosts.txt"); err != nil {
		return cf.RunResult{}, err
	}
	n.ran = true
	result, err := n.runtime.Run(ctx, spec)
	n.completed = err == nil && result.Completed
	return result, err
}

type originalCloudflareWirePlatform struct {
	OS      string `json:"os"`
	Machine string `json:"machine"`
}

type originalCloudflareWireInstall struct {
	URL      string                         `json:"url"`
	Archive  string                         `json:"archive"`
	Version  string                         `json:"version"`
	Platform originalCloudflareWirePlatform `json:"platform"`
}

func (a *App) originalCloudflareNativeResource(ctx context.Context, scope originalBaseCallScope, op string, raw json.RawMessage) (json.RawMessage, error) {
	if err := originalCloudflareValidate(ctx, scope); err != nil {
		return nil, err
	}
	n := scope.cloudflare.native
	if n == nil {
		var err error
		n, err = a.newOriginalCloudflareNative(ctx, scope)
		if err != nil {
			return nil, err
		}
		scope.cloudflare.native = n
	}
	switch op {
	case "platform", "ensure", "reset", "read_result":
		if err := originalResourceDecode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		switch op {
		case "platform":
			p := n.runtime.Target
			return json.Marshal(originalCloudflareWirePlatform{OS: p.OS, Machine: p.Machine})
		case "ensure":
			return json.RawMessage("true"), n.runtime.Ensure(ctx)
		case "reset":
			return json.RawMessage("true"), n.reset(ctx)
		case "read_result":
			if !n.completed {
				return nil, cf.ErrStorageScope
			}
			data, err := n.runtime.ReadResult(ctx)
			if err != nil {
				return nil, err
			}
			return json.Marshal(struct {
				Content []byte `json:"content"`
			}{data})
		}
	case "exists":
		var request struct {
			Name      string `json:"name"`
			Backslash bool   `json:"backslash"`
		}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		exists, err := n.exists(ctx, cf.FileRef{Name: request.Name, Backslash: request.Backslash})
		if err != nil {
			return nil, err
		}
		return json.Marshal(exists)
	case "latest":
		var request struct {
			Proxy bool `json:"proxy"`
		}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		response, err := n.latest(ctx, request.Proxy)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Available bool `json:"available"`
			Tag       any  `json:"tag"`
		}{response.Available, response.Tag})
	case "download", "extract":
		var request originalCloudflareWireInstall
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		in := cf.InstallRequest{URL: request.URL, Archive: request.Archive, Version: request.Version,
			Platform: cf.Platform{OS: request.Platform.OS, Machine: request.Platform.Machine}}
		if op == "download" {
			return json.RawMessage("true"), n.download(ctx, in)
		}
		err := n.extract(ctx, in)
		var extraction *cf.ExtractionError
		if errors.As(err, &extraction) {
			return json.RawMessage("false"), nil
		}
		return json.RawMessage("true"), err
	case "remove":
		var request struct {
			Name string `json:"name"`
		}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Name != n.archiveName {
			return nil, cf.ErrStorageScope
		}
		return json.RawMessage("true"), n.runtime.Remove(ctx, request.Name)
	case "run":
		var request struct {
			Binary      string        `json:"binary"`
			Args        []string      `json:"args"`
			Result      string        `json:"result"`
			WindowsWait time.Duration `json:"windows_wait"`
		}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		result, err := n.run(ctx, cf.RunSpec{Binary: request.Binary, Args: request.Args, Result: request.Result, WindowsWait: request.WindowsWait})
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			ExitCode   int  `json:"exit_code"`
			Completed  bool `json:"completed"`
			OwnTimeout bool `json:"own_timeout"`
		}{result.ExitCode, result.Completed, result.OwnTimeout})
	}
	return nil, cf.ErrStorageScope
}
