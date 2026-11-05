package plugins

import (
	"context"
	"errors"
)

const NetworkGetCapability = "network.get"

// NetworkGetRequestV1 selects a finite GET using the host's configured proxy.
// Credentials and arbitrary headers are never transported into the worker.
// GitHubHeaders uses the global GitHub credential only for an official HTTPS
// origin; it does not select repository tokens or the package mirror strategy.
type NetworkGetRequestV1 struct {
	URL           string `json:"url"`
	GitHubHeaders bool   `json:"github_headers,omitempty"`
}

type NetworkResponseV1 struct {
	StatusCode int    `json:"status_code"`
	Content    []byte `json:"content"`
}

type NetworkGetResultV1 struct {
	SchemaVersion int                `json:"schema_version"`
	Response      *NetworkResponseV1 `json:"response"`
}

type Network struct{ Host HostCaller }

func (n Network) Get(ctx context.Context, request NetworkGetRequestV1) (NetworkGetResultV1, error) {
	var out NetworkGetResultV1
	err := hostBusinessCall(ctx, n.Host, NetworkGetCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Response != nil && (out.Response.StatusCode < 100 || out.Response.StatusCode > 599 || out.Response.Content == nil)) {
		err = errors.New("invalid host network response")
	}
	return out, err
}
