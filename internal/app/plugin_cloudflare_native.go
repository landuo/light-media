package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
)

type cloudflareAssetProof struct{ Kind, Version, Archive, ArchiveSHA, AssetAPI, Binary, BinarySHA, Module, GoVersion, VCSRevision, GOOS, GOARCH, CGOEnabled, VCSModified string }
type cloudflareOfficialAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	ID     int64  `json:"id"`
	URL    string `json:"browser_download_url"`
}
type cloudflareOfficialRelease struct {
	Tag    string                    `json:"tag_name"`
	Assets []cloudflareOfficialAsset `json:"assets"`
}
type cloudflareNative struct {
	c            *cloudflareCall
	native       *cf.NativeRuntime
	files        *cf.OwnedFiles
	assets       map[string]cloudflareOfficialAsset
	client       *http.Client
	proxied      *http.Client
	httpsProxy   bool
	closeNetwork func()
	proof        *cloudflareAssetProof
}

var cloudflareTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+._A-Za-z0-9]*)$`)
var cloudflareAssetAPI = regexp.MustCompile(`^https://api\.github\.com/repos/XIU2/CloudflareSpeedTest/releases/assets/[1-9][0-9]*$`)
var cloudflareSourceCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

func cloudflarePlatform() cf.Platform {
	machine := "aarch64"
	if runtime.GOARCH == "amd64" {
		machine = "x86_64"
	}
	return cf.Platform{OS: runtime.GOOS, Machine: machine}
}
func cloudflareArchiveAllowed(name string) bool {
	switch name {
	case "cfst_linux_amd64.tar.gz", "cfst_linux_arm64.tar.gz", "cfst_darwin_amd64.zip", "cfst_darwin_arm64.zip", "cfst_windows_amd64.zip":
		return true
	}
	return false
}
func cloudflareSHA(s string) ([32]byte, error) {
	var out [32]byte
	b, e := hex.DecodeString(s)
	if e != nil || len(b) != 32 {
		return out, cf.ErrArtifactUnverified
	}
	copy(out[:], b)
	return out, nil
}
func validCloudflareAsset(p cloudflareAssetProof) bool {
	return p.Kind == "github-release-digest/v1" && cloudflareTag.MatchString(p.Version) && cloudflareArchiveAllowed(p.Archive) && pluginStorageDigest.MatchString(p.ArchiveSHA) && pluginStorageDigest.MatchString(p.BinarySHA) && (p.Binary == "cfst" || p.Binary == "cfst.exe") && p.Module == "github.com/XIU2/CloudflareSpeedTest" && cloudflareAssetAPI.MatchString(p.AssetAPI) && p.GoVersion != "" && cloudflareSourceCommit.MatchString(p.VCSRevision) && p.CGOEnabled == "0" && p.VCSModified == "false" && p.GOOS == runtime.GOOS && p.GOARCH == runtime.GOARCH
}
func newCloudflareNative(ctx context.Context, c *cloudflareCall) (cf.Runtime, error) {
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	root := filepath.Join(c.o.a.Dir, "plugin-private", "CloudflareSpeedTest", c.o.a.pluginStore.noticeID("cloudflare-data", c.o.instance), "generation-"+strconv.FormatInt(c.o.generation, 10))
	files, e := cf.NewOwnedFiles(root, c.o.a.pluginStore.noticeID("cloudflare-owner", c.o.instance, c.o.generation))
	if e != nil {
		return nil, e
	}
	client, proxyClient, httpsProxy, closeNetwork, e := c.o.a.cloudflareNetworkClients()
	if e != nil {
		return nil, e
	}
	completed := false
	defer func() {
		if !completed {
			closeNetwork()
		}
	}()
	clientCopy := *client
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("Cloudflare official redirect refused")
		}
		switch strings.ToLower(req.URL.Hostname()) {
		case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		default:
			return errors.New("Cloudflare official redirect host refused")
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return nil
	}
	proxyCopy := *proxyClient
	proxyCopy.CheckRedirect = clientCopy.CheckRedirect
	n := &cloudflareNative{c: c, files: files, assets: map[string]cloudflareOfficialAsset{}, client: &clientCopy, proxied: &proxyCopy, httpsProxy: httpsProxy, closeNetwork: closeNetwork}
	pins := cf.Pins{Archives: map[string][32]byte{}, Binaries: map[string][][32]byte{}}
	s, _, e := c.o.a.pluginStore.cloudflareRead(ctx, c.o.a.Store.DB, c.o.instance, c.o.generation)
	if e != nil {
		return nil, e
	}
	for _, proof := range s.Artifacts {
		if !validCloudflareAsset(proof) {
			return nil, cloudflareEvidenceError()
		}
		hash, e := cloudflareSHA(proof.BinarySHA)
		if e != nil {
			return nil, e
		}
		pins.Binaries[proof.Binary] = append(pins.Binaries[proof.Binary], hash)
	}
	n.native = &cf.NativeRuntime{Files: files, Target: cloudflarePlatform(), Pins: pins, Guard: c.Guard}
	n.native.CLI = &cf.NativeCLI{Files: files, Pins: pins, Guard: c.Guard}
	completed = true
	return n, nil
}

