package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"light-media/internal/plugins/builtin"
)

const siteReferenceProtocolSchema = `
CREATE TABLE IF NOT EXISTS native_site_reference_protocols(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS native_site_reference_sources(source_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS native_site_reference_transitions(source_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);`

const sourceSiteReferenceProtocol = "native-numeric/custom-string/v1"
const originalUnclaimedSiteReferenceProtocol = "moviepilot-reference-unclaimed/v1"

func pluginErrorCodeInternal(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

type siteReferenceProtocol struct {
	Version                                                    int
	Purpose, Protocol, Instance, Plugin, PluginVersion, Source string
	Generation                                                 int64
}

func siteReferencePlugin(id string) bool {
	return id == "IYUUAutoSeed" || id == "MergeSiteSwitch" || slices.ContainsFunc(builtin.SiteControlSlots, func(s builtin.SiteControlSlot) bool { return s.PluginID == id })
}

func (p *PluginStore) readSiteReferenceProtocol(ctx context.Context, q pluginRemoveLinkJournalReader, in PluginInstallation) (siteReferenceProtocol, error) {
	var sealed []byte
	var out siteReferenceProtocol
	err := q.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_protocols WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, bad("PLUGIN_SITE_PROTOCOL_UNCLAIMED", "站点引用协议未确认，需先完成来源核对和配置迁移")
	}
	if err != nil {
		return out, err
	}
	if p.store.Unseal(sealed, &out) != nil || out.Version != 1 || out.Purpose != "site-reference-protocol/v1" || out.Instance != in.InstanceID || out.Generation != in.Generation || out.Plugin != in.PluginID || out.PluginVersion != in.Version {
		return out, bad("PLUGIN_SITE_PROTOCOL_INVALID", "站点引用协议记录无法校验")
	}
	if out.Protocol != sourceSiteReferenceProtocol {
		if out.Protocol == originalUnclaimedSiteReferenceProtocol {
			return out, bad("PLUGIN_SITE_PROTOCOL_UNCLAIMED", "原版站点编号尚未完成独立引用映射，不能保存后直接激活")
		}
		return out, bad("PLUGIN_SITE_PROTOCOL_INVALID", "未知的站点引用协议")
	}
	if out.Source != "host-install/v1" && out.Source != "reset-empty/v1" {
		var receipt legacySiteControlTransition
		if !pluginStorageDigest.MatchString(out.Source) || q.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_transitions WHERE source_id=? AND instance_id=? AND generation=?`, out.Source, in.InstanceID, in.Generation).Scan(&sealed) != nil || p.store.Unseal(sealed, &receipt) != nil || receipt.Version != 1 || receipt.Purpose != "site-reference-transition/v1" || receipt.Source != out.Source || receipt.Instance != in.InstanceID || receipt.Generation != in.Generation || receipt.AfterRevision != receipt.BeforeRevision+1 || !pluginStorageDigest.MatchString(receipt.BeforeSHA) || !pluginStorageDigest.MatchString(receipt.AfterSHA) {
			return out, bad("PLUGIN_SITE_PROTOCOL_INVALID", "站点引用转换来源收据缺失或无法校验")
		}
	}
	return out, nil
}

func (p *PluginStore) writeSiteReferenceProtocolTx(ctx context.Context, tx *sql.Tx, in PluginInstallation, source string) error {
	if !siteReferencePlugin(in.PluginID) {
		return nil
	}
	f := siteReferenceProtocol{Version: 1, Purpose: "site-reference-protocol/v1", Protocol: sourceSiteReferenceProtocol, Instance: in.InstanceID, Plugin: in.PluginID, PluginVersion: in.Version, Generation: in.Generation, Source: source}
	sealed, err := p.store.Seal(f)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_site_reference_protocols VALUES(?,?,?)`, in.InstanceID, in.Generation, sealed)
	return err
}

// A new installation declares this host's source protocol. Retained data can
// inherit it only from an authenticated prior generation of the same instance.
// Missing history never means "probably opaque": it remains unclaimed.
func (p *PluginStore) prepareSiteReferenceInstallTx(ctx context.Context, tx *sql.Tx, in PluginInstallation) error {
	if !siteReferencePlugin(in.PluginID) {
		return nil
	}
	var previous PluginInstallation
	var retained int
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, in.InstanceID).Scan(&previous.PluginID, &previous.Version, &previous.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	previous.InstanceID = in.InstanceID
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=?`, in.InstanceID).Scan(&retained); err != nil {
		return err
	}
	if retained == 0 {
		return nil
	}
	// Uninstall can advance the lifecycle generation while retaining all data.
	// Find the most recent proven protocol; no config interpretation occurs here.
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM native_site_reference_protocols WHERE instance_id=? AND generation<=? ORDER BY generation DESC LIMIT 1`, in.InstanceID, previous.Generation).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return bad("PLUGIN_SITE_PROTOCOL_UNCLAIMED", "保留的站点引用缺少来源协议，不能直接重装运行")
	}
	if err != nil {
		return err
	}
	previous.Generation = generation
	_, err = p.readSiteReferenceProtocol(ctx, tx, previous)
	return err
}

func (p *PluginStore) markInstalledSiteReferencesTx(ctx context.Context, tx *sql.Tx, instance string, source string) error {
	var in PluginInstallation
	in.InstanceID = instance
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.PluginID, &in.Version, &in.Generation); err != nil {
		return err
	}
	return p.writeSiteReferenceProtocolTx(ctx, tx, in, source)
}

func (p *PluginStore) requireSiteReferenceProtocolTx(ctx context.Context, tx *sql.Tx, instance string) error {
	var in PluginInstallation
	in.InstanceID = instance
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.PluginID, &in.Version, &in.Generation); err != nil {
		return err
	}
	if !siteReferencePlugin(in.PluginID) {
		return nil
	}
	_, err := p.readSiteReferenceProtocol(ctx, tx, in)
	return err
}

// Source configs are already typed. Conversion belongs only to an independently
// authenticated old-format transition, never to form reads or ordinary saves.
func sourceSiteControls(c builtin.SiteControls) (builtin.SiteControls, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c.Clone(), nil
}

func sourceReferenceArray(raw json.RawMessage) ([]builtin.SiteReference, error) {
	var refs []builtin.SiteReference
	if json.Unmarshal(raw, &refs) != nil {
		return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "站点引用格式无效，配置未应用")
	}
	if refs == nil {
		refs = []builtin.SiteReference{}
	}
	return refs, nil
}

func (p *PluginStore) stageImportedSiteReferencesTx(ctx context.Context, tx *sql.Tx, instance, migration string) error {
	var in PluginInstallation
	in.InstanceID = instance
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.PluginID, &in.Version, &in.Generation); err != nil {
		return err
	}
	if !siteReferencePlugin(in.PluginID) {
		return nil
	}
	f := siteReferenceProtocol{Version: 1, Purpose: "site-reference-protocol/v1", Protocol: originalUnclaimedSiteReferenceProtocol, Instance: instance, Plugin: in.PluginID, PluginVersion: in.Version, Generation: in.Generation, Source: "moviepilot-import/v1:" + migration}
	sealed, err := p.store.Seal(f)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE native_site_reference_protocols SET sealed=? WHERE instance_id=? AND generation=?`, sealed, instance, in.Generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return bad("PLUGIN_SITE_PROTOCOL_INVALID", "导入协议记录缺失")
	}
	return nil
}
