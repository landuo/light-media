package app

import (
	"context"
	"encoding/json"
	"errors"
	transfer "light-media/internal/plugins/torrenttransfer"
	"time"
)

const transferSourceFailed = "TORRENT_TRANSFER_SOURCE_FAILED"

func transferResultErrorCode(raw json.RawMessage) string {
	var result map[string]json.RawMessage
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	var code string
	if json.Unmarshal(result["source_error"], &code) == nil && code == transferSourceFailed {
		return code
	}
	return ""
}

// Only the real native execution stack can publish a source failure. It first
// revalidates the complete anchored journal: a pending/unknown mutation, lost
// evidence, stale replay or failed verification can never become known failure.
// Prior confirmed effects stay in their original steps and source checkpoint.
func (a *App) transferKnownFailureContext(ctx context.Context, owner *pluginTransferOwner, run PluginRun, cause error) (context.Context, context.CancelFunc, bool) {
	if cause == nil || errors.Is(cause, transfer.ErrUnconfirmed) || errors.Is(cause, transfer.ErrJournalIntegrity) || errors.Is(cause, transfer.ErrReplayDiverged) {
		return ctx, func() {}, false
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	unknown, e := owner.journal.HasUnconfirmed(finish, run.InstanceID, uint64(run.Generation))
	if e != nil || unknown {
		cancel()
		return ctx, func() {}, false
	}
	return finish, cancel, true
}

func (a *App) transferConfigurationFailure(ctx context.Context, owner *pluginTransferOwner, in PluginInstallation, run *PluginRun, jr *transfer.JournalRun, cp transfer.RuntimeCheckpoint, cfg transfer.Config, cause error) (json.RawMessage, error) {
	finish, cancel, known := a.transferKnownFailureContext(ctx, owner, *run, cause)
	if !known {
		return nil, cause
	}
	defer cancel()
	cp.SourceError = transferSourceFailed
	result, e := a.publishTransferConfiguration(finish, owner, in, run, jr, cp, cfg, nil)
	if e != nil {
		return nil, e
	}
	return result, bad(transferSourceFailed, "转移做种初始化失败，已保留确定结果，可修正配置后重试")
}