func (n *cloudflareNative) Close() {
	if n.closeNetwork != nil {
		n.closeNetwork()
		n.closeNetwork = nil
	}
}
func (n *cloudflareNative) dataRoot() string {
	return filepath.Join(n.c.o.a.Dir, "plugin-private", "CloudflareSpeedTest", n.c.o.a.pluginStore.noticeID("cloudflare-data", n.c.o.instance), "generation-"+strconv.FormatInt(n.c.o.generation, 10))
}
func (n *cloudflareNative) Platform() cf.Platform { return n.native.Platform() }
func (n *cloudflareNative) Exists(ctx context.Context, ref cf.FileRef) (bool, error) {
	return n.native.Exists(ctx, ref)
}
func (n *cloudflareNative) Ensure(ctx context.Context) error { return n.native.Ensure(ctx) }
func (n *cloudflareNative) Reset(ctx context.Context) error  { return n.native.Reset(ctx) }
func (n *cloudflareNative) Remove(ctx context.Context, name string) error {
	return n.native.Remove(ctx, name)
}
func (n *cloudflareNative) Run(ctx context.Context, spec cf.RunSpec) (cf.RunResult, error) {
	return n.native.Run(ctx, spec)
}
func (n *cloudflareNative) ReadResult(ctx context.Context) ([]byte, error) {
	return n.native.ReadResult(ctx)
}

