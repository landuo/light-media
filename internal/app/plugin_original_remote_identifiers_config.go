package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

const privateIdentifiersURLs = "[已保存的私有订阅地址；保持此内容可保留，清空可删除]"

func maskRemoteIdentifiersURLs(fields map[string]json.RawMessage) bool {
	var addresses string
	if json.Unmarshal(fields["file_urls"], &addresses) != nil || !remoteIdentifiersPrivate(addresses) {
		return false
	}
	fields["file_urls"] = mustCleanSeedJSON(privateIdentifiersURLs)
	return true
}

// Preserve the prior authenticated form's placeholder and never expose a
// credential-bearing URL in the general config response or native form.
func remoteIdentifiersPrivate(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		u, err := url.Parse(strings.TrimSpace(line))
		if err != nil || originalRemoteIdentifiersURLPrivate(u) {
			return true
		}
	}
	return false
}

func originalRemoteIdentifiersURLPrivate(u *url.URL) bool {
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return true
	}
	for _, part := range strings.Split(u.Path, "/") {
		if len(part) >= 32 && !strings.Contains(part, ".") {
			return true
		}
	}
	return false
}

func (a *App) restoreRemoteIdentifiersURLs(ctx context.Context, instance string, fields map[string]json.RawMessage) error {
	var addresses string
	if json.Unmarshal(fields["file_urls"], &addresses) != nil || addresses != privateIdentifiersURLs {
		return nil
	}
	raw, _, err := a.readOriginalBaseConfigForInit(ctx, instance)
	if err != nil {
		return err
	}
	var previous map[string]json.RawMessage
	if json.Unmarshal(raw, &previous) != nil || json.Unmarshal(previous["file_urls"], &addresses) != nil || !remoteIdentifiersPrivate(addresses) {
		return bad("INVALID_IDENTIFIERS_URL", "没有可保留的私有订阅地址")
	}
	fields["file_urls"] = previous["file_urls"]
	return nil
}
