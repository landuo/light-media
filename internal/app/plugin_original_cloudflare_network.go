package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
)

const originalCloudflareGitHub = "https://api.github.com/repos/XIU2/CloudflareSpeedTest"
const originalCloudflareArchiveLimit = 128 << 20
const originalCloudflareMetadataLimit = 2 << 20

var originalCloudflareTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+[-+._A-Za-z0-9]*$`)
var errOriginalCloudflareAssetUnavailable = errors.New("official CloudflareSpeedTest asset unavailable")

type originalCloudflareAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	ID     int64  `json:"id"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
}

type originalCloudflareRelease struct {
	Tag    string                    `json:"tag_name"`
	Assets []originalCloudflareAsset `json:"assets"`
}

type originalCloudflareNetwork struct {
	direct, proxied *http.Client
	httpsProxy      bool
	owned           []*http.Client
}

func (a *App) originalCloudflareNetwork() (originalCloudflareNetwork, error) {
	var n originalCloudflareNetwork
	config, err := a.proxyConfig()
	if err != nil {
		return n, err
	}
	n.httpsProxy = config.HTTPS != ""
	if a.cloudflareTestClient != nil {
		client := *a.cloudflareTestClient
		client.CheckRedirect = originalCloudflareRedirect
		n.direct, n.proxied = &client, &client
		return n, nil
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	n.direct = &http.Client{Transport: base, CheckRedirect: originalCloudflareRedirect}
	n.proxied = n.direct
	n.owned = append(n.owned, n.direct)
	if config.HTTP != "" || config.HTTPS != "" {
		transport, err := config.Transport(base)
		if err != nil {
			n.close()
			return originalCloudflareNetwork{}, err
		}
		n.proxied = &http.Client{Transport: transport, CheckRedirect: originalCloudflareRedirect}
		n.owned = append(n.owned, n.proxied)
	}
	return n, nil
}

func (n originalCloudflareNetwork) close() {
	for _, client := range n.owned {
		client.CloseIdleConnections()
	}
}

func originalCloudflareRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 || req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Port() != "" && req.URL.Port() != "443") {
		return cf.ErrArtifactUnverified
	}
	switch strings.ToLower(req.URL.Hostname()) {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return nil
	}
	return cf.ErrArtifactUnverified
}

func (n originalCloudflareNetwork) get(ctx context.Context, endpoint, accept string, proxy bool, timeout time.Duration, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, cf.ErrArtifactUnverified
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "Light-Media-CloudflareSpeedTest/1.5.1")
	client := n.direct
	if proxy {
		client = n.proxied
	}
	call := *client
	if call.Timeout == 0 || call.Timeout > timeout {
		call.Timeout = timeout
	}
	response, err := call.Do(req)
	if err != nil {
		if errors.Is(err, cf.ErrArtifactUnverified) {
			return nil, cf.ErrArtifactUnverified
		}
		return nil, ctx.Err()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil
	}
	if response.ContentLength > limit {
		return nil, cf.ErrArtifactUnverified
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, ctx.Err()
	}
	if int64(len(data)) > limit {
		return nil, cf.ErrArtifactUnverified
	}
	return data, nil
}

func originalCloudflareDigest(text string) ([32]byte, error) {
	var out [32]byte
	if !pluginStorageDigest.MatchString(text) {
		return out, cf.ErrArtifactUnverified
	}
	_, err := hex.Decode(out[:], []byte(text))
	return out, err
}

func originalCloudflareValidTag(tag string) bool {
	return len(tag) <= 64 && originalCloudflareTag.MatchString(tag)
}

