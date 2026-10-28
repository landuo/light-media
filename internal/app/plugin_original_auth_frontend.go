package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// The original grants anonymous access only to the frontend directory declared
// by an authentication provider of a currently enabled plugin. Provider.enabled
// controls the login button, not this directory check. Never trust a caller URL.
func (a *App) originalAuthFrontend(ctx context.Context, instance string) (*originalFrontendDescriptor, error) {
	deny := func() (*originalFrontendDescriptor, error) {
		return nil, &APIError{"UNAUTHORIZED", "请先登录", http.StatusUnauthorized}
	}
	if !pluginStorageID.MatchString(instance) {
		return deny()
	}
	providers, err := a.originalPluginAuthProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, provider := range providers {
		var pluginID string
		var remote struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		}
		if json.Unmarshal(provider["plugin_id"], &pluginID) != nil || pluginID != instance ||
			json.Unmarshal(provider["remote"], &remote) != nil || remote.ID != instance {
			continue
		}
		value, err := a.originalPluginFrontend(ctx, instance)
		if err != nil {
			return nil, err
		}
		descriptor, ok := value.(originalFrontendDescriptor)
		if !ok || remote.URL != descriptor.EntryPath {
			return deny()
		}
		return &descriptor, nil
	}
	return deny()
}

func (a *App) serveOriginalAuthFrontendDescriptor(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/api/plugins/") || len(parts) != 2 || parts[1] != "frontend" || r.Method != http.MethodGet {
		return false
	}
	descriptor, err := a.originalAuthFrontend(r.Context(), parts[0])
	if err != nil {
		a.writeError(w, r, err)
	} else {
		writeJSON(w, r, descriptor)
	}
	return true
}
