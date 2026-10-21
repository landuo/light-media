package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	transfer "light-media/internal/plugins/torrenttransfer"
)

func (p *PluginStore) transferQueueAdmissionTx(ctx context.Context, tx *sql.Tx, instance, plugin string) error {
	if !isTorrentTransfer(plugin) {
		return nil
	}
	_, e := p.transferTransitionParentTx(ctx, tx, instance)
	if errors.Is(e, transfer.ErrUnconfirmed) {
		return fmt.Errorf("%w: %w", bad("TORRENT_TRANSFER_UNCONFIRMED", "转移做种存在待核对执行，原证据已保留"), e)
	}
	if errors.Is(e, transfer.ErrJournalIntegrity) || errors.Is(e, transfer.ErrReplayDiverged) {
		return fmt.Errorf("%w: %w", bad("TORRENT_TRANSFER_EVIDENCE_MISSING", "转移做种执行证据缺失或无法校验"), e)
	}
	if e != nil {
		return e
	}
	var g int64
	if e = tx.QueryRowContext(ctx, "SELECT generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&g); e != nil {
		return e
	}
	root, _, e := p.transferRootReadTx(ctx, tx, instance, g)
	if e != nil {
		return e
	}
	if root.ReferenceProtocol != "native-downloader-name/v1" && root.ReferenceProtocol != reviewedTransferProtocol {
		return bad("TORRENT_TRANSFER_REFERENCES_UNCLAIMED", "原版下载器引用尚未确认映射")
	}
	return nil
}
func (p *PluginStore) pluginNativeQueueAdmissionTx(ctx context.Context, tx *sql.Tx, instance, plugin string) error {
	if plugin == "LarkMessager" {
		_, e := p.larkTransitionTx(context.WithValue(ctx, pluginLarkQueueKey{}, true), tx, instance)
		return e
	}
	if plugin == "ScrapeFileClean" {
		return p.scrapeCleanQueueAdmissionTx(ctx, tx, instance, plugin)
	}
	if isTorrentTransfer(plugin) {
		if e := p.transferQueueAdmissionTx(ctx, tx, instance, plugin); e != nil {
			return e
		}
		return p.requirePluginExecutionHandoffTx(ctx, tx, instance)
	}
	if e := p.iyuuQueueAdmissionTx(ctx, tx, instance, plugin); e != nil {
		return e
	}
	return p.requirePluginExecutionHandoffTx(ctx, tx, instance)
}
func pluginNativeUnconfirmedQueueAdmission(e error) bool {
	return pluginHandoffPending(e) || pluginErrorCodeInternal(e) == "LARK_UNCONFIRMED" || scrapeQueueRejected(e) || errors.Is(e, errBrushFlowUnknownAdmission) || iyuuUnconfirmedQueueAdmission(e) || (pluginErrorCodeInternal(e) == "TORRENT_TRANSFER_UNCONFIRMED" && errors.Is(e, transfer.ErrUnconfirmed) && !errors.Is(e, transfer.ErrJournalIntegrity) && !errors.Is(e, transfer.ErrReplayDiverged))
}
func (p *PluginStore) pluginNativeConfigWriteTx(ctx context.Context, tx *sql.Tx, instance string, revision int64, raw json.RawMessage) error {
	var plugin string
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin); e != nil {
		return e
	}
	if plugin == "LarkMessager" {
		_, e := p.larkTransitionTx(ctx, tx, instance)
		return e
	}
	if plugin == "BrushFlowLanduo" {
		return p.brushFlowConfigAdmissionTx(ctx, tx, instance, revision, raw)
	}
	if plugin == "ScrapeFileClean" {
		return p.scrapeCleanConfigWriteTx(ctx, tx, instance, revision, raw)
	}
	if isTorrentTransfer(plugin) {
		return p.transferConfigAdmissionTx(ctx, tx, instance, revision, raw)
	}
	return p.iyuuConfigWriteTx(ctx, tx, instance, revision, raw)
}