func (n *originalCloudflareNative) release(ctx context.Context, endpoint, tag string, proxy bool) (*originalCloudflareRelease, error) {
	data, err := n.network.get(ctx, endpoint, "application/vnd.github+json", proxy, 20*time.Second, originalCloudflareMetadataLimit)
	if err != nil || data == nil {
		return nil, err
	}
	var release originalCloudflareRelease
	if json.Unmarshal(data, &release) != nil {
		return nil, &cf.SourceError{Class: "JSONDecodeError", Operation: "official release"}
	}
	if !originalCloudflareValidTag(release.Tag) || (tag != "" && release.Tag != tag) {
		return nil, &cf.SourceError{Class: "KeyError", Operation: "official release tag_name"}
	}
	found := false
	for _, asset := range release.Assets {
		if asset.Name != n.archiveName {
			continue
		}
		if found || asset.ID <= 0 || asset.Size <= 0 || asset.Size > originalCloudflareArchiveLimit ||
			asset.URL != cf.ReleasePrefix+"/"+release.Tag+"/"+asset.Name || !strings.HasPrefix(asset.Digest, "sha256:") {
			return nil, cf.ErrArtifactUnverified
		}
		digest, err := originalCloudflareDigest(strings.TrimPrefix(asset.Digest, "sha256:"))
		if err != nil {
			return nil, err
		}
		if previous, exists := n.assets[asset.URL]; exists && previous != asset {
			return nil, cf.ErrArtifactUnverified
		}
		n.assets[asset.URL] = asset
		n.runtime.Pins.Archives[asset.URL] = digest
		found = true
	}
	return &release, nil
}

func (n *originalCloudflareNative) latest(ctx context.Context, proxy bool) (cf.ReleaseResponse, error) {
	release, err := n.release(ctx, cf.ReleaseAPI, "", proxy)
	if err != nil || release == nil {
		return cf.ReleaseResponse{}, err
	}
	return cf.ReleaseResponse{Available: true, Tag: release.Tag}, nil
}

func (n *originalCloudflareNative) asset(ctx context.Context, req cf.InstallRequest) (originalCloudflareAsset, error) {
	if req.Platform != n.runtime.Target || req.Archive != n.archiveName ||
		req.URL != cf.ReleasePrefix+"/"+req.Version+"/"+req.Archive {
		return originalCloudflareAsset{}, cf.ErrArtifactUnverified
	}
	if req.Version == "None" {
		return originalCloudflareAsset{}, errOriginalCloudflareAssetUnavailable
	}
	if !originalCloudflareValidTag(req.Version) {
		return originalCloudflareAsset{}, cf.ErrArtifactUnverified
	}
	if asset, ok := n.assets[req.URL]; ok {
		return asset, nil
	}
	endpoint := originalCloudflareGitHub + "/releases/tags/" + url.PathEscape(req.Version)
	release, err := n.release(ctx, endpoint, req.Version, false)
	if err == nil && release == nil {
		release, err = n.release(ctx, endpoint, req.Version, true)
	}
	if err != nil {
		return originalCloudflareAsset{}, err
	}
	if asset, ok := n.assets[req.URL]; release != nil && ok {
		return asset, nil
	}
	return originalCloudflareAsset{}, errOriginalCloudflareAssetUnavailable
}

func (n *originalCloudflareNative) download(ctx context.Context, req cf.InstallRequest) error {
	asset, err := n.asset(ctx, req)
	if errors.Is(err, errOriginalCloudflareAssetUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	endpoint := asset.URL
	if !n.network.httpsProxy {
		// The source's optional Linux mirror transports only the already
		// approved public archive. It never supplies the trusted SHA-256.
		mirror, configured := os.LookupEnv("PROXY_HOST")
		if !configured {
			mirror = "https://ghfast.top"
		}
		u, err := url.Parse(mirror)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return cf.ErrArtifactUnverified
		}
		endpoint = strings.TrimRight(mirror, "/") + "/" + asset.URL
	}
	data, err := n.network.get(ctx, endpoint, "application/octet-stream", n.network.httpsProxy, 60*time.Second, asset.Size)
	if err != nil || data == nil {
		return err
	}
	if int64(len(data)) != asset.Size {
		return cf.ErrArtifactUnverified
	}
	if err := n.runtime.Guard(ctx); err != nil {
		return err
	}
	return cf.MaterializeApprovedArchive(ctx, n.runtime.Files, req, data, n.runtime.Pins)
}
