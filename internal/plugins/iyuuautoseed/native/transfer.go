package download

// Fixed transport operations consumed by TorrentTransfer1.10.3. They neither
// alter IYUU selection nor expose a generic delete/RPC capability to workers.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// TransferConnected projects the source downloader wrapper's is_inactive
// state through fixed, read-only authentication/session calls. It grants no
// torrent selector or generic RPC access.
func (c *IYUUClient) TransferConnected(ctx context.Context) error {
	if e := c.client.nativeLock(ctx); e != nil {
		return e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		_, e := c.webVersionLocked(ctx)
		return e
	}
	return c.client.trConnect(ctx)
}

// TransferTorrents adds the source wrapper's seed_pending status(5). IYUU's
// own completed source predicate remains unchanged.
func (c *IYUUClient) TransferTorrents(ctx context.Context, hashes []string, completed bool) (json.RawMessage, error) {
	if c.client.config.Kind == "qbittorrent" {
		return c.Torrents(ctx, hashes, completed)
	}
	raw, e := c.Torrents(ctx, hashes, false)
	if e != nil || !completed {
		return raw, e
	}
	rows, e := nativeObjectList(raw, "transfer_torrents")
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, r := range rows {
		status, _ := r["status"].(json.Number)
		if status == "5" || status == "6" {
			out = append(out, r)
		}
	}
	return json.Marshal(out)
}

// TransferDeleteTask is always data-less. A caller cannot select deleteFiles or
// delete-local-data, and the exact public call's dispatch proof covers session
// negotiation and authentication refresh as well as the selected mutation.
func (c *IYUUClient) TransferDeleteTask(ctx context.Context, hash string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	id, e := c.client.validHash(hash)
	if e != nil {
		return out, e
	}
	if e = c.client.nativeLock(ctx); e != nil {
		return out, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		_, e = c.client.qbSelected(ctx, http.MethodPost, "torrents/delete", url.Values{"hashes": {id}, "deleteFiles": {"false"}}.Encode(), "application/x-www-form-urlencoded")
	} else {
		if e = c.client.trConnect(ctx); e == nil {
			_, e = c.client.trSelectedRPC(ctx, "torrent_remove", map[string]any{"ids": []string{id}, "delete_local_data": false}, c.client.trDialect)
		}
	}
	if e != nil {
		out.Outcome = "unknown"
		return out, e
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}

func TransferContentHashes(content []byte, kind string) ([]string, error) {
	return torrentHashes(content, kind)
}

// TransferObservedTask decodes only native fields consumed by the source.
// Existing raw status is not confused with transmission-rpc.Status.stopped.
func TransferObservedTask(row map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range row {
		out[k] = v
	}
	if stopped, ok := row["status_stopped"].(bool); ok {
		out["status"] = map[string]any{"stopped": stopped}
	}
	return out
}
func TransferValidHash(hash string) bool {
	if len(hash) != 40 {
		return false
	}
	for _, r := range hash {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
