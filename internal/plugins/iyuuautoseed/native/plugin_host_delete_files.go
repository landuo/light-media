package download

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// PluginHostFiles keeps the existing origin-bound transport and owner guard.
func (c *IYUUClient) PluginHostFiles(ctx context.Context, hash string) ([]File, error) {
	return c.client.Files(ctx, hash)
}

// PluginHostRemoveOwnedFiles is called only after the trusted App has frozen
// exact owned tasks and their authorized local file manifests. There is no
// worker-provided delete-data boolean or arbitrary native RPC entry point.
func (c *IYUUClient) PluginHostRemoveOwnedFiles(ctx context.Context, hashes []string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	if len(hashes) == 0 || c.options.Guard == nil {
		return out, failure("plugin_delete_files", "missing_owned_selector")
	}
	ids := make([]string, len(hashes))
	for i, hash := range hashes {
		ids[i], err = c.client.validHash(hash)
		if err != nil {
			return out, err
		}
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		_, err = c.client.qbSelected(ctx, http.MethodPost, "torrents/delete", url.Values{"hashes": {strings.Join(ids, "|")}, "deleteFiles": {"true"}}.Encode(), "application/x-www-form-urlencoded")
	} else if err = c.client.trConnect(ctx); err == nil {
		_, err = c.client.trSelectedRPC(ctx, "torrent_remove", map[string]any{"ids": ids, "delete_local_data": true}, c.client.trDialect)
	}
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}
