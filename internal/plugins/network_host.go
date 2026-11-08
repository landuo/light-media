package plugins

import (
	"context"
	"errors"
)

const NetworkGetCapability = "network.get"

// These finite credential-free profiles reproduce the two public RequestUtils
// calls used by AutoSignIn Tjupt. Empty Profile preserves ordinary Network.Get.
const NetworkGetSourcePublicProfile = "source_public"
const NetworkGetSourceDoubanImageProfile = "source_douban_image"
const NetworkGetSourceRemoteIdentifiersProfile = "source_remote_identifiers"

func validNetworkGetProfile(request NetworkGetRequestV1) bool {
	switch request.Profile {
	case "":
		return true
	case NetworkGetSourcePublicProfile, NetworkGetSourceDoubanImageProfile, NetworkGetSourceRemoteIdentifiersProfile:
		return !request.GitHubHeaders
	default:
		return false
	}
}

// NetworkGetRequestV1 selects a finite GET. Empty Profile retains the saved
// host proxy/15s mode; the two source profiles are env-only/default20s.
// The RemoteIdentifiers profile uses the saved host proxy/15s mode and selects
// headers on the host from the source URL. The host never returns its stored
// credentials or accepts arbitrary headers from the worker.
// GitHubHeaders uses the global GitHub credential only for an official HTTPS
// origin; it does not select repository tokens or the package mirror strategy.
type NetworkGetRequestV1 struct {
	URL           string `json:"url"`
	Profile       string `json:"profile,omitempty"`
	GitHubHeaders bool   `json:"github_headers,omitempty"`
}

type NetworkResponseV1 struct {
	StatusCode  int    `json:"status_code"`
	Content     []byte `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

type NetworkGetResultV1 struct {
	SchemaVersion int                `json:"schema_version"`
	Response      *NetworkResponseV1 `json:"response"`
}

type Network struct{ Host HostCaller }

func (n Network) Get(ctx context.Context, request NetworkGetRequestV1) (NetworkGetResultV1, error) {
	var out NetworkGetResultV1
	if !validNetworkGetProfile(request) {
		return out, errors.New("invalid host network profile")
	}
	err := hostBusinessCall(ctx, n.Host, NetworkGetCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Response != nil && (out.Response.StatusCode < 100 || out.Response.StatusCode > 999 || out.Response.Content == nil)) {
		err = errors.New("invalid host network response")
	}
	return out, err
}
