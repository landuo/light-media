package download

import (
	"context"
	"net/http"
	"net/url"
)

// SourceRemoveKeepData implements MoviePilot's remove_torrents primitive for
// one already-authorized exact hash. Unlike interactive Control it does not
// infer a different default instance or skip the source call because a lookup
// in that default is empty. It can never request deletion of local data.
// A nil error is a protocol acknowledgement, not proof of observed absence.
func (c *Client) SourceRemoveKeepData(ctx context.Context, hash string) error {
	hash, err := c.validHash(hash)
	if err != nil {
		return beforeMutation(err)
	}
	if err = c.nativeLock(ctx); err != nil {
		return beforeMutation(err)
	}
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		_, err = c.qb(ctx, http.MethodPost, "torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {"false"}}.Encode(), "application/x-www-form-urlencoded")
		return err
	}
	if err = c.trConnect(ctx); err != nil {
		return beforeMutation(err)
	}
	_, err = c.trRPC(ctx, "torrent_remove", map[string]any{"ids": []string{hash}, "delete_local_data": false}, c.trDialect)
	return err
}
