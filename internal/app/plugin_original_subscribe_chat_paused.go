package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type originalChatPausedSearchPayload struct {
	ID          string `json:"id"`
	Revision    *int   `json:"revision"`
	Paused      bool   `json:"_chat_paused_search,omitempty"`
	Manual      bool   `json:"_chat_manual_search,omitempty"`
	LatestBatch string `json:"latest_batch,omitempty"`
}

func originalChatManualSubscriptionJob(j Job) bool {
	var payload originalChatPausedSearchPayload
	return j.Kind == "subscription" && j.ID != "" && json.Unmarshal(j.Payload, &payload) == nil &&
		payload.Manual && payload.ID != "" && payload.Revision != nil && payload.LatestBatch == ""
}

type originalChatPausedSearchKey struct{}
type originalChatPausedSearchGrant struct {
	app      *App
	id, job  string
	revision int
}

func originalChatPausedSearchJob(ctx context.Context, s Subscription) string {
	grant, _ := ctx.Value(originalChatPausedSearchKey{}).(originalChatPausedSearchGrant)
	if s.State == "paused" && grant.id == s.ID && grant.revision == s.Revision {
		return grant.job
	}
	return ""
}

func (a *App) subscriptionSearchState(ctx context.Context, s Subscription) bool {
	if s.State == "active" {
		return true
	}
	grant, _ := ctx.Value(originalChatPausedSearchKey{}).(originalChatPausedSearchGrant)
	return grant.app == a && originalChatPausedSearchJob(ctx, s) != ""
}

func (a *App) originalChatPausedSearchFromJob(ctx context.Context, j Job, key string, revision int) (context.Context, error) {
	if len(j.Payload) == 0 {
		return ctx, nil
	}
	var payload originalChatPausedSearchPayload
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return ctx, err
	}
	if !payload.Paused {
		return ctx, nil
	}
	if j.Kind != "subscription" || j.ID == "" || payload.ID != key || payload.Revision == nil || *payload.Revision != revision || payload.LatestBatch != "" {
		return ctx, bad("SUBSCRIPTION_CHANGED", "暂停订阅的手动搜索任务无法核对")
	}
	grant := originalChatPausedSearchGrant{app: a, id: key, job: j.ID, revision: revision}
	return context.WithValue(ctx, originalChatPausedSearchKey{}, grant), nil
}

func (a *App) originalChatPausedSearchForDownload(ctx context.Context, j Job, in Intent) (context.Context, error) {
	if in.SubscriptionID == "" || (j.Kind != "download" && j.Kind != "subscription_download") {
		return ctx, nil
	}
	if _, inherited := ctx.Value(originalChatPausedSearchKey{}).(originalChatPausedSearchGrant); inherited {
		ctx = context.WithValue(ctx, originalChatPausedSearchKey{}, originalChatPausedSearchGrant{})
	}
	var payload struct {
		ID   string `json:"id"`
		Root string `json:"_chat_paused_search_job"`
	}
	if len(j.Payload) == 0 {
		return ctx, nil
	}
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return ctx, err
	}
	if payload.Root == "" {
		return ctx, nil
	}
	if payload.ID != in.ID || payload.Root != j.ParentJobID {
		return ctx, bad("SUBSCRIPTION_CHANGED", "暂停订阅的下载来源无法核对")
	}
	return a.originalChatPausedSearchRoot(ctx, payload.Root, in.SubscriptionID, in.SubscriptionRevision)
}

func (a *App) originalChatPausedSearchRoot(ctx context.Context, root, key string, revision int) (context.Context, error) {
	if root == "" {
		return ctx, nil
	}
	parent, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, root))
	if errors.Is(err, sql.ErrNoRows) {
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	if parent.Kind != "subscription" || (parent.State != "running" && parent.State != "succeeded") {
		return ctx, nil
	}
	return a.originalChatPausedSearchFromJob(ctx, parent, key, revision)
}