func (n *cloudflareNative) release(ctx context.Context, endpoint string, proxy bool) (*cloudflareOfficialRelease, error) {
	if e := n.c.Guard(ctx); e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Light-Media-CloudflareSpeedTest/1.5.1")
	client := *n.client
	if proxy && n.proxied != nil {
		client = *n.proxied
	}
	if client.Timeout == 0 || client.Timeout > 20*time.Second {
		client.Timeout = 20 * time.Second
	}
	response, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, nil
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if e != nil {
		return nil, nil
	}
	if len(body) > 2<<20 {
		return nil, &cf.SourceError{Class: "ValueError", Operation: "official release size"}
	}
	var release cloudflareOfficialRelease
	if json.Unmarshal(body, &release) != nil {
		return nil, &cf.SourceError{Class: "JSONDecodeError", Operation: "official release"}
	}
	if !cloudflareTag.MatchString(release.Tag) {
		return nil, &cf.SourceError{Class: "KeyError", Operation: "official release tag_name"}
	}
	for _, a := range release.Assets {
		if !cloudflareArchiveAllowed(a.Name) {
			continue
		}
		if a.ID <= 0 || a.URL != cf.ReleasePrefix+"/"+release.Tag+"/"+a.Name || !strings.HasPrefix(a.Digest, "sha256:") {
			return nil, cf.ErrArtifactUnverified
		}
		hash, e := cloudflareSHA(strings.TrimPrefix(a.Digest, "sha256:"))
		if e != nil {
			return nil, e
		}
		n.assets[a.URL] = a
		n.native.Pins.Archives[a.URL] = hash
	}
	return &release, nil
}
func (n *cloudflareNative) Latest(ctx context.Context, proxy bool) (cf.ReleaseResponse, error) {
	release, e := n.release(ctx, cf.ReleaseAPI, proxy)
	if e != nil || release == nil {
		return cf.ReleaseResponse{}, e
	}
	return cf.ReleaseResponse{Available: true, Tag: release.Tag}, nil
}
func (n *cloudflareNative) asset(ctx context.Context, req cf.InstallRequest) (cloudflareOfficialAsset, error) {
	if !cloudflareTag.MatchString(req.Version) || !cloudflareArchiveAllowed(req.Archive) || req.URL != cf.ReleasePrefix+"/"+req.Version+"/"+req.Archive {
		return cloudflareOfficialAsset{}, cf.ErrArtifactUnverified
	}
	if asset, ok := n.assets[req.URL]; ok {
		return asset, nil
	}
	endpoint := "https://api.github.com/repos/XIU2/CloudflareSpeedTest/releases/tags/" + url.PathEscape(req.Version)
	release, e := n.release(ctx, endpoint, false)
	if e == nil && release == nil {
		release, e = n.release(ctx, endpoint, true)
	}
	if e != nil {
		return cloudflareOfficialAsset{}, e
	}
	if release == nil || release.Tag != req.Version {
		return cloudflareOfficialAsset{}, cf.ErrArtifactUnverified
	}
	asset, ok := n.assets[req.URL]
	if !ok {
		return asset, cf.ErrArtifactUnverified
	}
	return asset, nil
}
func (n *cloudflareNative) Download(ctx context.Context, req cf.InstallRequest) error {
	_, e := n.asset(ctx, req)
	if e != nil {
		return e
	}
	if e = n.c.Guard(ctx); e != nil {
		return e
	}
	// Match the original archive routing. The trusted official metadata/digest
	// above still authenticates the exact bytes returned through either route.
	endpoint, useProxy, e := cloudflareDownloadRoute(req, n.httpsProxy)
	if e != nil {
		return e
	}
	r, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return e
	}
	r.Header.Set("Accept", "application/octet-stream")
	r.Header.Set("User-Agent", "Light-Media-CloudflareSpeedTest/1.5.1")
	client := n.client
	if useProxy && n.proxied != nil {
		client = n.proxied
	}
	response, e := client.Do(r)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 128<<20+1))
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	if len(data) > 128<<20 {
		return cf.ErrArtifactUnverified
	}
	if e = n.c.Guard(ctx); e != nil {
		return e
	}
	return cf.MaterializeApprovedArchive(ctx, n.files, req, data, n.native.Pins)
}
func (n *cloudflareNative) Extract(ctx context.Context, req cf.InstallRequest) error {
	asset, e := n.asset(ctx, req)
	if e != nil {
		return e
	}
	// Read only the protected generation-owned archive and authenticate it in
	// the package helper before normalizing official platform layout.
	archivePath := filepath.Join(n.dataRoot(), req.Archive)
	info, e := os.Lstat(archivePath)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return cf.ErrStorageScope
	}
	data, e := os.ReadFile(archivePath)
	if e != nil {
		return e
	}
	if e = cf.ExtractApprovedArchive(ctx, n.files, req, data, n.native.Pins); e != nil {
		return e
	}
	binary := "cfst"
	if req.Platform.OS == "windows" {
		binary = "cfst.exe"
	}
	inspection, e := cf.InspectOwnedExecutable(ctx, n.files, binary)
	if e != nil {
		return e
	}
	digest, e := cloudflareSHA(inspection.SHA256)
	if e != nil {
		return e
	}
	n.native.Pins.Binaries[binary] = append(n.native.Pins.Binaries[binary], digest)
	n.proof = &cloudflareAssetProof{Kind: "github-release-digest/v1", Version: req.Version, Archive: req.Archive, ArchiveSHA: strings.TrimPrefix(asset.Digest, "sha256:"), AssetAPI: "https://api.github.com/repos/XIU2/CloudflareSpeedTest/releases/assets/" + strconv.FormatInt(asset.ID, 10), Binary: binary, BinarySHA: inspection.SHA256, Module: inspection.Module, GoVersion: inspection.GoVersion, VCSRevision: inspection.VCSRevision, GOOS: inspection.GOOS, GOARCH: inspection.GOARCH, CGOEnabled: inspection.CGOEnabled, VCSModified: inspection.VCSModified}
	if !validCloudflareAsset(*n.proof) {
		return fmt.Errorf("Cloudflare official executable verification failed")
	}
	return nil
}

// No arbitrary origin, path or binary is accepted at this boundary. The source
// source-install controls are retained, while provenance approval is automatic
// technical verification of the official repository/version/digest, not a new
// user confirmation step.
var _ cf.Runtime = (*cloudflareNative)(nil)
