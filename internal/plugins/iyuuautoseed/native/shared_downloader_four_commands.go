package download

import (
	"context"
	"net/http"
	"net/url"
)

// AddTags is the fixed qbittorrent-api torrents_add_tags port, not its
// replace-tags operation. Source wrappers decide None/bool, not this layer.
func (q *sharedQBCommands) AddTags(ctx context.Context, ids, tags any) (any, error) {
	return q.owner.run(ctx, func(ctx context.Context) (any, error) {
		hashes, err := commandJoined(ids, "|", false)
		if err != nil {
			return nil, err
		}
		labels, err := commandJoined(tags, ",", false)
		if err != nil {
			return nil, err
		}
		version, err := q.version(ctx)
		if err != nil {
			return nil, err
		}
		supported, err := iyuuVersionAtLeast(version, 2, 3)
		if err != nil {
			return nil, err
		}
		if !supported {
			// The pinned constructor leaves RAISE_NOTIMPLEMENTEDERROR false.
			// The vendor returns None without dispatching the selected POST.
			return nil, nil
		}
		form := url.Values{}
		if hashes != nil {
			form.Set("hashes", *hashes)
		}
		if labels != nil {
			form.Set("tags", *labels)
		}
		_, err = q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, "torrents/addTags", form.Encode(), "application/x-www-form-urlencoded")
		return nil, err
	})
}

// SetCategory preserves the native nullable field. The original wrapper's
// falsey-hash guard and category-or-empty conversion belong to its leaf.
func (q *sharedQBCommands) SetCategory(ctx context.Context, ids, category any) (any, error) {
	return q.owner.run(ctx, func(ctx context.Context) (any, error) {
		hashes, err := commandJoined(ids, "|", false)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		if hashes != nil {
			form.Set("hashes", *hashes)
		}
		if err = commandSet(form, "category", category); err != nil {
			return nil, err
		}
		_, err = q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, "torrents/setCategory", form.Encode(), "application/x-www-form-urlencoded")
		return nil, err
	})
}

func (q *sharedQBCommands) Stop(ctx context.Context, ids any) (any, error) {
	return q.owner.run(ctx, func(ctx context.Context) (any, error) {
		// torrents_pause aliases torrents_stop in the pinned vendor. Unlike
		// addTags, its version lookup precedes selector preparation.
		version, err := q.version(ctx)
		if err != nil {
			return nil, err
		}
		modern, err := iyuuVersionAtLeast(version, 2, 11)
		if err != nil {
			return nil, err
		}
		hashes, err := commandJoined(ids, "|", false)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		if hashes != nil {
			form.Set("hashes", *hashes)
		}
		endpoint := "pause"
		if modern {
			endpoint = "stop"
		}
		_, err = q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, "torrents/"+endpoint, form.Encode(), "application/x-www-form-urlencoded")
		return nil, err
	})
}

func (t *sharedTRCommands) Stop(ctx context.Context, ids any) (any, error) {
	// Reuse the existing exact require_ids parser, readonly session handshake,
	// selected single dispatch, private guard/lock and unknown-effect proof.
	return t.control(ctx, "torrent-stop", ids)
}
